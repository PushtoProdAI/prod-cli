// Functions for building AWS Secrets Manager resources

import type { DeploymentSpec } from './types.ts';

/**
 * Build Secrets Manager resources for sensitive environment variables
 * This includes:
 * - Simple secrets for user-provided sensitive vars (API keys, etc.)
 * - Lambda-backed custom resources for DATABASE_URL construction
 */
export function buildSecretsManagerResources(spec: DeploymentSpec, resources: any): void {
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

    // Lambda function code (inlined for reliability in Supabase Edge Functions)
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
    // NOTE: No DependsOn needed here - the CustomResource handles dependencies
    // The Lambda function itself can be created without waiting for RDS
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
      
      // Pass the secret NAME instead of ARN for more reliable lookups
      // The secret name is explicitly set in backing-services.ts
      const passwordSecretName = `/prod/${spec.serviceName}/${firstPostgres.name.toUpperCase()}_PASSWORD`;
      
      resources[`CustomResource${sanitizedName}`] = {
        Type: 'AWS::CloudFormation::CustomResource',
        Properties: {
          ServiceToken: { 'Fn::GetAtt': ['DatabaseUrlConstructorFunction', 'Arn'] },
          DBInstanceId: dbInstanceIdentifier,
          PasswordSecretArn: passwordSecretName,
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
