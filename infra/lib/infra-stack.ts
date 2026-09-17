import * as cdk from 'aws-cdk-lib';
import { Construct } from 'constructs';
import * as lambda from 'aws-cdk-lib/aws-lambda';
import * as apigwv2 from 'aws-cdk-lib/aws-apigatewayv2';
import { HttpLambdaIntegration } from 'aws-cdk-lib/aws-apigatewayv2-integrations';
import * as path from 'path';

export class InfraStack extends cdk.Stack {
  constructor(scope: Construct, id: string, props?: cdk.StackProps) {
    super(scope, id, props);

    // 1. Define the Go Lambda Function
    const apiLambda = new lambda.Function(this, 'FlashCartApiLambda', {
      runtime: lambda.Runtime.PROVIDED_AL2023, // Recommended by the PDF
      architecture: lambda.Architecture.ARM_64,
      handler: 'bootstrap',
      // Point CDK to the folder containing our compiled 'bootstrap' file
      code: lambda.Code.fromAsset(path.join(__dirname, '../../backend/cmd/api')), 
    });

    // 2. Create the API Gateway HTTP API integration
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