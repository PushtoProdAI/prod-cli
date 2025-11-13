import 'jsr:@supabase/functions-js/edge-runtime.d.ts';
import { createClient } from 'jsr:@supabase/supabase-js@2';
import { initSentry, captureException, flushSentry } from '../_shared/sentry.ts';

// Import types and helper functions from deploy-aws-stack
import type { DeploymentSpec } from '../deploy-aws-stack/types.ts';
import { buildEnvironmentVariables, buildEnvironmentSecrets } from '../deploy-aws-stack/env-builders.ts';
import { buildSecretsManagerResources } from '../deploy-aws-stack/secrets-manager.ts';
import { buildNetworkingResources } from '../deploy-aws-stack/networking.ts';
import { buildAppRunnerAccessRole, buildAppRunnerInstanceRole, buildECSTaskExecutionRole, buildECSTaskRole } from '../deploy-aws-stack/iam-roles.ts';
import { buildBackingServices } from '../deploy-aws-stack/backing-services.ts';
import { buildECSMigrationResources, buildAppRunnerService } from '../deploy-aws-stack/compute.ts';

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

    console.log('Received template preview request:', {
      serviceName: deploymentSpec.serviceName,
      backingServicesCount: deploymentSpec.backingServices?.length || 0,
      envVarsCount: deploymentSpec.envVars?.length || 0,
    });

    // Validate required fields
    if (!deploymentSpec.serviceName) {
      return new Response("Missing required field: serviceName", { status: 400 });
    }

    // Generate CloudFormation template
    // Use a placeholder image URL since we're just generating the template for pricing
    const previewSpec = {
      ...deploymentSpec,
      imageUrl: deploymentSpec.imageUrl || 'placeholder.dkr.ecr.us-east-1.amazonaws.com/app:latest',
    };

    const template = generateCloudFormationTemplate(previewSpec, data.user.id);

    // Return the template
    return Response.json({
      template: template,
      serviceName: deploymentSpec.serviceName,
    });

  } catch (error) {
    console.error('Error in preview-aws-template function:', error);
    captureException(error instanceof Error ? error : new Error(String(error)), {
      function: 'preview-aws-template',
      operation: 'generate_template',
      method: req.method
    });
    await flushSentry();

    return new Response(
      JSON.stringify({ error: error instanceof Error ? error.message : 'Internal server error' }),
      { status: 500, headers: { 'Content-Type': 'application/json' } }
    );
  }
});

// This is the same function from deploy-aws-stack/index.ts
// We reuse it here for template generation
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
  // For pricing estimation, assume all env vars might be sensitive (worst case)
  const hasSensitiveVars = spec.envVars && spec.envVars.length > 0;
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
  const shouldCreateAppRunner = spec.createAppRunner === undefined || spec.createAppRunner === true;
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
