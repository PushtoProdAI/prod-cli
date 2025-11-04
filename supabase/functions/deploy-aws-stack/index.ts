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

// Initialize Sentry
initSentry();

interface DeploymentSpec {
  serviceName: string;
  imageUrl: string;
  cpu: string;
  memory: string;
  port: number;
  envVars: Record<string, string>;
  backingServices?: BackingService[];
}

interface BackingService {
  type: 'rds' | 'elasticache';
  name: string;
  engine?: string;
  instanceClass?: string;
  allocatedStorage?: number;
  nodeType?: string;
  numCacheNodes?: number;
}

interface DeploymentResult {
  stackId: string;
  stackName: string;
  status: string;
  outputs?: Record<string, string>;
  error?: string;
}

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

  // Create VPC and networking if backing services are needed
  const needsVpc = spec.backingServices && spec.backingServices.length > 0;

  if (needsVpc) {
    // VPC
    resources.VPC = {
      Type: 'AWS::EC2::VPC',
      Properties: {
        CidrBlock: '10.0.0.0/16',
        EnableDnsHostnames: true,
        EnableDnsSupport: true,
        Tags: [
          { Key: 'Name', Value: `prod-${spec.serviceName}-vpc` },
          { Key: 'tenant', Value: tenantId },
        ],
      },
    };

    // Subnets
    resources.SubnetA = {
      Type: 'AWS::EC2::Subnet',
      Properties: {
        VpcId: { Ref: 'VPC' },
        CidrBlock: '10.0.1.0/24',
        AvailabilityZone: { 'Fn::Select': [0, { 'Fn::GetAZs': '' }] },
        Tags: [{ Key: 'Name', Value: `prod-${spec.serviceName}-subnet-a` }],
      },
    };

    resources.SubnetB = {
      Type: 'AWS::EC2::Subnet',
      Properties: {
        VpcId: { Ref: 'VPC' },
        CidrBlock: '10.0.2.0/24',
        AvailabilityZone: { 'Fn::Select': [1, { 'Fn::GetAZs': '' }] },
        Tags: [{ Key: 'Name', Value: `prod-${spec.serviceName}-subnet-b` }],
      },
    };

    // Security Group for backing services
    resources.BackingServiceSecurityGroup = {
      Type: 'AWS::EC2::SecurityGroup',
      Properties: {
        GroupDescription: 'Security group for backing services',
        VpcId: { Ref: 'VPC' },
        SecurityGroupIngress: [
          {
            IpProtocol: 'tcp',
            FromPort: 5432,
            ToPort: 5432,
            SourceSecurityGroupId: { Ref: 'AppRunnerSecurityGroup' },
          },
          {
            IpProtocol: 'tcp',
            FromPort: 6379,
            ToPort: 6379,
            SourceSecurityGroupId: { Ref: 'AppRunnerSecurityGroup' },
          },
        ],
        Tags: [{ Key: 'Name', Value: `prod-${spec.serviceName}-backing-sg` }],
      },
    };

    // Security Group for App Runner
    resources.AppRunnerSecurityGroup = {
      Type: 'AWS::EC2::SecurityGroup',
      Properties: {
        GroupDescription: 'Security group for App Runner',
        VpcId: { Ref: 'VPC' },
        Tags: [{ Key: 'Name', Value: `prod-${spec.serviceName}-apprunner-sg` }],
      },
    };

    // DB Subnet Group (if RDS is needed)
    const hasRds = spec.backingServices?.some(s => s.type === 'rds');
    if (hasRds) {
      resources.DBSubnetGroup = {
        Type: 'AWS::RDS::DBSubnetGroup',
        Properties: {
          DBSubnetGroupDescription: 'Subnet group for RDS',
          SubnetIds: [{ Ref: 'SubnetA' }, { Ref: 'SubnetB' }],
          Tags: [{ Key: 'Name', Value: `prod-${spec.serviceName}-db-subnet` }],
        },
      };
    }
  }

  // Add backing services
  if (spec.backingServices) {
    for (const service of spec.backingServices) {
      if (service.type === 'rds') {
        const dbName = service.name.replace(/[^a-zA-Z0-9]/g, '');
        
        // Generate random password
        resources[`${dbName}Password`] = {
          Type: 'AWS::SecretsManager::Secret',
          Properties: {
            Description: `Password for ${service.name}`,
            GenerateSecretString: {
              SecretStringTemplate: JSON.stringify({ username: 'postgres' }),
              GenerateStringKey: 'password',
              PasswordLength: 32,
              ExcludeCharacters: '"@/\\',
            },
          },
        };

        resources[dbName] = {
          Type: 'AWS::RDS::DBInstance',
          Properties: {
            DBInstanceIdentifier: `prod-${spec.serviceName}-${service.name}`,
            Engine: service.engine || 'postgres',
            DBInstanceClass: service.instanceClass || 'db.t3.micro',
            AllocatedStorage: service.allocatedStorage || 20,
            MasterUsername: 'postgres',
            MasterUserPassword: {
              'Fn::Sub': `{{resolve:secretsmanager:\${${dbName}Password}::password}}`,
            },
            DBSubnetGroupName: { Ref: 'DBSubnetGroup' },
            VPCSecurityGroups: [{ Ref: 'BackingServiceSecurityGroup' }],
            PubliclyAccessible: false,
            Tags: [
              { Key: 'tenant', Value: tenantId },
              { Key: 'service', Value: spec.serviceName },
            ],
          },
        };
      } else if (service.type === 'elasticache') {
        const cacheName = service.name.replace(/[^a-zA-Z0-9]/g, '');
        
        resources[`${cacheName}SubnetGroup`] = {
          Type: 'AWS::ElastiCache::SubnetGroup',
          Properties: {
            Description: 'Subnet group for ElastiCache',
            SubnetIds: [{ Ref: 'SubnetA' }, { Ref: 'SubnetB' }],
          },
        };

        resources[cacheName] = {
          Type: 'AWS::ElastiCache::CacheCluster',
          Properties: {
            ClusterName: `prod-${spec.serviceName}-${service.name}`,
            Engine: 'redis',
            CacheNodeType: service.nodeType || 'cache.t3.micro',
            NumCacheNodes: service.numCacheNodes || 1,
            CacheSubnetGroupName: { Ref: `${cacheName}SubnetGroup` },
            VpcSecurityGroupIds: [{ Ref: 'BackingServiceSecurityGroup' }],
            Tags: [
              { Key: 'tenant', Value: tenantId },
              { Key: 'service', Value: spec.serviceName },
            ],
          },
        };
      }
    }
  }

  // App Runner Access Role (for pulling from ECR)
  resources.AppRunnerAccessRole = {
    Type: 'AWS::IAM::Role',
    Properties: {
      RoleName: `prod-${spec.serviceName}-apprunner-access`,
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

  // App Runner Instance Role (for container runtime)
  const instanceRolePolicies: any[] = [];
  
  // Add Secrets Manager access if we have backing services
  if (spec.backingServices && spec.backingServices.length > 0) {
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
            Resource: `arn:aws:secretsmanager:*:*:secret:prod-${spec.serviceName}-*`,
          },
        ],
      },
    });
  }

  resources.AppRunnerInstanceRole = {
    Type: 'AWS::IAM::Role',
    Properties: {
      RoleName: `prod-${spec.serviceName}-apprunner-instance`,
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

  // App Runner VPC Connector (if VPC is needed)
  if (needsVpc) {
    resources.AppRunnerVPCConnector = {
      Type: 'AWS::AppRunner::VpcConnector',
      Properties: {
        VpcConnectorName: `prod-${spec.serviceName}-connector`,
        Subnets: [{ Ref: 'SubnetA' }, { Ref: 'SubnetB' }],
        SecurityGroups: [{ Ref: 'AppRunnerSecurityGroup' }],
        Tags: [{ Key: 'tenant', Value: tenantId }],
      },
    };
  }

  // App Runner Service
  const appRunnerProps: any = {
    ServiceName: `prod-${spec.serviceName}`,
    SourceConfiguration: {
      AuthenticationConfiguration: {
        AccessRoleArn: { 'Fn::GetAtt': ['AppRunnerAccessRole', 'Arn'] },
      },
      ImageRepository: {
        ImageIdentifier: spec.imageUrl,
        ImageRepositoryType: 'ECR',
        ImageConfiguration: {
          Port: String(spec.port),
          RuntimeEnvironmentVariables: Object.entries(spec.envVars).map(([key, value]) => ({
            Name: key,
            Value: value,
          })),
        },
      },
    },
    InstanceConfiguration: {
      Cpu: spec.cpu,
      Memory: spec.memory,
      InstanceRoleArn: { 'Fn::GetAtt': ['AppRunnerInstanceRole', 'Arn'] },
    },
    Tags: [
      { Key: 'tenant', Value: tenantId },
      { Key: 'service', Value: spec.serviceName },
    ],
  };

  if (needsVpc) {
    appRunnerProps.NetworkConfiguration = {
      EgressConfiguration: {
        EgressType: 'VPC',
        VpcConnectorArn: { 'Fn::GetAtt': ['AppRunnerVPCConnector', 'VpcConnectorArn'] },
      },
    };
  }

  resources.AppRunnerService = {
    Type: 'AWS::AppRunner::Service',
    Properties: appRunnerProps,
  };

  // Outputs
  const outputs: any = {
    ServiceUrl: {
      Description: 'App Runner service URL',
      Value: { 'Fn::GetAtt': ['AppRunnerService', 'ServiceUrl'] },
    },
    ServiceArn: {
      Description: 'App Runner service ARN',
      Value: { 'Fn::GetAtt': ['AppRunnerService', 'ServiceArn'] },
    },
  };

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

  const template = {
    AWSTemplateFormatVersion: '2010-09-09',
    Description: `Prod deployment for ${spec.serviceName}`,
    Resources: resources,
    Outputs: outputs,
  };

  return JSON.stringify(template, null, 2);
}
