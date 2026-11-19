# FlashCart infrastructure

The CDK app that defines `InfraStack`: the DynamoDB tables, EventBridge pipe, SQS queues, both Go Lambdas, the HTTP API, the CloudFront site, alarms, and the GitHub deploy role. Build the Lambda `bootstrap` binaries and `web/dist` before deploying (see the root README's **Getting started**).

The `cdk.json` file tells the CDK Toolkit how to execute your app.

## Useful commands

* `npm run build`   type-check the project
* `npm run watch`   watch for changes and type-check
* `npm run test`    perform the jest unit tests
* `npx cdk deploy`  deploy this stack to your default AWS account/region
* `npx cdk diff`    compare deployed stack with current state
* `npx cdk synth`   emits the synthesized CloudFormation template
