import * as cdk from 'aws-cdk-lib';
import { Construct } from 'constructs';
import * as lambda from 'aws-cdk-lib/aws-lambda';
import * as apigwv2 from 'aws-cdk-lib/aws-apigatewayv2';
import { HttpLambdaIntegration } from 'aws-cdk-lib/aws-apigatewayv2-integrations';
import * as dynamodb from 'aws-cdk-lib/aws-dynamodb';
import * as sqs from 'aws-cdk-lib/aws-sqs';
import * as pipes from 'aws-cdk-lib/aws-pipes';
import * as iam from 'aws-cdk-lib/aws-iam';
import { SqsEventSource } from 'aws-cdk-lib/aws-lambda-event-sources';
import * as cloudwatch from 'aws-cdk-lib/aws-cloudwatch';
import * as cloudwatchActions from 'aws-cdk-lib/aws-cloudwatch-actions';
import * as sns from 'aws-cdk-lib/aws-sns';
import * as subscriptions from 'aws-cdk-lib/aws-sns-subscriptions';
import * as secretsmanager from 'aws-cdk-lib/aws-secretsmanager';
import * as path from 'path';


import * as s3 from 'aws-cdk-lib/aws-s3';
import * as cloudfront from 'aws-cdk-lib/aws-cloudfront';
import * as origins from 'aws-cdk-lib/aws-cloudfront-origins';
import * as s3deploy from 'aws-cdk-lib/aws-s3-deployment';

