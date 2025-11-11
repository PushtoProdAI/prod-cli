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
  sensitive?: boolean; // true if variable contains sensitive data (API keys, passwords, etc.)
  sensitivityReason?: string; // explanation for why variable is sensitive
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

// Build Secrets Manager resources for sensitive environment variables
function buildSecretsManagerResources(spec: DeploymentSpec, resources: any): void {
  const postgresServices = spec.backingServices?.filter(s => s.type === 'rds') || [];
  
  console.log('buildSecretsManagerResources called:', {
    postgresServicesCount: postgresServices.length,
    totalEnvVars: spec.envVars.length,
    sensitiveEnvVars: spec.envVars.filter(ev => ev.sensitive).length,
  });
  
  // Create secrets for user-provided sensitive env vars
  for (const envVar of spec.envVars) {
    // Only create secrets for sensitive vars that have values
    // Skip DB vars without values (they'll be populated from RDS)
    // But DO create secrets for DB vars WITH values (user-provided external databases)
    if (envVar.sensitive && envVar.value) {
      // Sanitize the env var name for use as CloudFormation logical ID
      const sanitizedName = envVar.name.replace(/[^a-zA-Z0-9]/g, '');
      const secretId = `Secret${sanitizedName}`;
      
      resources[secretId] = {
        Type: 'AWS::SecretsManager::Secret',
        Properties: {
          Name: `/prod/${spec.serviceName}/${envVar.name}`,
          Description: `Sensitive environment variable: ${envVar.name}`,
          SecretString: envVar.value,
          Tags: [
            { Key: 'service', Value: spec.serviceName },
            { Key: 'managed-by', Value: 'prod' },
            { Key: 'env-var', Value: envVar.name },
          ],
        },
      };
      
      console.log(`Creating Secrets Manager secret for ${envVar.name} (${envVar.sensitivityReason})`);
    }
  }
  
  // Create Lambda-backed custom resource to construct DATABASE_URL securely
  // This is necessary because CloudFormation resolves {{resolve:secretsmanager:...}} during stack creation
  // Using a Lambda allows us to construct the URL at runtime and store it in Secrets Manager
  const fullUriVars = spec.envVars.filter(
    ev => ev.role === 'full_uri' && !ev.value && ev.service === 'postgresql' && ev.sensitive
  );
  
  console.log('Checking for full_uri vars:', {
    fullUriVarsCount: fullUriVars.length,
    fullUriVarNames: fullUriVars.map(v => v.name),
    postgresServicesCount: postgresServices.length,
    allEnvVars: spec.envVars.map(ev => ({
      name: ev.name,
      role: ev.role,
      service: ev.service,
      sensitive: ev.sensitive,
      hasValue: !!ev.value,
    })),
  });
  
  if (fullUriVars.length > 0 && postgresServices.length > 0) {
    const firstPostgres = postgresServices[0];
    const dbName = firstPostgres.name.replace(/[^a-zA-Z0-9]/g, '');
    
    // Create IAM role for the Lambda
    resources.DatabaseUrlConstructorRole = {
      Type: 'AWS::IAM::Role',
      Properties: {
        AssumeRolePolicyDocument: {
          Version: '2012-10-17',
          Statement: [{
            Effect: 'Allow',
            Principal: { Service: 'lambda.amazonaws.com' },
            Action: 'sts:AssumeRole',
          }],
        },
        ManagedPolicyArns: [
          'arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole',
        ],
        Policies: [{
          PolicyName: 'SecretsManagerAccess',
          PolicyDocument: {
            Version: '2012-10-17',
            Statement: [{
              Effect: 'Allow',
              Action: [
                'secretsmanager:CreateSecret',
                'secretsmanager:UpdateSecret',
                'secretsmanager:DeleteSecret',
                'secretsmanager:GetSecretValue',
                'secretsmanager:TagResource',
              ],
              Resource: '*',
            }, {
              Effect: 'Allow',
              Action: ['rds:DescribeDBInstances'],
              Resource: '*',
            }],
          },
        }],
      },
    };

    // Inline Lambda function to construct DATABASE_URL
    const lambdaCode = `
const {  SecretsManagerClient, CreateSecretCommand, UpdateSecretCommand, DeleteSecretCommand, GetSecretValueCommand } = require('@aws-sdk/client-secrets-manager');
const { RDSClient, DescribeDBInstancesCommand } = require('@aws-sdk/client-rds');
const https = require('https');
const url = require('url');

const secretsManager = new SecretsManagerClient({});
const rds = new RDSClient({});

async function sendResponse(event, context, status, data) {
  const responseBody = JSON.stringify({
    Status: status,
    Reason: data.Reason || 'See CloudWatch logs',
    PhysicalResourceId: data.PhysicalResourceId || context.logStreamName,
    StackId: event.StackId,
    RequestId: event.RequestId,
    LogicalResourceId: event.LogicalResourceId,
    Data: data.Data || {},
  });

  const parsedUrl = url.parse(event.ResponseURL);
  const options = {
    hostname: parsedUrl.hostname,
    port: 443,
    path: parsedUrl.path,
    method: 'PUT',
    headers: {
      'Content-Type': '',
      'Content-Length': responseBody.length,
    },
  };

  return new Promise((resolve, reject) => {
    const req = https.request(options, (res) => {
      resolve();
    });
    req.on('error', reject);
    req.write(responseBody);
    req.end();
  });
}

exports.handler = async (event, context) => {
  console.log('Event:', JSON.stringify(event));
  
  try {
    const { RequestType, ResourceProperties } = event;
    const { DBInstanceId, PasswordSecretArn, SecretName, ServiceName, EnvVarName } = ResourceProperties;

    if (RequestType === 'Delete') {
      try {
        await secretsManager.send(new DeleteSecretCommand({
          SecretId: SecretName,
          ForceDeleteWithoutRecovery: true,
        }));
      } catch (err) {
        console.log('Delete error (may not exist):', err.message);
      }
      await sendResponse(event, context, 'SUCCESS', { PhysicalResourceId: SecretName });
      return;
    }

    // Get DB instance details
    const dbResponse = await rds.send(new DescribeDBInstancesCommand({
      DBInstanceIdentifier: DBInstanceId,
    }));
    const dbInstance = dbResponse.DBInstances[0];
    const endpoint = dbInstance.Endpoint.Address;
    const port = dbInstance.Endpoint.Port;

    // Get password from Secrets Manager
    const passwordResponse = await secretsManager.send(new GetSecretValueCommand({
      SecretId: PasswordSecretArn,
    }));
    const passwordData = JSON.parse(passwordResponse.SecretString);
    const password = passwordData.password;

    // Construct DATABASE_URL
    const databaseUrl = \`postgresql://postgres:\${password}@\${endpoint}:\${port}/postgres\`;

    // Create or update the secret
    const secretParams = {
      Name: SecretName,
      Description: \`Database connection URL for \${EnvVarName}\`,
      SecretString: databaseUrl,
      Tags: [
        { Key: 'service', Value: ServiceName },
        { Key: 'managed-by', Value: 'prod' },
        { Key: 'env-var', Value: EnvVarName },
      ],
    };

    let secretArn;
    if (RequestType === 'Create') {
      const createResponse = await secretsManager.send(new CreateSecretCommand(secretParams));
      secretArn = createResponse.ARN;
    } else if (RequestType === 'Update') {
      const updateResponse = await secretsManager.send(new UpdateSecretCommand({
        SecretId: SecretName,
        SecretString: databaseUrl,
      }));
      secretArn = updateResponse.ARN;
    }

    await sendResponse(event, context, 'SUCCESS', {
      PhysicalResourceId: SecretName,
      Data: { SecretArn: secretArn },
    });
  } catch (error) {
    console.error('Error:', error);
    await sendResponse(event, context, 'FAILED', {
      Reason: error.message,
      PhysicalResourceId: event.PhysicalResourceId || 'FAILED',
    });
  }
};
`;

    // Lambda function resource
    resources.DatabaseUrlConstructorFunction = {
      Type: 'AWS::Lambda::Function',
      Properties: {
        FunctionName: `prod-${spec.serviceName}-db-url-constructor`,
        Runtime: 'nodejs20.x',
        Handler: 'index.handler',
        Role: { 'Fn::GetAtt': ['DatabaseUrlConstructorRole', 'Arn'] },
        Code: {
          ZipFile: lambdaCode,
        },
        Timeout: 30,
      },
    };

    // Create custom resource for each full_uri variable
    for (const envVar of fullUriVars) {
      const sanitizedName = envVar.name.replace(/[^a-zA-Z0-9]/g, '');
      const secretName = `/prod/${spec.serviceName}/${envVar.name}`;
      const dbInstanceIdentifier = `prod-${spec.serviceName}-${firstPostgres.name}`;
      
      resources[`CustomResource${sanitizedName}`] = {
        Type: 'AWS::CloudFormation::CustomResource',
        Properties: {
          ServiceToken: { 'Fn::GetAtt': ['DatabaseUrlConstructorFunction', 'Arn'] },
          DBInstanceId: dbInstanceIdentifier,
          PasswordSecretArn: { Ref: `${dbName}Password` },
          SecretName: secretName,
          ServiceName: spec.serviceName,
          EnvVarName: envVar.name,
        },
        DependsOn: [dbName, `${dbName}Password`, 'DatabaseUrlConstructorFunction'],
      };

      // Also create a reference resource that other resources can depend on
      resources[`Secret${sanitizedName}`] = {
        Type: 'AWS::CloudFormation::WaitConditionHandle',
        Metadata: {
          SecretName: secretName,
          Description: `Reference to ${envVar.name} secret created by Lambda`,
        },
        DependsOn: [`CustomResource${sanitizedName}`],
      };
      
      console.log(`Creating Lambda-backed custom resource for ${envVar.name}`);
    }
  }
}

