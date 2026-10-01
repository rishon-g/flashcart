#!/usr/bin/env node
import * as cdk from 'aws-cdk-lib/core';
import { InfraStack } from '../lib/infra-stack';

const app = new cdk.App();
// no env set, so the stack isn't tied to one account or region
new InfraStack(app, 'InfraStack');
