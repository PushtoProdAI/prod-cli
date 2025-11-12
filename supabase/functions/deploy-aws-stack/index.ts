import 'jsr:@supabase/functions-js/edge-runtime.d.ts';
import { createClient } from 'jsr:@supabase/supabase-js@2';
import { initSentry, captureException, flushSentry } from '../_shared/sentry.ts';
import {
  STSClient,
  AssumeRoleCommand,
} from "npm:@aws-sdk/client-sts";
import {
  CloudFormationClient,
  CreateStackCommand,
  UpdateStackCommand,
  DescribeStacksCommand,
  DeleteStackCommand,
  DescribeStackEventsCommand,
  StackStatus,
} from "npm:@aws-sdk/client-cloudformation";

// Import types and helper functions
import type { DeploymentSpec, DeploymentResult } from './types.ts';
import { buildEnvironmentVariables, buildEnvironmentSecrets } from './env-builders.ts';
import { buildSecretsManagerResources } from './secrets-manager.ts';
import { buildNetworkingResources } from './networking.ts';
import { buildAppRunnerAccessRole, buildAppRunnerInstanceRole, buildECSTaskExecutionRole, buildECSTaskRole } from './iam-roles.ts';
import { buildBackingServices } from './backing-services.ts';
import { buildECSMigrationResources, buildAppRunnerService } from './compute.ts';

// Initialize Sentry
initSentry();

Deno.serve(async (req) => {
  try {
    if (req.method !== 'POST') {
      return new Response(JSON.stringify({ error: 'Method not allowed' }), {
        status: 405,
        headers: { 'Content-Type': 'application/json' },
      });
    }

    const supabaseClient = createClient(
      Deno.env.get('SUPABASE_URL') ?? '',
      Deno.env.get('SUPABASE_ANON_KEY') ?? '',
      {
        global: {
          headers: { Authorization: req.headers.get('Authorization')! },
        },
      },
    );

    const authHeader = req.headers.get('Authorization')!;
    const token = authHeader.replace('Bearer ', '');
    const { data } = await supabaseClient.auth.getUser(token);

    if (!data.user) {
      return new Response("Unauthorized", { status: 401 });
    }

    const deploymentSpec: DeploymentSpec = await req.json();

    console.log('Received deployment spec:', {
      serviceName: deploymentSpec.serviceName,
      imageUrl: deploymentSpec.imageUrl,
      backingServicesCount: deploymentSpec.backingServices?.length || 0,
      backingServices: deploymentSpec.backingServices,
    });

    // Validate required fields
    if (!deploymentSpec.serviceName || !deploymentSpec.imageUrl) {
      return new Response("Missing required fields: serviceName, imageUrl", { status: 400 });
    }

    // Get customer AWS credentials from database
    const { data: awsCredentials, error: credError } = await supabaseClient
      .from('aws_credentials')
      .select('role_arn, region, external_id')
      .eq('user_id', data.user.id)
      .single();

    if (credError || !awsCredentials) {
      return new Response("AWS credentials not found. Please authenticate with AWS first.", { status: 404 });
    }

    if (!awsCredentials.role_arn) {
      return new Response("AWS role ARN not configured. Please complete AWS authentication.", { status: 400 });
    }

    const awsRegion = awsCredentials.region || 'us-east-1';

    // Assume customer's AWS role
    const stsClient = new STSClient({
      region: awsRegion,
      credentials: {
        accessKeyId: Deno.env.get('AWS_ACCESS_KEY_ID') || '',
        secretAccessKey: Deno.env.get('AWS_SECRET_ACCESS_KEY') || '',
      },
    });

    const assumeRoleParams: any = {
      RoleArn: awsCredentials.role_arn,
      RoleSessionName: `deploy-${data.user.id}`,
      DurationSeconds: 3600,
    };

    if (awsCredentials.external_id) {
      assumeRoleParams.ExternalId = awsCredentials.external_id;
    }

    const assumeRoleCommand = new AssumeRoleCommand(assumeRoleParams);
    const { Credentials } = await stsClient.send(assumeRoleCommand);

    if (!Credentials) {
      return new Response('Failed to assume AWS role', { status: 500 });
    }

    // Create CloudFormation client with assumed credentials
    const cfnClient = new CloudFormationClient({
      region: awsRegion,
      credentials: {
        accessKeyId: Credentials.AccessKeyId!,
        secretAccessKey: Credentials.SecretAccessKey!,
        sessionToken: Credentials.SessionToken,
      },
    });

    // Generate CloudFormation template
    const template = generateCloudFormationTemplate(deploymentSpec, data.user.id);
    const stackName = `prod-${deploymentSpec.serviceName}`;

    // Check if stack exists
    let stackExists = false;
    try {
      const describeResult = await cfnClient.send(
        new DescribeStacksCommand({ StackName: stackName })
      );
      stackExists = describeResult.Stacks && describeResult.Stacks.length > 0;
    } catch (error) {
      // Stack doesn't exist, which is fine
      stackExists = false;
    }

    // Create or update stack
    let stackId: string;
    let operation: 'create' | 'update';
    
    if (stackExists) {
      const updateResult = await cfnClient.send(
        new UpdateStackCommand({
          StackName: stackName,
          TemplateBody: template,
          Capabilities: ['CAPABILITY_IAM', 'CAPABILITY_NAMED_IAM'],
          Tags: [
            { Key: 'tenant', Value: data.user.id },
            { Key: 'service', Value: deploymentSpec.serviceName },
          ],
        })
      );
      stackId = updateResult.StackId || stackName;
      operation = 'update';
    } else {
      const createResult = await cfnClient.send(
        new CreateStackCommand({
          StackName: stackName,
          TemplateBody: template,
          Capabilities: ['CAPABILITY_IAM', 'CAPABILITY_NAMED_IAM'],
          Tags: [
            { Key: 'tenant', Value: data.user.id },
            { Key: 'service', Value: deploymentSpec.serviceName },
          ],
        })
      );
      stackId = createResult.StackId || stackName;
      operation = 'create';
    }

    // Return immediately without polling - CLI will poll separately
    return Response.json({
      stackId: stackId,
      stackName: stackName,
      status: operation === 'create' ? 'CREATE_IN_PROGRESS' : 'UPDATE_IN_PROGRESS',
      operation: operation,
    });

  } catch (error) {
    console.error('Unexpected error in deploy-aws-stack function:', error);
    captureException(error instanceof Error ? error : new Error(String(error)), {
      function: 'deploy-aws-stack',
      operation: 'general_error',
      method: req.method
    });
    await flushSentry();

    return new Response(
      JSON.stringify({ error: error instanceof Error ? error.message : 'Internal server error' }),
      { status: 500, headers: { 'Content-Type': 'application/json' } }
    );
  }
});

