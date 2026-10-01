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
