import * as cdk from 'aws-cdk-lib';
import { Construct } from 'constructs';
import * as lambda from 'aws-cdk-lib/aws-lambda';
import * as apigwv2 from 'aws-cdk-lib/aws-apigatewayv2';
import { HttpLambdaIntegration } from 'aws-cdk-lib/aws-apigatewayv2-integrations';
import * as dynamodb from 'aws-cdk-lib/aws-dynamodb';
import * as sqs from 'aws-cdk-lib/aws-sqs'; // <-- NEW: Import SQS (Queues)
import * as pipes from 'aws-cdk-lib/aws-pipes'; // <-- NEW: Import Pipes
import * as iam from 'aws-cdk-lib/aws-iam'; // <-- NEW: Import Security Roles
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
    // 2. THE OUTBOX QUEUE (SQS) & PLUMBING (EventBridge Pipes)
    // =====================================================================
    
    // Create the waiting room for orders waiting to be fulfilled
    const fulfillmentQueue = new sqs.Queue(this, 'FulfillmentQueue');

    // Security: Create a "badge" that allows AWS Pipes to read the DB and send to the Queue
    const pipeRole = new iam.Role(this, 'PipeRole', {
      assumedBy: new iam.ServicePrincipal('pipes.amazonaws.com'),
    });
    ordersTable.grantStreamRead(pipeRole);
    fulfillmentQueue.grantSendMessages(pipeRole);

    // Create the Pipe connecting the Database Stream to the SQS Queue
    new pipes.CfnPipe(this, 'OrderOutboxPipe', {
      name: 'OrderOutboxPipe',
      roleArn: pipeRole.roleArn,
      source: ordersTable.tableStreamArn!, // From the database...
      sourceParameters: {
        dynamoDbStreamParameters: {
          startingPosition: 'LATEST',
          batchSize: 1, // Move one order at a time
        }
      },
      target: fulfillmentQueue.queueArn, // ...To the Queue!
    });


    // =====================================================================
    // 3. COMPUTE (The API Server)
    // =====================================================================
    const apiLambda = new lambda.Function(this, 'FlashCartApiLambda', {
      runtime: lambda.Runtime.PROVIDED_AL2023,
      architecture: lambda.Architecture.ARM_64,
      handler: 'bootstrap',
      code: lambda.Code.fromAsset(path.join(__dirname, '../../backend/cmd/api')),
      environment: {
        PRODUCTS_TABLE: productsTable.tableName,
        ORDERS_TABLE: ordersTable.tableName,
      }
    });

    productsTable.grantReadWriteData(apiLambda);
    ordersTable.grantReadWriteData(apiLambda);

    // =====================================================================
    // 4. API GATEWAY
    // =====================================================================
    const lambdaIntegration = new HttpLambdaIntegration('ApiIntegration', apiLambda);
    const httpApi = new apigwv2.HttpApi(this, 'FlashCartHttpApi', {
      apiName: 'FlashCart API',
    });
    httpApi.addRoutes({
      path: '/{proxy+}',
      integration: lambdaIntegration,
    });

    new cdk.CfnOutput(this, 'ApiUrl', { value: httpApi.apiEndpoint });
  }
}