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

interface EnvVar {
  name: string;
  value?: string;
  role?: string;    // "full_uri", "hostname", "port", "username", "password", "database_name", etc.
  service?: string; // "postgresql", "redis", etc.
}

interface DeploymentSpec {
  serviceName: string;
  imageUrl: string;
  cpu: string;
  memory: string;
  port: number;
  envVars: EnvVar[];
  backingServices?: BackingService[];
  migrationCommand?: string;
  createAppRunner?: boolean; // If true, create App Runner service (post-migration)
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

function buildEnvironmentVariables(spec: DeploymentSpec, resources: any): any[] {
  const envVars: any[] = [];
  const addedEnvVars = new Set<string>();
  
  // Process PostgreSQL backing services
  const postgresServices = spec.backingServices?.filter(s => s.type === 'rds') || [];
  
  // Build database connection components for each PostgreSQL service
  const dbConnectionInfo: Record<string, any> = {};
  for (const service of postgresServices) {
    const dbName = service.name.replace(/[^a-zA-Z0-9]/g, '');
    
    // Use Fn::Sub with proper secret reference
    // Format: {{resolve:secretsmanager:secret-id:SecretString:json-key::}}
    dbConnectionInfo[service.name] = {
      host: { 'Fn::GetAtt': [dbName, 'Endpoint.Address'] },
      port: { 'Fn::GetAtt': [dbName, 'Endpoint.Port'] },
      username: 'postgres',
      password: {
        'Fn::Sub': [
          '{{resolve:secretsmanager:${SecretId}:SecretString:password::}}',
          { SecretId: { Ref: `${dbName}Password` } },
        ],
      },
      database: 'postgres',
      connectionString: {
        'Fn::Sub': [
          'postgresql://postgres:{{resolve:secretsmanager:${SecretId}:SecretString:password::}}@${Endpoint}:${Port}/postgres',
          {
            SecretId: { Ref: `${dbName}Password` },
            Endpoint: { 'Fn::GetAtt': [dbName, 'Endpoint.Address'] },
            Port: { 'Fn::GetAtt': [dbName, 'Endpoint.Port'] },
          },
        ],
      },
    };
  }
  
  // Map categorized env vars to their database values
  for (const envVar of spec.envVars) {
    // Skip database-related env vars without values (will be populated from RDS)
    if (envVar.service === 'postgresql' && !envVar.value) {
      // Find the first PostgreSQL service (or could be smarter about matching)
      const firstPostgres = postgresServices[0];
      if (firstPostgres && dbConnectionInfo[firstPostgres.name]) {
        const dbInfo = dbConnectionInfo[firstPostgres.name];
        
        switch (envVar.role) {
          case 'full_uri':
            envVars.push({ Name: envVar.name, Value: dbInfo.connectionString });
            addedEnvVars.add(envVar.name);
            break;
          case 'hostname':
            envVars.push({ Name: envVar.name, Value: dbInfo.host });
            addedEnvVars.add(envVar.name);
            break;
          case 'port':
            envVars.push({ Name: envVar.name, Value: dbInfo.port });
            addedEnvVars.add(envVar.name);
            break;
          case 'username':
            envVars.push({ Name: envVar.name, Value: dbInfo.username });
            addedEnvVars.add(envVar.name);
            break;
          case 'password':
            // For password, we use Fn::Sub to resolve from Secrets Manager
            envVars.push({ Name: envVar.name, Value: dbInfo.password });
            addedEnvVars.add(envVar.name);
            break;
          case 'database_name':
            envVars.push({ Name: envVar.name, Value: dbInfo.database });
            addedEnvVars.add(envVar.name);
            break;
        }
      }
    } else if (envVar.value) {
      // Add non-database env vars with values
      envVars.push({
        Name: envVar.name,
        Value: envVar.value,
      });
      addedEnvVars.add(envVar.name);
    }
  }
  
  // Add default DATABASE_URL if not already added and we have a PostgreSQL service
  if (!addedEnvVars.has('DATABASE_URL') && postgresServices.length > 0) {
    const firstPostgres = postgresServices[0];
    if (dbConnectionInfo[firstPostgres.name]) {
      envVars.push({
        Name: 'DATABASE_URL',
        Value: dbConnectionInfo[firstPostgres.name].connectionString,
      });
    }
  }
  
  return envVars;
}

function generateCloudFormationTemplate(spec: DeploymentSpec, tenantId: string): string {
  const resources: any = {};

  // Create VPC and networking if backing services are needed
  const needsVpc = spec.backingServices && spec.backingServices.length > 0;
  console.log('Generating CloudFormation template:', {
    backingServicesCount: spec.backingServices?.length || 0,
    needsVpc: needsVpc,
  });

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

    // Subnets - Private subnets for RDS and other backing services
    resources.PrivateSubnetAZ1 = {
      Type: 'AWS::EC2::Subnet',
      Properties: {
        VpcId: { Ref: 'VPC' },
        CidrBlock: '10.0.1.0/24',
        AvailabilityZone: { 'Fn::Select': [0, { 'Fn::GetAZs': '' }] },
        Tags: [
          { Key: 'Name', Value: `prod-${spec.serviceName}-private-az1` },
          { Key: 'Type', Value: 'Private' },
        ],
      },
    };

    resources.PrivateSubnetAZ2 = {
      Type: 'AWS::EC2::Subnet',
      Properties: {
        VpcId: { Ref: 'VPC' },
        CidrBlock: '10.0.2.0/24',
        AvailabilityZone: { 'Fn::Select': [1, { 'Fn::GetAZs': '' }] },
        Tags: [
          { Key: 'Name', Value: `prod-${spec.serviceName}-private-az2` },
          { Key: 'Type', Value: 'Private' },
        ],
      },
    };

    // Public subnets for ECS tasks (need internet access to pull images from ECR)
    resources.PublicSubnetAZ1 = {
      Type: 'AWS::EC2::Subnet',
      Properties: {
        VpcId: { Ref: 'VPC' },
        CidrBlock: '10.0.10.0/24',
        AvailabilityZone: { 'Fn::Select': [0, { 'Fn::GetAZs': '' }] },
        MapPublicIpOnLaunch: true,
        Tags: [
          { Key: 'Name', Value: `prod-${spec.serviceName}-public-az1` },
          { Key: 'Type', Value: 'Public' },
        ],
      },
    };

    resources.PublicSubnetAZ2 = {
      Type: 'AWS::EC2::Subnet',
      Properties: {
        VpcId: { Ref: 'VPC' },
        CidrBlock: '10.0.11.0/24',
        AvailabilityZone: { 'Fn::Select': [1, { 'Fn::GetAZs': '' }] },
        MapPublicIpOnLaunch: true,
        Tags: [
          { Key: 'Name', Value: `prod-${spec.serviceName}-public-az2` },
          { Key: 'Type', Value: 'Public' },
        ],
      },
    };

    // Internet Gateway for public subnets
    resources.InternetGateway = {
      Type: 'AWS::EC2::InternetGateway',
      Properties: {
        Tags: [
          { Key: 'Name', Value: `prod-${spec.serviceName}-igw` },
        ],
      },
    };

    resources.AttachGateway = {
      Type: 'AWS::EC2::VPCGatewayAttachment',
      Properties: {
        VpcId: { Ref: 'VPC' },
        InternetGatewayId: { Ref: 'InternetGateway' },
      },
    };

    // Route table for public subnets
    resources.PublicRouteTable = {
      Type: 'AWS::EC2::RouteTable',
      Properties: {
        VpcId: { Ref: 'VPC' },
        Tags: [
          { Key: 'Name', Value: `prod-${spec.serviceName}-public-rt` },
        ],
      },
    };

    resources.PublicRoute = {
      Type: 'AWS::EC2::Route',
      DependsOn: 'AttachGateway',
      Properties: {
        RouteTableId: { Ref: 'PublicRouteTable' },
        DestinationCidrBlock: '0.0.0.0/0',
        GatewayId: { Ref: 'InternetGateway' },
      },
    };

    resources.PublicSubnetRouteTableAssociationAZ1 = {
      Type: 'AWS::EC2::SubnetRouteTableAssociation',
      Properties: {
        SubnetId: { Ref: 'PublicSubnetAZ1' },
        RouteTableId: { Ref: 'PublicRouteTable' },
      },
    };

    resources.PublicSubnetRouteTableAssociationAZ2 = {
      Type: 'AWS::EC2::SubnetRouteTableAssociation',
      Properties: {
        SubnetId: { Ref: 'PublicSubnetAZ2' },
        RouteTableId: { Ref: 'PublicRouteTable' },
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
          SubnetIds: [{ Ref: 'PrivateSubnetAZ1' }, { Ref: 'PrivateSubnetAZ2' }],
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
        // Exclude characters that have special meaning in URLs or can cause parsing issues
        resources[`${dbName}Password`] = {
          Type: 'AWS::SecretsManager::Secret',
          Properties: {
            Description: `Password for ${service.name}`,
            GenerateSecretString: {
              SecretStringTemplate: JSON.stringify({ username: 'postgres' }),
              GenerateStringKey: 'password',
              PasswordLength: 32,
              ExcludeCharacters: '"@/:?#[]!$&\'()*+,;=\\% ',
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
              'Fn::Sub': [
                '{{resolve:secretsmanager:${SecretId}:SecretString:password::}}',
                { SecretId: { Ref: `${dbName}Password` } },
              ],
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
            SubnetIds: [{ Ref: 'PrivateSubnetAZ1' }, { Ref: 'PrivateSubnetAZ2' }],
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

  // ECS resources for running migrations (only if migration command exists)
  const hasMigrations = spec.migrationCommand && spec.migrationCommand.trim() !== '';
  
  if (hasMigrations) {
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

    // ECS Task Execution Role (for pulling images and logging)
    resources.ECSTaskExecutionRole = {
      Type: 'AWS::IAM::Role',
      Properties: {
        RoleName: `prod-${spec.serviceName}-ecs-execution`,
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
                  Resource: `arn:aws:secretsmanager:*:*:secret:prod-${spec.serviceName}-*`,
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
                  Resource: `arn:aws:logs:*:*:log-group:/ecs/prod-${spec.serviceName}*`,
                },
              ],
            },
          },
        ],
        Tags: [{ Key: 'tenant', Value: tenantId }],
      },
    };

    // ECS Task Role (for application permissions - access to secrets, etc)
    resources.ECSTaskRole = {
      Type: 'AWS::IAM::Role',
      Properties: {
        RoleName: `prod-${spec.serviceName}-ecs-task`,
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
                  Resource: `arn:aws:secretsmanager:*:*:secret:prod-${spec.serviceName}-*`,
                },
              ],
            },
          },
        ],
        Tags: [{ Key: 'tenant', Value: tenantId }],
      },
    };

    // ECS Task Definition for migrations
    resources.MigrationTaskDefinition = {
      Type: 'AWS::ECS::TaskDefinition',
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

  // App Runner Service - only create if explicitly requested (after first migration)
  // On first deploy: stack creates infrastructure WITHOUT App Runner
  // After migration runs: stack is updated WITH createAppRunner=true to add App Runner
  const shouldCreateAppRunner = spec.createAppRunner !== false; // Default to true for backward compatibility
  
  if (shouldCreateAppRunner) {
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

    // Build environment variables array for App Runner
    // App Runner expects an array of {Name, Value} objects, not a map
    const runtimeEnvVars = buildEnvironmentVariables(spec, resources);

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
            ImageConfiguration: {
              Port: String(spec.port),
              RuntimeEnvironmentVariables: runtimeEnvVars,
            },
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
  } else {
    console.log('Skipping App Runner Service creation - will be added after migration');
  }

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
    outputs.PublicSubnetAZ1 = {
      Description: 'Public Subnet AZ1 ID',
      Value: { Ref: 'PublicSubnetAZ1' },
    };
    outputs.PublicSubnetAZ2 = {
      Description: 'Public Subnet AZ2 ID',
      Value: { Ref: 'PublicSubnetAZ2' },
    };
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
