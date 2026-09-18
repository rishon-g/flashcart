import * as cdk from 'aws-cdk-lib';
import { Construct } from 'constructs';
import * as lambda from 'aws-cdk-lib/aws-lambda';
import * as apigwv2 from 'aws-cdk-lib/aws-apigatewayv2';
import { HttpLambdaIntegration } from 'aws-cdk-lib/aws-apigatewayv2-integrations';
import * as dynamodb from 'aws-cdk-lib/aws-dynamodb';
import * as path from 'path';

export class InfraStack extends cdk.Stack {
  constructor(scope: Construct, id: string, props?: cdk.StackProps) {
    super(scope, id, props);

      // =====================================================================
    // 1. DATABASE: Create the DynamoDB Tables
    // =====================================================================
    
    const productsTable = new dynamodb.Table(this, 'ProductsTable', {
      partitionKey: { name: 'productId', type: dynamodb.AttributeType.STRING },
      billingMode: dynamodb.BillingMode.PAY_PER_REQUEST, // Only pay when we use it
      removalPolicy: cdk.RemovalPolicy.DESTROY, // Deletes the DB when we delete the project
    });

    const ordersTable = new dynamodb.Table(this, 'OrdersTable', {
      partitionKey: { name: 'orderId', type: dynamodb.AttributeType.STRING },
      billingMode: dynamodb.BillingMode.PAY_PER_REQUEST,
      removalPolicy: cdk.RemovalPolicy.DESTROY,
    });

    // =====================================================================
    // 2. COMPUTE: Create the Lambda Function
    // =====================================================================
    const apiLambda = new lambda.Function(this, 'FlashCartApiLambda', {
      runtime: lambda.Runtime.PROVIDED_AL2023,
      architecture: lambda.Architecture.ARM_64,
      handler: 'bootstrap',
      code: lambda.Code.fromAsset(path.join(__dirname, '../../backend/cmd/api')),
      
      // NEW: Tell our Go code the actual names of the tables using Environment Variables!
      environment: {
        PRODUCTS_TABLE: productsTable.tableName,
        ORDERS_TABLE: ordersTable.tableName,
      }
    });

    // NEW: Security! AWS blocks everything by default. 
    // We explicitly give our Lambda permission to read/write to our new tables.
    productsTable.grantReadWriteData(apiLambda);
    ordersTable.grantReadWriteData(apiLambda);

    // =====================================================================
    // 3. API GATEWAY: Create the Public URL
    // =====================================================================
    const lambdaIntegration = new HttpLambdaIntegration('ApiIntegration', apiLambda);

    const httpApi = new apigwv2.HttpApi(this, 'FlashCartHttpApi', {
      apiName: 'FlashCart API',
    });

    // 3. Route all traffic (ANY method, to any path) to our Lambda
    httpApi.addRoutes({
      path: '/{proxy+}',
      integration: lambdaIntegration,
    });

    // 4. Output the URL to the terminal after deployment!
    new cdk.CfnOutput(this, 'ApiUrl', {
      value: httpApi.apiEndpoint,
    });
  }
}