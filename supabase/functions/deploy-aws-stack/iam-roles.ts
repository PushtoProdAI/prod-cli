// IAM roles for AWS deployments

import type { DeploymentSpec } from './types.ts';

/**
 * Build App Runner Access Role for pulling images from ECR
 */
export function buildAppRunnerAccessRole(
  serviceName: string,
  tenantId: string,
  resources: any
): void {
  resources.AppRunnerAccessRole = {
    Type: 'AWS::IAM::Role',
    Properties: {
      RoleName: `prod-${serviceName}-apprunner-access`,
      AssumeRolePolicyDocument: {
        Version: '2012-10-17',
        Statement: [
          {
            Effect: 'Allow',
            Principal: { Service: 'build.apprunner.amazonaws.com' },
            Action: 'sts:AssumeRole',
          },
        ],
      },
      Policies: [
        {
          PolicyName: 'AppRunnerECRAccess',
          PolicyDocument: {
            Version: '2012-10-17',
            Statement: [
              {
                Effect: 'Allow',
                Action: [
                  'ecr:GetAuthorizationToken',
                ],
                Resource: '*',
              },
              {
                Effect: 'Allow',
                Action: [
                  'ecr:BatchCheckLayerAvailability',
                  'ecr:GetDownloadUrlForLayer',
                  'ecr:BatchGetImage',
                  'ecr:DescribeImages',
                ],
                Resource: '*',
              },
            ],
          },
        },
      ],
      Tags: [{ Key: 'tenant', Value: tenantId }],
    },
  };
}

/**
 * Build App Runner Instance Role for container runtime
 * Conditionally adds Secrets Manager access if needed
 */
export function buildAppRunnerInstanceRole(
  serviceName: string,
  tenantId: string,
  hasBackingServices: boolean,
  hasSensitiveVars: boolean,
  resources: any
): void {
  const instanceRolePolicies: any[] = [];
  
  // Add Secrets Manager access if we have backing services or sensitive env vars
  if (hasBackingServices || hasSensitiveVars) {
    instanceRolePolicies.push({
      PolicyName: 'SecretsManagerAccess',
      PolicyDocument: {
        Version: '2012-10-17',
        Statement: [
          {
            Effect: 'Allow',
            Action: [
              'secretsmanager:GetSecretValue',
              'secretsmanager:DescribeSecret',
            ],
            Resource: `arn:aws:secretsmanager:*:*:secret:/prod/${serviceName}/*`,
          },
        ],
      },
    });
    console.log('Added Secrets Manager permissions to Instance Role');
  }

  resources.AppRunnerInstanceRole = {
    Type: 'AWS::IAM::Role',
    Properties: {
      RoleName: `prod-${serviceName}-apprunner-instance`,
      AssumeRolePolicyDocument: {
        Version: '2012-10-17',
        Statement: [
          {
            Effect: 'Allow',
            Principal: { Service: 'tasks.apprunner.amazonaws.com' },
            Action: 'sts:AssumeRole',
          },
        ],
      },
      Policies: instanceRolePolicies.length > 0 ? instanceRolePolicies : undefined,
      Tags: [{ Key: 'tenant', Value: tenantId }],
    },
  };
}

/**
 * Build ECS Task Execution Role for pulling images and logging
 */
export function buildECSTaskExecutionRole(
  serviceName: string,
  tenantId: string,
  resources: any
): void {
  resources.ECSTaskExecutionRole = {
    Type: 'AWS::IAM::Role',
    Properties: {
      RoleName: `prod-${serviceName}-ecs-execution`,
      AssumeRolePolicyDocument: {
        Version: '2012-10-17',
        Statement: [
          {
            Effect: 'Allow',
            Principal: { Service: 'ecs-tasks.amazonaws.com' },
            Action: 'sts:AssumeRole',
          },
        ],
      },
      ManagedPolicyArns: [
        'arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy',
      ],
      Policies: [
        {
          PolicyName: 'SecretsManagerAccess',
          PolicyDocument: {
            Version: '2012-10-17',
            Statement: [
              {
                Effect: 'Allow',
                Action: [
                  'secretsmanager:GetSecretValue',
                  'secretsmanager:DescribeSecret',
                ],
                Resource: [
                  `arn:aws:secretsmanager:*:*:secret:prod-${serviceName}-*`,
                  `arn:aws:secretsmanager:*:*:secret:/prod/${serviceName}/*`,
                ],
              },
            ],
          },
        },
        {
          PolicyName: 'CloudWatchLogsAccess',
          PolicyDocument: {
            Version: '2012-10-17',
            Statement: [
              {
                Effect: 'Allow',
                Action: [
                  'logs:CreateLogGroup',
                  'logs:CreateLogStream',
                  'logs:PutLogEvents',
                ],
                Resource: `arn:aws:logs:*:*:log-group:/ecs/prod-${serviceName}*`,
              },
            ],
          },
        },
      ],
      Tags: [{ Key: 'tenant', Value: tenantId }],
    },
  };
}

/**
 * Build ECS Task Role for application permissions (access to secrets, etc)
 */
export function buildECSTaskRole(
  serviceName: string,
  tenantId: string,
  resources: any
): void {
  resources.ECSTaskRole = {
    Type: 'AWS::IAM::Role',
    Properties: {
      RoleName: `prod-${serviceName}-ecs-task`,
      AssumeRolePolicyDocument: {
        Version: '2012-10-17',
        Statement: [
          {
            Effect: 'Allow',
            Principal: { Service: 'ecs-tasks.amazonaws.com' },
            Action: 'sts:AssumeRole',
          },
        ],
      },
      Policies: [
        {
          PolicyName: 'SecretsManagerAccess',
          PolicyDocument: {
            Version: '2012-10-17',
            Statement: [
              {
                Effect: 'Allow',
                Action: [
                  'secretsmanager:GetSecretValue',
                  'secretsmanager:DescribeSecret',
                ],
                Resource: [
                  `arn:aws:secretsmanager:*:*:secret:prod-${serviceName}-*`,
                  `arn:aws:secretsmanager:*:*:secret:/prod/${serviceName}/*`,
                ],
              },
            ],
          },
        },
      ],
      Tags: [{ Key: 'tenant', Value: tenantId }],
    },
  };
}
