import * as cdk from 'aws-cdk-lib';
import { Construct } from 'constructs';
import * as lambda from 'aws-cdk-lib/aws-lambda';
import * as apigwv2 from 'aws-cdk-lib/aws-apigatewayv2';
import { HttpLambdaIntegration } from 'aws-cdk-lib/aws-apigatewayv2-integrations';
import * as dynamodb from 'aws-cdk-lib/aws-dynamodb';
import * as sqs from 'aws-cdk-lib/aws-sqs'; // <-- NEW: Import SQS (Queues)
import * as pipes from 'aws-cdk-lib/aws-pipes'; // <-- NEW: Import Pipes
import * as iam from 'aws-cdk-lib/aws-iam'; // <-- NEW: Import Security Roles
import { SqsEventSource } from 'aws-cdk-lib/aws-lambda-event-sources';
import * as cloudwatch from 'aws-cdk-lib/aws-cloudwatch';
import * as path from 'path';

export class InfraStack extends cdk.Stack {
  constructor(scope: Construct, id: string, props?: cdk.StackProps) {
    super(scope, id, props);

    // =====================================================================
    // 1. DATABASE
    // =====================================================================
    const productsTable = new dynamodb.Table(this, 'ProductsTable', {
      partitionKey: { name: 'productId', type: dynamodb.AttributeType.STRING },
      billingMode: dynamodb.BillingMode.PAY_PER_REQUEST,
      removalPolicy: cdk.RemovalPolicy.DESTROY,
    });

    const ordersTable = new dynamodb.Table(this, 'OrdersTable', {
      partitionKey: { name: 'orderId', type: dynamodb.AttributeType.STRING },
      billingMode: dynamodb.BillingMode.PAY_PER_REQUEST,
      removalPolicy: cdk.RemovalPolicy.DESTROY,
      
      // NEW: Turn on the "Security Camera" to watch for new orders!
      stream: dynamodb.StreamViewType.NEW_IMAGE, 
    });

     // =====================================================================
    // 2. THE OUTBOX QUEUE & DEAD-LETTER QUEUE
    // =====================================================================
    
    // NEW: The Dead-Letter Queue. If a message fails 3 times, it goes here!
    const deadLetterQueue = new sqs.Queue(this, 'OrderDLQ');

    const fulfillmentQueue = new sqs.Queue(this, 'FulfillmentQueue', {
      deadLetterQueue: {
        queue: deadLetterQueue,
        maxReceiveCount: 3, // Retry 3 times before giving up
      }
    });

    const pipeRole = new iam.Role(this, 'PipeRole', {
      assumedBy: new iam.ServicePrincipal('pipes.amazonaws.com'),
    });
    ordersTable.grantStreamRead(pipeRole);
    fulfillmentQueue.grantSendMessages(pipeRole);

    new pipes.CfnPipe(this, 'OrderOutboxPipe', {
      name: 'OrderOutboxPipe',
      roleArn: pipeRole.roleArn,
      source: ordersTable.tableStreamArn!,
      sourceParameters: { dynamoDbStreamParameters: { startingPosition: 'LATEST', batchSize: 1 } },
      target: fulfillmentQueue.queueArn,
    });

    // =====================================================================
    // 3. COMPUTE (API & WORKER)
    // =====================================================================
    
    // The original API (Hostess)
    const apiLambda = new lambda.Function(this, 'FlashCartApiLambda', {
      runtime: lambda.Runtime.PROVIDED_AL2023,
      architecture: lambda.Architecture.ARM_64,
      handler: 'bootstrap',
      code: lambda.Code.fromAsset(path.join(__dirname, '../../backend/cmd/api')),
      environment: { PRODUCTS_TABLE: productsTable.tableName, ORDERS_TABLE: ordersTable.tableName }
    });

    // NEW: The Worker (Shipping Department)
    const workerLambda = new lambda.Function(this, 'FlashCartWorkerLambda', {
      runtime: lambda.Runtime.PROVIDED_AL2023,
      architecture: lambda.Architecture.ARM_64,
      handler: 'bootstrap',
      code: lambda.Code.fromAsset(path.join(__dirname, '../../backend/cmd/worker')),
      environment: { PRODUCTS_TABLE: productsTable.tableName, ORDERS_TABLE: ordersTable.tableName }
    });

    productsTable.grantReadWriteData(apiLambda);
    ordersTable.grantReadWriteData(apiLambda);
    
    productsTable.grantReadWriteData(workerLambda);
    ordersTable.grantReadWriteData(workerLambda);

    // NEW: Plug the SQS Queue into the Worker Lambda!
    workerLambda.addEventSource(new SqsEventSource(fulfillmentQueue, {
      reportBatchItemFailures: true, // Let Go tell AWS exactly which message failed
    }));

    // =====================================================================
    // 4. API GATEWAY
    // =====================================================================
    const lambdaIntegration = new HttpLambdaIntegration('ApiIntegration', apiLambda);
    const httpApi = new apigwv2.HttpApi(this, 'FlashCartHttpApi', { apiName: 'FlashCart API' });
    httpApi.addRoutes({ path: '/{proxy+}', integration: lambdaIntegration });

    // =====================================================================
    // 5. OBSERVABILITY (Dashboards & Alarms)
    // =====================================================================
    
    // 1. Create the Dashboard Board
    const dashboard = new cloudwatch.Dashboard(this, 'FlashCartDashboard', {
      dashboardName: 'FlashCart-Live-Metrics',
    });

    // 2. Graph: API Latency (How fast is our Hostess?)
    const latencyWidget = new cloudwatch.GraphWidget({
      title: 'API Latency (p50 & p99)',
      left: [
        httpApi.metric('Latency', { statistic: 'p50' }),
        httpApi.metric('Latency', { statistic: 'p99' })
      ]
    });

    // 3. Graph: The EMF Metrics we just created in Go!
    const customMetricsWidget = new cloudwatch.GraphWidget({
      title: 'Sales & Failures',
      left: [
        new cloudwatch.Metric({ namespace: 'FlashCart', metricName: 'OrdersPlaced', statistic: 'sum' }),
        new cloudwatch.Metric({ namespace: 'FlashCart', metricName: 'PaymentFailures', statistic: 'sum' }),
        new cloudwatch.Metric({ namespace: 'FlashCart', metricName: 'SoldOutRejections', statistic: 'sum' })
      ]
    });

    // 4. Graph: Dead Letter Queue Size
    const dlqWidget = new cloudwatch.GraphWidget({
      title: 'Dead Letter Queue (Poison Pills)',
      left: [ deadLetterQueue.metricApproximateNumberOfMessagesVisible() ]
    });

    // Add the graphs to the board!
    dashboard.addWidgets(latencyWidget, customMetricsWidget, dlqWidget);

    // 5. ALARM: If a message hits the Dead Letter Queue, sound the alarm!
    new cloudwatch.Alarm(this, 'DLQAlarm', {
      metric: deadLetterQueue.metricApproximateNumberOfMessagesVisible(),
      threshold: 1,      // If we get even ONE message stuck...
      evaluationPeriods: 1, // ...trigger the alarm immediately.
      comparisonOperator: cloudwatch.ComparisonOperator.GREATER_THAN_OR_EQUAL_TO_THRESHOLD,
    });

    new cdk.CfnOutput(this, 'ApiUrl', { value: httpApi.apiEndpoint });
  }
}