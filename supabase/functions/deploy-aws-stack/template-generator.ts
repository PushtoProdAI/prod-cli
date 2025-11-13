import type { DeploymentSpec } from './types.ts';
import { buildNetworkingResources } from './networking.ts';
import { buildBackingServices } from './backing-services.ts';
import { buildAppRunnerAccessRole, buildAppRunnerInstanceRole, buildECSTaskExecutionRole, buildECSTaskRole } from './iam-roles.ts';
import { buildSecretsManagerResources } from './secrets-manager.ts';
import { buildECSMigrationResources, buildAppRunnerService } from './compute.ts';

export function generateCloudFormationTemplate(spec: DeploymentSpec, tenantId: string): string {
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
  // For consistent pricing between preview and deploy, assume all env vars might be sensitive (worst case)
  // This ensures preview cost estimates match actual deployment costs
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