function generateCloudFormationTemplate(spec: DeploymentSpec, tenantId: string): string {
  const resources: any = {};

  // Create VPC and networking if:
  // 1. Backing services (databases, caches) are needed
  // 2. OR migrations need to run (ECS tasks require VPC)
  const hasMigrations = spec.migrationCommand && spec.migrationCommand.trim() !== '';
  const hasBackingServices = spec.backingServices && spec.backingServices.length > 0;
  const needsVpc = hasBackingServices || hasMigrations;
  
  console.log('Generating CloudFormation template:', {
    backingServicesCount: spec.backingServices?.length || 0,
    hasMigrations: hasMigrations,
    needsVpc: needsVpc,
  });

  if (needsVpc) {
    // Build VPC networking resources (VPC, subnets, security groups, route tables)
    const hasRds = spec.backingServices?.some(s => s.type === 'rds');
    buildNetworkingResources(spec.serviceName, tenantId, hasMigrations, hasRds || false, resources);
  }

  // Build backing services (RDS, ElastiCache)
  buildBackingServices(spec, tenantId, resources);

  // Build IAM roles for App Runner
  buildAppRunnerAccessRole(spec.serviceName, tenantId, resources);
  const hasSensitiveVars = spec.envVars.some(ev => ev.sensitive);
  buildAppRunnerInstanceRole(spec.serviceName, tenantId, hasBackingServices, hasSensitiveVars, resources);

  // Build Secrets Manager resources for sensitive env vars (including Lambda-backed custom resources)
  // This must be called early so secrets exist for both migrations and App Runner
  buildSecretsManagerResources(spec, resources);

  // Build ECS resources for running migrations (if migration command exists)
  if (hasMigrations) {
    buildECSTaskExecutionRole(spec.serviceName, tenantId, resources);
    buildECSTaskRole(spec.serviceName, tenantId, resources);
    buildECSMigrationResources(spec, tenantId, resources);
  }

  // Build App Runner Service (only if createAppRunner is not false)
  buildAppRunnerService(spec, tenantId, needsVpc, hasMigrations, resources);

  // Outputs
  const outputs: any = {
    AppRunnerAccessRoleArn: {
      Description: 'App Runner Access Role ARN',
      Value: { 'Fn::GetAtt': ['AppRunnerAccessRole', 'Arn'] },
    },
    AppRunnerInstanceRoleArn: {
      Description: 'App Runner Instance Role ARN',
      Value: { 'Fn::GetAtt': ['AppRunnerInstanceRole', 'Arn'] },
    },
  };
  
  // Add App Runner service outputs only if the service was created
  const shouldCreateAppRunner = spec.createAppRunner !== false;
  if (shouldCreateAppRunner) {
    outputs.AppRunnerServiceArn = {
      Description: 'App Runner Service ARN',
      Value: { 'Fn::GetAtt': ['AppRunnerService', 'ServiceArn'] },
    };
    outputs.AppRunnerServiceUrl = {
      Description: 'App Runner Service URL',
      Value: { 'Fn::GetAtt': ['AppRunnerService', 'ServiceUrl'] },
    };
    outputs.AppRunnerServiceId = {
      Description: 'App Runner Service ID',
      Value: { 'Fn::GetAtt': ['AppRunnerService', 'ServiceId'] },
    };
  }
  
  // Add VPC outputs if VPC was created
  if (needsVpc) {
    outputs.VPCId = {
      Description: 'VPC ID',
      Value: { Ref: 'VPC' },
    };
    outputs.PrivateSubnetAZ1 = {
      Description: 'Private Subnet AZ1 ID',
      Value: { Ref: 'PrivateSubnetAZ1' },
    };
    outputs.PrivateSubnetAZ2 = {
      Description: 'Private Subnet AZ2 ID',
      Value: { Ref: 'PrivateSubnetAZ2' },
    };
    
    // Only output public subnets if migrations are present
    if (hasMigrations) {
      outputs.PublicSubnetAZ1 = {
        Description: 'Public Subnet AZ1 ID',
        Value: { Ref: 'PublicSubnetAZ1' },
      };
      outputs.PublicSubnetAZ2 = {
        Description: 'Public Subnet AZ2 ID',
        Value: { Ref: 'PublicSubnetAZ2' },
      };
    }
    
    outputs.AppRunnerSecurityGroupId = {
      Description: 'Security Group ID for App Runner',
      Value: { Ref: 'AppRunnerSecurityGroup' },
    };
  }
  
  // Add ECS outputs if migrations are present
  if (hasMigrations) {
    outputs.ECSClusterArn = {
      Description: 'ECS Cluster ARN for running migrations',
      Value: { 'Fn::GetAtt': ['ECSCluster', 'Arn'] },
    };
    outputs.MigrationTaskDefinitionArn = {
      Description: 'ECS Task Definition ARN for migrations',
      Value: { Ref: 'MigrationTaskDefinition' },
    };
  }

  // Add database connection strings to outputs
  if (spec.backingServices) {
    for (const service of spec.backingServices) {
      if (service.type === 'rds') {
        const dbName = service.name.replace(/[^a-zA-Z0-9]/g, '');
        outputs[`${dbName}Endpoint`] = {
          Description: `${service.name} endpoint`,
          Value: { 'Fn::GetAtt': [dbName, 'Endpoint.Address'] },
        };
        outputs[`${dbName}Port`] = {
          Description: `${service.name} port`,
          Value: { 'Fn::GetAtt': [dbName, 'Endpoint.Port'] },
        };
      } else if (service.type === 'elasticache') {
        const cacheName = service.name.replace(/[^a-zA-Z0-9]/g, '');
        outputs[`${cacheName}Endpoint`] = {
          Description: `${service.name} endpoint`,
          Value: { 'Fn::GetAtt': [cacheName, 'RedisEndpoint.Address'] },
        };
        outputs[`${cacheName}Port`] = {
          Description: `${service.name} port`,
          Value: { 'Fn::GetAtt': [cacheName, 'RedisEndpoint.Port'] },
        };
      }
    }
  }

  // Add CloudFormation Parameters for image URL
  // This allows updating the image without recreating resources
  const parameters: any = {
    ImageUrl: {
      Type: 'String',
      Description: 'Container image URL from ECR',
      Default: spec.imageUrl,
    },
  };

  const template = {
    AWSTemplateFormatVersion: '2010-09-09',
    Description: `Prod deployment for ${spec.serviceName}`,
    Parameters: parameters,
    Resources: resources,
    Outputs: outputs,
  };

  return JSON.stringify(template, null, 2);
}
