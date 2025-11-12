// Compute resources (ECS, App Runner) for AWS deployments

import type { DeploymentSpec } from './types.ts';
import { buildEnvironmentVariables, buildEnvironmentSecrets } from './env-builders.ts';

/**
 * Build ECS Cluster and Task Definition for running migrations
 */
export function buildECSMigrationResources(
  spec: DeploymentSpec,
  tenantId: string,
  resources: any
): void {
  console.log('Creating ECS resources for migration execution');
  
  // ECS Cluster for running migrations
  resources.ECSCluster = {
    Type: 'AWS::ECS::Cluster',
    Properties: {
      ClusterName: `prod-${spec.serviceName}-cluster`,
      Tags: [
        { Key: 'tenant', Value: tenantId },
        { Key: 'service', Value: spec.serviceName },
      ],
    },
  };

  // Build dependencies for custom resources that create DATABASE_URL secrets
  const migrationDependsOn: string[] = [];
  const fullUriVarsForMigration = spec.envVars.filter(
    ev => ev.role === 'full_uri' && !ev.value && ev.service === 'postgresql' && ev.sensitive
  );
  for (const envVar of fullUriVarsForMigration) {
    const sanitizedName = envVar.name.replace(/[^a-zA-Z0-9]/g, '');
    migrationDependsOn.push(`CustomResource${sanitizedName}`);
  }

  // ECS Task Definition for migrations
  resources.MigrationTaskDefinition = {
    Type: 'AWS::ECS::TaskDefinition',
    DependsOn: migrationDependsOn.length > 0 ? migrationDependsOn : undefined,
    Properties: {
      Family: `prod-${spec.serviceName}-migration`,
      NetworkMode: 'awsvpc',
      RequiresCompatibilities: ['FARGATE'],
      Cpu: '256',
      Memory: '512',
      ExecutionRoleArn: { 'Fn::GetAtt': ['ECSTaskExecutionRole', 'Arn'] },
      TaskRoleArn: { 'Fn::GetAtt': ['ECSTaskRole', 'Arn'] },
      ContainerDefinitions: [
        {
          Name: 'migration',
          Image: spec.imageUrl,
          Essential: true,
          Environment: buildEnvironmentVariables(spec, resources),
          Secrets: buildEnvironmentSecrets(spec, resources),
          LogConfiguration: {
            LogDriver: 'awslogs',
            Options: {
              'awslogs-group': `/ecs/prod-${spec.serviceName}`,
              'awslogs-region': { Ref: 'AWS::Region' },
              'awslogs-stream-prefix': 'migration',
              'awslogs-create-group': 'true',
            },
          },
        },
      ],
      Tags: [
        { Key: 'tenant', Value: tenantId },
        { Key: 'service', Value: spec.serviceName },
      ],
    },
  };
}

/**
 * Build App Runner Service with VPC Connector
 * Only creates App Runner if spec.createAppRunner is not false
 */
export function buildAppRunnerService(
  spec: DeploymentSpec,
  tenantId: string,
  needsVpc: boolean,
  hasMigrations: boolean,
  resources: any
): void {
  const shouldCreateAppRunner = spec.createAppRunner !== false; // Default to true for backward compatibility
  
  if (!shouldCreateAppRunner) {
    console.log('Skipping App Runner Service creation - will be added after migration');
    return;
  }

  console.log('Creating App Runner Service and VPC Connector in CloudFormation');
  
  // VPC Connector for App Runner (if VPC exists)
  if (needsVpc) {
    resources.AppRunnerVpcConnector = {
      Type: 'AWS::AppRunner::VpcConnector',
      Properties: {
        VpcConnectorName: `${spec.serviceName}-connector`,
        Subnets: [
          { Ref: 'PrivateSubnetAZ1' },
          { Ref: 'PrivateSubnetAZ2' },
        ],
        SecurityGroups: [
          { Ref: 'AppRunnerSecurityGroup' },
        ],
        Tags: [
          { Key: 'tenant', Value: tenantId },
          { Key: 'service', Value: spec.serviceName },
        ],
      },
    };
  }
  
  // Build dependency list - wait for IAM roles and VPC resources
  const appRunnerDependsOn: string[] = [
    'AppRunnerAccessRole',
    'AppRunnerInstanceRole',
  ];
  
  if (needsVpc) {
    appRunnerDependsOn.push('VPC', 'PrivateSubnetAZ1', 'PrivateSubnetAZ2', 'AppRunnerSecurityGroup', 'AppRunnerVpcConnector');
  }
  
  // If migrations exist, ensure migration task definition exists
  if (hasMigrations) {
    appRunnerDependsOn.push('MigrationTaskDefinition');
  }

  // Add dependencies on custom resources that create DATABASE_URL secrets
  const fullUriVars = spec.envVars.filter(
    ev => ev.role === 'full_uri' && !ev.value && ev.service === 'postgresql' && ev.sensitive
  );
  for (const envVar of fullUriVars) {
    const sanitizedName = envVar.name.replace(/[^a-zA-Z0-9]/g, '');
    appRunnerDependsOn.push(`CustomResource${sanitizedName}`);
  }
  
  // Build environment variables array for App Runner
  const runtimeEnvVars = buildEnvironmentVariables(spec, resources);
  const runtimeSecrets = buildEnvironmentSecrets(spec, resources);
  
  // Transform secrets for App Runner format (uses "Value" instead of "ValueFrom")
  const appRunnerSecrets = runtimeSecrets.map(secret => ({
    Name: secret.Name,
    Value: secret.ValueFrom,  // App Runner uses "Value" key instead of "ValueFrom"
  }));
  
  // Build ImageConfiguration with both env vars and secrets
  const imageConfig: any = {
    Port: String(spec.port),
    RuntimeEnvironmentVariables: runtimeEnvVars,
  };
  
  // Only add RuntimeEnvironmentSecrets if there are any
  if (appRunnerSecrets.length > 0) {
    imageConfig.RuntimeEnvironmentSecrets = appRunnerSecrets;
    console.log(`App Runner will use ${appRunnerSecrets.length} secrets from Secrets Manager`);
  }

  resources.AppRunnerService = {
    Type: 'AWS::AppRunner::Service',
    DependsOn: appRunnerDependsOn,
    Properties: {
      ServiceName: spec.serviceName,
      SourceConfiguration: {
        AuthenticationConfiguration: {
          AccessRoleArn: { 'Fn::GetAtt': ['AppRunnerAccessRole', 'Arn'] },
        },
        ImageRepository: {
          ImageIdentifier: { Ref: 'ImageUrl' },
          ImageRepositoryType: 'ECR',
          ImageConfiguration: imageConfig,
        },
      },
      InstanceConfiguration: {
        Cpu: spec.cpu,
        Memory: spec.memory,
        InstanceRoleArn: { 'Fn::GetAtt': ['AppRunnerInstanceRole', 'Arn'] },
      },
      NetworkConfiguration: needsVpc ? {
        EgressConfiguration: {
          EgressType: 'VPC',
          VpcConnectorArn: { 'Fn::GetAtt': ['AppRunnerVpcConnector', 'VpcConnectorArn'] },
        },
      } : undefined,
      Tags: [
        { Key: 'tenant', Value: tenantId },
        { Key: 'service', Value: spec.serviceName },
      ],
    },
  };
}
