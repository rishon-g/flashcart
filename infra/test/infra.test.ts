import * as fs from 'fs';
import * as path from 'path';
import * as cdk from 'aws-cdk-lib';
import { Match, Template } from 'aws-cdk-lib/assertions';
import { InfraStack } from '../lib/infra-stack';

let template: Template;

beforeAll(() => {
  // the stack bundles web/dist, so make an empty one if the frontend isn't built yet
  fs.mkdirSync(path.join(__dirname, '../../web/dist'), { recursive: true });

  const app = new cdk.App();
  template = Template.fromStack(new InfraStack(app, 'TestStack'));
});

test('Orders table is keyed on orderId and streams new images', () => {
  template.hasResourceProperties('AWS::DynamoDB::Table', {
    KeySchema: [{ AttributeName: 'orderId', KeyType: 'HASH' }],
    StreamSpecification: { StreamViewType: 'NEW_IMAGE' },
  });
});

test('Products table is keyed on productId', () => {
  template.hasResourceProperties('AWS::DynamoDB::Table', {
    KeySchema: [{ AttributeName: 'productId', KeyType: 'HASH' }],
  });
});

test('Outbox pipe forwards only INSERT events', () => {
  template.hasResourceProperties('AWS::Pipes::Pipe', {
    SourceParameters: {
      FilterCriteria: { Filters: [{ Pattern: '{ "eventName": ["INSERT"] }' }] },
    },
  });
});

test('Fulfillment queue dead-letters after 3 receives', () => {
  template.hasResourceProperties('AWS::SQS::Queue', {
    RedrivePolicy: { maxReceiveCount: 3 },
  });
});

test('Worker reports partial batch failures', () => {
  template.hasResourceProperties('AWS::Lambda::EventSourceMapping', {
    FunctionResponseTypes: ['ReportBatchItemFailures'],
  });
});

test('DLQ alarm notifies the alarm topic', () => {
  template.hasResourceProperties('AWS::CloudWatch::Alarm', {
    AlarmActions: [{ Ref: Match.stringLikeRegexp('AlarmTopic') }],
  });
});

test('API Lambda can read the admin token secret', () => {
  template.hasResourceProperties('AWS::Lambda::Function', {
    Environment: { Variables: Match.objectLike({ ADMIN_TOKEN_SECRET_ARN: Match.anyValue() }) },
  });
});

test('GitHub deploy role is not an administrator', () => {
  const roles = template.findResources('AWS::IAM::Role', {
    Properties: { Description: 'Role assumed by GitHub Actions to deploy the CDK app' },
  });
  const [role] = Object.values(roles);
  expect(role).toBeDefined();
  expect(JSON.stringify(role.Properties.ManagedPolicyArns ?? [])).not.toContain('AdministratorAccess');
});

test('GitHub deploy role can only be assumed from main of this repo', () => {
  template.hasResourceProperties('AWS::IAM::Role', {
    Description: 'Role assumed by GitHub Actions to deploy the CDK app',
    AssumeRolePolicyDocument: {
      Statement: [Match.objectLike({
        Condition: {
          StringEquals: Match.objectLike({
            'token.actions.githubusercontent.com:repository': 'rishon-g/flashcart',
            'token.actions.githubusercontent.com:ref': 'refs/heads/main',
          }),
        },
      })],
    },
  });
});

test('CloudFront redirects HTTP viewers to HTTPS', () => {
  template.hasResourceProperties('AWS::CloudFront::Distribution', {
    DistributionConfig: Match.objectLike({
      DefaultCacheBehavior: Match.objectLike({ ViewerProtocolPolicy: 'redirect-to-https' }),
      DefaultRootObject: 'index.html',
    }),
  });
});

test('Alarm topic gets an email subscription only when alertEmail is set', () => {
  template.resourceCountIs('AWS::SNS::Subscription', 0);

  const app = new cdk.App({ context: { alertEmail: 'ops@example.com' } });
  const withEmail = Template.fromStack(new InfraStack(app, 'AlertStack'));
  withEmail.hasResourceProperties('AWS::SNS::Subscription', {
    Protocol: 'email',
    Endpoint: 'ops@example.com',
  });
});

test('DLQ alarm fires as soon as one message is dead-lettered', () => {
  template.hasResourceProperties('AWS::CloudWatch::Alarm', {
    MetricName: 'ApproximateNumberOfMessagesVisible',
    Threshold: 1,
    EvaluationPeriods: 1,
    ComparisonOperator: 'GreaterThanOrEqualToThreshold',
  });
});

test('Both Lambdas run the arm64 bootstrap binary on provided.al2023', () => {
  // the deploy builds with GOOS=linux GOARCH=arm64 -o bootstrap, so these must match
  template.resourcePropertiesCountIs('AWS::Lambda::Function', {
    Runtime: 'provided.al2023',
    Architectures: ['arm64'],
    Handler: 'bootstrap',
  }, 2);
});

