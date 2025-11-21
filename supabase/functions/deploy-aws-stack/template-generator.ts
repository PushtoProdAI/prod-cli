import type { DeploymentSpec } from './types.ts';
import { buildNetworkingResources } from './networking.ts';
import { buildBackingServices } from './backing-services.ts';
import { buildAppRunnerAccessRole, buildAppRunnerInstanceRole, buildECSTaskExecutionRole, buildECSTaskRole } from './iam-roles.ts';
// SECURITY: Use S3-hosted Lambda packages instead of inline code to prevent code injection
import { buildSecretsManagerResources } from './secrets-manager-s3.ts';
import { buildECSMigrationResources, buildAppRunnerService } from './compute.ts';

/**
 * Validate deployment spec to prevent template injection attacks
 * All user-provided data must pass strict validation before being used in CloudFormation
 */
function validateDeploymentSpec(spec: DeploymentSpec, tenantId: string): void {
  // Validate and sanitize service name (used in resource names, IAM roles, etc.)
  if (!spec.serviceName || typeof spec.serviceName !== 'string') {
    throw new Error('serviceName is required');
  }
  
  // Sanitize: convert underscores to hyphens, lowercase, remove invalid chars
  const originalName = spec.serviceName;
  spec.serviceName = spec.serviceName
    .toLowerCase()
    .replace(/_/g, '-')  // Convert underscores to hyphens
    .replace(/[^a-z0-9-]/g, '');  // Remove any other invalid characters
  
  // Ensure it starts with a letter
  if (!/^[a-z]/.test(spec.serviceName)) {
    throw new Error(
      `Invalid serviceName: "${originalName}". Must start with a letter (after sanitization: "${spec.serviceName}").`
    );
  }
  
  // Validate final format
  if (!/^[a-z][a-z0-9-]{0,62}$/.test(spec.serviceName)) {
    throw new Error(
      `Invalid serviceName: "${originalName}" (sanitized to: "${spec.serviceName}"). Must be 1-63 characters, lowercase alphanumeric with hyphens, starting with a letter.`
    );
  }
  
  if (originalName !== spec.serviceName) {
    console.log(`Service name sanitized: "${originalName}" → "${spec.serviceName}"`);
  }

  // Validate image URL (must be from ECR)
  if (!spec.imageUrl || typeof spec.imageUrl !== 'string') {
    throw new Error('imageUrl is required');
  }
  // Allow placeholder URLs for pricing estimation (without account ID)
  // Real URLs have 12-digit account ID, placeholder has literal "placeholder"
  const ecrPattern = /^(\d+|placeholder)\.dkr\.ecr\.[a-z0-9-]+\.amazonaws\.com\/[a-z0-9-_/]+:[a-z0-9-_.]+$/i;
  if (!ecrPattern.test(spec.imageUrl)) {
    throw new Error(
      `Invalid imageUrl: "${spec.imageUrl}". Must be a valid ECR repository URL (e.g., 123456789012.dkr.ecr.us-east-1.amazonaws.com/repo:tag)`
    );
  }

  // Validate CPU and memory values
  // Normalize CPU values (handle both "1024" and "1 vCPU" formats)
  const cpuMap: Record<string, string> = {
    '256': '256',
    '0.25 vCPU': '256',
    '512': '512',
    '0.5 vCPU': '512',
    '1024': '1024',
    '1 vCPU': '1024',
    '2048': '2048',
    '2 vCPU': '2048',
    '4096': '4096',
    '4 vCPU': '4096',
  };
  
  // Normalize memory values (handle both "2048" and "2 GB" formats)
  const memoryMap: Record<string, string> = {
    '512': '512',
    '0.5 GB': '512',
    '1024': '1024',
    '1 GB': '1024',
    '2048': '2048',
    '2 GB': '2048',
    '3072': '3072',
    '3 GB': '3072',
    '4096': '4096',
    '4 GB': '4096',
    '5120': '5120',
    '5 GB': '5120',
    '6144': '6144',
    '6 GB': '6144',
    '7168': '7168',
    '7 GB': '7168',
    '8192': '8192',
    '8 GB': '8192',
  };
  
  const normalizedCpu = cpuMap[spec.cpu];
  const normalizedMemory = memoryMap[spec.memory];
  
  if (!normalizedCpu) {
    throw new Error(
      `Invalid CPU value: "${spec.cpu}". Must be one of: 256, 512, 1024, 2048, 4096 (or 0.25 vCPU, 0.5 vCPU, 1 vCPU, 2 vCPU, 4 vCPU)`
    );
  }
  if (!normalizedMemory) {
    throw new Error(
      `Invalid memory value: "${spec.memory}". Must be one of: 512, 1024, 2048, 3072, 4096, 5120, 6144, 7168, 8192 (or 0.5 GB, 1 GB, 2 GB, etc.)`
    );
  }
  
  // Update spec with normalized values for template generation
  spec.cpu = normalizedCpu;
  spec.memory = normalizedMemory;

  // Validate port
  if (typeof spec.port !== 'number' || spec.port < 1 || spec.port > 65535) {
    throw new Error(`Invalid port: ${spec.port}. Must be a number between 1 and 65535`);
  }

  // Validate tenant ID (UUID format)
  const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
  if (!tenantId || !uuidPattern.test(tenantId)) {
    throw new Error(`Invalid tenantId: "${tenantId}". Must be a valid UUID`);
  }

  // Validate environment variables
  if (spec.envVars && Array.isArray(spec.envVars)) {
    for (const envVar of spec.envVars) {
      if (!envVar.name || typeof envVar.name !== 'string') {
        throw new Error('Environment variable name is required');
      }
      // Environment variable names must be uppercase with underscores
      if (!/^[A-Z_][A-Z0-9_]{0,254}$/.test(envVar.name)) {
        throw new Error(
          `Invalid environment variable name: "${envVar.name}". Must be uppercase letters, numbers, and underscores only.`
        );
      }
      // Validate value if present
      if (envVar.value !== undefined && envVar.value !== null && typeof envVar.value !== 'string') {
        throw new Error(`Environment variable value must be a string: ${envVar.name}`);
      }
    }
  }

  // Validate backing services
  if (spec.backingServices && Array.isArray(spec.backingServices)) {
    for (const service of spec.backingServices) {
      if (!service.name || typeof service.name !== 'string') {
        throw new Error('Backing service name is required');
      }
      if (!/^[a-z][a-z0-9-]{0,62}$/.test(service.name)) {
        throw new Error(
          `Invalid backing service name: "${service.name}". Must be lowercase alphanumeric with hyphens.`
        );
      }
      if (service.type !== 'rds' && service.type !== 'serverless-cache') {
        throw new Error(`Invalid backing service type: "${service.type}". Must be "rds" or "serverless-cache"`);
      }
    }
  }

  // Validate migration command if present
  if (spec.migrationCommand) {
    if (typeof spec.migrationCommand !== 'string') {
      throw new Error('Migration command must be a string');
    }
    // Don't allow shell metacharacters that could enable command injection
    const dangerousChars = /[;&|`$(){}[\]<>\\]/;
    if (dangerousChars.test(spec.migrationCommand)) {
      throw new Error(
        'Migration command contains potentially dangerous characters. Please use simple commands only.'
      );
    }
    // Limit length to prevent DoS
    if (spec.migrationCommand.length > 500) {
      throw new Error('Migration command is too long (max 500 characters)');
    }
  }

  console.log('✓ Deployment spec validation passed');
}

export function generateCloudFormationTemplate(spec: DeploymentSpec, tenantId: string): string {
  // SECURITY: Validate all user inputs before using them in CloudFormation template
  validateDeploymentSpec(spec, tenantId);
  
  const resources: any = {};

  // Create VPC and networking if:
  // 1. Backing services (databases, caches) are needed
  // 2. OR migrations need to run (ECS tasks require VPC)
  const hasMigrations = spec.migrationCommand && spec.migrationCommand.trim() !== '';
  const hasBackingServices = spec.backingServices && spec.backingServices.length > 0;
  const needsVpc = hasBackingServices || hasMigrations;
  
  console.log('Generating CloudFormation template:', {
    serviceName: spec.serviceName,
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
  buildSecretsManagerResources(spec, tenantId, resources);

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
      } else if (service.type === 'serverless-cache') {
        const cacheName = service.name.replace(/[^a-zA-Z0-9]/g, '');
        outputs[`${cacheName}Endpoint`] = {
          Description: `${service.name} endpoint`,
          Value: { 'Fn::GetAtt': [cacheName, 'Endpoint.Address'] },
        };
        outputs[`${cacheName}Port`] = {
          Description: `${service.name} port`,
          Value: { 'Fn::GetAtt': [cacheName, 'Endpoint.Port'] },
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