export class InfraStack extends cdk.Stack {
  constructor(scope: Construct, id: string, props?: cdk.StackProps) {
    super(scope, id, props);

    // database
    const productsTable = new dynamodb.Table(this, 'ProductsTable', {
      partitionKey: { name: 'productId', type: dynamodb.AttributeType.STRING },
      billingMode: dynamodb.BillingMode.PAY_PER_REQUEST,
      removalPolicy: cdk.RemovalPolicy.DESTROY,
    });

    const ordersTable = new dynamodb.Table(this, 'OrdersTable', {
      partitionKey: { name: 'orderId', type: dynamodb.AttributeType.STRING },
      billingMode: dynamodb.BillingMode.PAY_PER_REQUEST,
      removalPolicy: cdk.RemovalPolicy.DESTROY,
      
      // stream new orders for the outbox
      stream: dynamodb.StreamViewType.NEW_IMAGE, 
    });

    // outbox queue and dead-letter queue
    
    // messages land here after 3 failed receives
    const deadLetterQueue = new sqs.Queue(this, 'OrderDLQ');

    const fulfillmentQueue = new sqs.Queue(this, 'FulfillmentQueue', {
      deadLetterQueue: {
        queue: deadLetterQueue,
        maxReceiveCount: 3,
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
      sourceParameters: {
        // only forward inserts, not the worker's status updates
        filterCriteria: {
          filters: [{ pattern: '{ "eventName": ["INSERT"] }' }]
        },
        dynamoDbStreamParameters: { 
          startingPosition: 'LATEST', 
          batchSize: 1 
        }
      },
      target: fulfillmentQueue.queueArn,
    });

    // compute
    
    // shared secret for the admin restock endpoint
    const adminTokenSecret = new secretsmanager.Secret(this, 'AdminTokenSecret', {
      description: 'Value for the X-Admin-Token header on POST /admin/products',
      generateSecretString: { excludePunctuation: true, passwordLength: 32 },
    });

    // api
    const apiLambda = new lambda.Function(this, 'FlashCartApiLambda', {
      runtime: lambda.Runtime.PROVIDED_AL2023,
      architecture: lambda.Architecture.ARM_64,
      handler: 'bootstrap',
      code: lambda.Code.fromAsset(path.join(__dirname, '../../backend/cmd/api')),
      environment: {
        PRODUCTS_TABLE: productsTable.tableName,
        ORDERS_TABLE: ordersTable.tableName,
        ADMIN_TOKEN_SECRET_ARN: adminTokenSecret.secretArn,
      },
      memorySize: 1024, // more memory also means more cpu
    });
    adminTokenSecret.grantRead(apiLambda);

    // fulfillment worker
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

    // worker reads from the fulfillment queue
    workerLambda.addEventSource(new SqsEventSource(fulfillmentQueue, {
      reportBatchItemFailures: true, // retry only the failed messages in a batch
    }));

    // api gateway
    const lambdaIntegration = new HttpLambdaIntegration('ApiIntegration', apiLambda);
    const httpApi = new apigwv2.HttpApi(this, 'FlashCartHttpApi', { 
      apiName: 'FlashCart API',
      // let the dashboard call the api from the browser
      corsPreflight: {
        allowOrigins: ['*'],
        allowMethods: [apigwv2.CorsHttpMethod.ANY],
        allowHeaders: ['*'],
      },
    });
    httpApi.addRoutes({ path: '/{proxy+}', integration: lambdaIntegration });

    // observability
    
    // cloudwatch dashboard
    const dashboard = new cloudwatch.Dashboard(this, 'FlashCartDashboard', {
      dashboardName: 'FlashCart-Live-Metrics',
    });

    // api latency
    const latencyWidget = new cloudwatch.GraphWidget({
      title: 'API Latency (p50 & p99)',
      left: [
        httpApi.metric('Latency', { statistic: 'p50' }),
        httpApi.metric('Latency', { statistic: 'p99' })
      ]
    });

    // custom metrics from the lambdas
    const customMetricsWidget = new cloudwatch.GraphWidget({
      title: 'Sales & Failures',
      left: [
        new cloudwatch.Metric({ namespace: 'FlashCart', metricName: 'OrdersPlaced', statistic: 'sum' }),
        new cloudwatch.Metric({ namespace: 'FlashCart', metricName: 'PaymentFailures', statistic: 'sum' }),
        new cloudwatch.Metric({ namespace: 'FlashCart', metricName: 'SoldOutRejections', statistic: 'sum' })
      ]
    });

    // dlq depth
    const dlqWidget = new cloudwatch.GraphWidget({
      title: 'Dead Letter Queue (Poison Pills)',
      left: [ deadLetterQueue.metricApproximateNumberOfMessagesVisible() ]
    });

    dashboard.addWidgets(latencyWidget, customMetricsWidget, dlqWidget);

    // alarm as soon as anything lands in the dlq
    // pass -c alertEmail=you@example.com to get it by email
    const alarmTopic = new sns.Topic(this, 'AlarmTopic');
    const alertEmail = this.node.tryGetContext('alertEmail');
    if (alertEmail) {
      alarmTopic.addSubscription(new subscriptions.EmailSubscription(alertEmail));
    }

    const dlqAlarm = new cloudwatch.Alarm(this, 'DLQAlarm', {
      metric: deadLetterQueue.metricApproximateNumberOfMessagesVisible(),
      threshold: 1,
      evaluationPeriods: 1,
      comparisonOperator: cloudwatch.ComparisonOperator.GREATER_THAN_OR_EQUAL_TO_THRESHOLD,
    });
    dlqAlarm.addAlarmAction(new cloudwatchActions.SnsAction(alarmTopic));

    // ci/cd (github oidc)
    
    // trust github's oidc provider
    const githubProvider = new iam.OpenIdConnectProvider(this, 'GithubOIDCProvider', {
      url: 'https://token.actions.githubusercontent.com',
      clientIds: ['sts.amazonaws.com'],
    });

    // role for the deploy workflow, limited to main on this repo
    const githubRole = new iam.Role(this, 'GitHubDeployRole', {
  assumedBy: new iam.WebIdentityPrincipal(githubProvider.openIdConnectProviderArn, {
    StringEquals: {
      'token.actions.githubusercontent.com:aud': 'sts.amazonaws.com',
      'token.actions.githubusercontent.com:repository': 'rishon-g/flashcart',
      'token.actions.githubusercontent.com:ref': 'refs/heads/main',
      'token.actions.githubusercontent.com:job_workflow_ref': 'rishon-g/flashcart/.github/workflows/deploy.yml@refs/heads/main',
    },
  }),
  description: 'Role assumed by GitHub Actions to deploy the CDK app',
});

    // frontend hosting
    
    // bucket for the built dashboard
    const websiteBucket = new s3.Bucket(this, 'FlashCartWebsiteBucket', {
      removalPolicy: cdk.RemovalPolicy.DESTROY,
      autoDeleteObjects: true,
    });

    // cloudfront in front of the bucket, https only
    const distribution = new cloudfront.Distribution(this, 'FlashCartDistribution', {
      defaultBehavior: {
        origin: new origins.S3Origin(websiteBucket),
        viewerProtocolPolicy: cloudfront.ViewerProtocolPolicy.REDIRECT_TO_HTTPS,
      },
      defaultRootObject: 'index.html',
    });

    // upload web/dist on every deploy
    new s3deploy.BucketDeployment(this, 'DeployWebsite', {
      sources: [s3deploy.Source.asset(path.join(__dirname, '../../web/dist'))],
      destinationBucket: websiteBucket,
      distribution,
      distributionPaths: ['/*'], // invalidate the cache on each deploy
    });

    new cdk.CfnOutput(this, 'WebsiteUrl', { value: distribution.distributionDomainName });

    // the deploy role can only assume the cdk bootstrap roles,
    // which do the actual publishing and deploys
    githubRole.addToPolicy(new iam.PolicyStatement({
      actions: ['sts:AssumeRole'],
      resources: [`arn:${cdk.Aws.PARTITION}:iam::${cdk.Aws.ACCOUNT_ID}:role/cdk-*`],
    }));

    // arn to paste into deploy.yml
    new cdk.CfnOutput(this, 'GitHubRoleArn', { value: githubRole.roleArn });

    new cdk.CfnOutput(this, 'ApiUrl', { value: httpApi.apiEndpoint });
    new cdk.CfnOutput(this, 'AdminTokenSecretArn', { value: adminTokenSecret.secretArn });
    new cdk.CfnOutput(this, 'AlarmTopicArn', { value: alarmTopic.topicArn });
  }
}