test('Both Go Lambdas are told the products and orders table names', () => {
  template.resourcePropertiesCountIs('AWS::Lambda::Function', {
    Environment: {
      Variables: Match.objectLike({
        PRODUCTS_TABLE: { Ref: Match.stringLikeRegexp('^ProductsTable') },
        ORDERS_TABLE: { Ref: Match.stringLikeRegexp('^OrdersTable') },
      }),
    },
  }, 2);
});

test('HTTP API sends every method and path to the API Lambda', () => {
  template.hasResourceProperties('AWS::ApiGatewayV2::Route', {
    RouteKey: 'ANY /{proxy+}',
    Target: Match.objectLike({ 'Fn::Join': Match.anyValue() }),
  });
  template.hasResourceProperties('AWS::ApiGatewayV2::Integration', {
    IntegrationType: 'AWS_PROXY',
    IntegrationUri: { 'Fn::GetAtt': [Match.stringLikeRegexp('FlashCartApiLambda'), 'Arn'] },
  });
});

test('HTTP API answers browser preflights for any origin, method and header', () => {
  template.hasResourceProperties('AWS::ApiGatewayV2::Api', {
    CorsConfiguration: { AllowOrigins: ['*'], AllowMethods: ['*'], AllowHeaders: ['*'] },
  });
});

test('Admin token secret is a generated 32-character token with no punctuation', () => {
  // the token is pasted into an HTTP header, so it must not need escaping
  template.hasResourceProperties('AWS::SecretsManager::Secret', {
    GenerateSecretString: { PasswordLength: 32, ExcludePunctuation: true },
  });
});

test('Both tables use on-demand billing so a sale spike is not throttled by provisioned capacity', () => {
  const tables = template.findResources('AWS::DynamoDB::Table');
  expect(Object.keys(tables)).toHaveLength(2);
  for (const table of Object.values(tables)) {
    expect(table.Properties.BillingMode).toBe('PAY_PER_REQUEST');
  }
});

test('Stack outputs the values the README says a deploy prints', () => {
  for (const name of ['ApiUrl', 'WebsiteUrl', 'AdminTokenSecretArn', 'AlarmTopicArn', 'GitHubRoleArn']) {
    template.hasOutput(name, { Value: Match.anyValue() });
  }
});

test('GitHub deploy role may only assume the CDK bootstrap roles', () => {
  const roleId = Object.keys(template.findResources('AWS::IAM::Role', {
    Properties: { Description: 'Role assumed by GitHub Actions to deploy the CDK app' },
  }))[0];
  const policies = Object.values(template.findResources('AWS::IAM::Policy', {
    Properties: { Roles: [{ Ref: roleId }] },
  }));
  expect(policies).toHaveLength(1);
  const statements = policies[0].Properties.PolicyDocument.Statement;
  expect(statements).toHaveLength(1);
  expect(statements[0].Action).toBe('sts:AssumeRole');
  expect(JSON.stringify(statements[0].Resource)).toContain(':role/cdk-*');
});

test('GitHub deploy role only trusts STS-audience tokens from the deploy.yml workflow on main', () => {
  template.hasResourceProperties('AWS::IAM::Role', {
    Description: 'Role assumed by GitHub Actions to deploy the CDK app',
    AssumeRolePolicyDocument: {
      Statement: [Match.objectLike({
        Condition: {
          StringEquals: Match.objectLike({
            'token.actions.githubusercontent.com:aud': 'sts.amazonaws.com',
            'token.actions.githubusercontent.com:job_workflow_ref': 'rishon-g/flashcart/.github/workflows/deploy.yml@refs/heads/main',
          }),
        },
      })],
    },
  });
});

test('Outbox pipe reads one new stream record at a time, since the worker parses one record per message', () => {
  template.hasResourceProperties('AWS::Pipes::Pipe', {
    SourceParameters: Match.objectLike({
      DynamoDBStreamParameters: { StartingPosition: 'LATEST', BatchSize: 1 },
    }),
  });
});

test('Outbox pipe carries the orders table stream to the fulfillment queue, not the DLQ', () => {
  template.hasResourceProperties('AWS::Pipes::Pipe', {
    Source: { 'Fn::GetAtt': [Match.stringLikeRegexp('^OrdersTable'), 'StreamArn'] },
    Target: { 'Fn::GetAtt': [Match.stringLikeRegexp('^FulfillmentQueue'), 'Arn'] },
  });
});

test('Only the worker Lambda consumes the fulfillment queue', () => {
  const mappings = Object.values(template.findResources('AWS::Lambda::EventSourceMapping'));
  expect(mappings).toHaveLength(1);
  expect(mappings[0].Properties.EventSourceArn['Fn::GetAtt'][0]).toMatch(/^FulfillmentQueue/);
  expect(mappings[0].Properties.FunctionName.Ref).toMatch(/^FlashCartWorkerLambda/);
});