// Build runtime environment variables (non-sensitive, direct values)
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
            // Full URI is constructed by Lambda and stored in Secrets Manager
            // It will be added to RuntimeEnvironmentSecrets, not RuntimeEnvironmentVariables
            // Mark as added so we don't try to add it again
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
            // Database passwords should go to Secrets Manager, not plain env vars
            // Skip them here - they'll be added to RuntimeEnvironmentSecrets
            addedEnvVars.add(envVar.name);
            break;
          case 'database_name':
            envVars.push({ Name: envVar.name, Value: dbInfo.database });
            addedEnvVars.add(envVar.name);
            break;
        }
      }
    } else if (envVar.value) {
      // Skip sensitive vars - they'll be added to RuntimeEnvironmentSecrets instead
      if (envVar.sensitive) {
        continue;
      }
      
      // Add non-sensitive, non-database env vars with values
      envVars.push({
        Name: envVar.name,
        Value: envVar.value,
      });
      addedEnvVars.add(envVar.name);
    }
  }
  
  // Don't add default DATABASE_URL here anymore - it's handled in Secrets Manager
  // if it's sensitive (which it should be)
  
  return envVars;
}

// Build runtime environment secrets (sensitive vars from Secrets Manager)
function buildEnvironmentSecrets(spec: DeploymentSpec, resources: any): any[] {
  const secrets: any[] = [];
  const addedSecrets = new Set<string>();
  
  // Process PostgreSQL backing services for database credentials
  const postgresServices = spec.backingServices?.filter(s => s.type === 'rds') || [];
  
  for (const envVar of spec.envVars) {
    // Handle database-related sensitive env vars WITHOUT values (will be populated from RDS)
    if (envVar.service === 'postgresql' && !envVar.value && envVar.sensitive) {
      const firstPostgres = postgresServices[0];
      if (!firstPostgres) continue;
      
      const dbName = firstPostgres.name.replace(/[^a-zA-Z0-9]/g, '');
      
      // Database passwords are already stored in Secrets Manager by RDS resource
      if (envVar.role === 'password') {
        secrets.push({
          Name: envVar.name,
          ValueFrom: { Ref: `${dbName}Password` },
        });
        addedSecrets.add(envVar.name);
      } else if (envVar.role === 'full_uri') {
        // Full URI is constructed by Lambda custom resource and stored in Secrets Manager
        // Get the ARN from the Custom Resource output
        const sanitizedName = envVar.name.replace(/[^a-zA-Z0-9]/g, '');
        
        secrets.push({
          Name: envVar.name,
          ValueFrom: { 'Fn::GetAtt': [`CustomResource${sanitizedName}`, 'SecretArn'] },
        });
        addedSecrets.add(envVar.name);
        console.log(`Using Lambda-constructed secret for ${envVar.name}`);
      }
    } else if (envVar.sensitive && envVar.value) {
      // Handle ALL sensitive env vars WITH values (API keys, user-provided DATABASE_URL, etc.)
      const sanitizedName = envVar.name.replace(/[^a-zA-Z0-9]/g, '');
      const secretId = `Secret${sanitizedName}`;
      
      secrets.push({
        Name: envVar.name,
        ValueFrom: { Ref: secretId },
      });
      addedSecrets.add(envVar.name);
      
      console.log(`Using Secrets Manager for ${envVar.name}`);
    }
  }
  
  return secrets;
}

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
    // Only create these if migrations are present
    if (hasMigrations) {
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
    }

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
            Name: `/prod/${spec.serviceName}/${service.name.toUpperCase()}_PASSWORD`,
            Description: `Password for ${service.name}`,
            GenerateSecretString: {
              SecretStringTemplate: JSON.stringify({ username: 'postgres' }),
              GenerateStringKey: 'password',
              PasswordLength: 32,
              ExcludeCharacters: '"@/:?#[]!$&\'()*+,;=\\% ',
            },
            Tags: [
              { Key: 'service', Value: spec.serviceName },
              { Key: 'managed-by', Value: 'prod' },
              { Key: 'db-service', Value: service.name },
            ],
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
  
  // Check if we need Secrets Manager access (for backing services or sensitive env vars)
  const hasSensitiveVars = spec.envVars.some(ev => ev.sensitive);
  
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
            Resource: `arn:aws:secretsmanager:*:*:secret:/prod/${spec.serviceName}/*`,
          },
        ],
      },
    });
    console.log('Added Secrets Manager permissions to Instance Role');
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

  // Build Secrets Manager resources for sensitive env vars (including Lambda-backed custom resources)
  // This must be called early so secrets exist for both migrations and App Runner
  buildSecretsManagerResources(spec, resources);

  // ECS resources for running migrations (only if migration command exists)
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
                  Resource: [
                    `arn:aws:secretsmanager:*:*:secret:prod-${spec.serviceName}-*`,
                    `arn:aws:secretsmanager:*:*:secret:/prod/${spec.serviceName}/*`,
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
                  Resource: [
                    `arn:aws:secretsmanager:*:*:secret:prod-${spec.serviceName}-*`,
                    `arn:aws:secretsmanager:*:*:secret:/prod/${spec.serviceName}/*`,
                  ],
                },
              ],
            },
          },
        ],
        Tags: [{ Key: 'tenant', Value: tenantId }],
      },
    };

    // Build dependencies for custom resources that create DATABASE_URL secrets
    // (buildSecretsManagerResources was already called earlier)
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

    // Secrets Manager resources are already built earlier (before migration task)
    // Just add dependencies on custom resources that create DATABASE_URL secrets
    const fullUriVars = spec.envVars.filter(
      ev => ev.role === 'full_uri' && !ev.value && ev.service === 'postgresql' && ev.sensitive
    );
    for (const envVar of fullUriVars) {
      const sanitizedName = envVar.name.replace(/[^a-zA-Z0-9]/g, '');
      appRunnerDependsOn.push(`CustomResource${sanitizedName}`);
    }
    
    // Build environment variables array for App Runner
    // Non-sensitive vars go to RuntimeEnvironmentVariables
    // Sensitive vars go to RuntimeEnvironmentSecrets
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
