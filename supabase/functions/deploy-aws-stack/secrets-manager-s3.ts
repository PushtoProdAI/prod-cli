// Functions for building AWS Secrets Manager resources
// IMPROVED VERSION: Uses S3-hosted Lambda packages instead of inline code

import type { DeploymentSpec } from './types.ts';
import { BACKING_SERVICE_TYPE_RDS, BACKING_SERVICE_TYPE_SERVERLESS_CACHE } from './types.ts';
import { getStandardTags } from './tags.ts';

// Configuration for Lambda function packages
// Bucket is configured via LAMBDA_BUCKET environment variable in Supabase
// For multi-region support, bucket names follow the pattern: {base-bucket}-{region}
// e.g., prod-aws-deploy-us-east-1, prod-aws-deploy-us-east-2
function getLambdaPackageConfig(region: string) {
  const baseBucket = Deno.env.get('LAMBDA_BUCKET') || 'prod-aws-deploy';
  return {
    databaseUrlConstructor: {
      bucket: `${baseBucket}-${region}`,
      key: 'lambda-functions/database-url-constructor/function.zip',
      version: '1.0.0',
    },
  };
}

/**
 * Build Secrets Manager resources for sensitive environment variables
 * This includes:
 * - Simple secrets for user-provided sensitive vars (API keys, etc.)
 * - Lambda-backed custom resources for DATABASE_URL construction (from S3)
 */
export function buildSecretsManagerResources(spec: DeploymentSpec, tenantId: string, resources: any, region: string = 'us-east-1'): void {
  const LAMBDA_PACKAGES = getLambdaPackageConfig(region);
  const postgresServices = spec.backingServices?.filter(s => s.type === BACKING_SERVICE_TYPE_RDS) || [];
  const redisServices = spec.backingServices?.filter(s => s.type === BACKING_SERVICE_TYPE_SERVERLESS_CACHE) || [];
  
  console.log('buildSecretsManagerResources called:', {
    postgresServicesCount: postgresServices.length,
    redisServicesCount: redisServices.length,
    totalEnvVars: spec.envVars.length,
    sensitiveEnvVars: spec.envVars.filter(ev => ev.sensitive).length,
  });
  
  // NOTE: Service name validation and sanitization happens in template-generator.ts
  // before this function is called, so spec.serviceName is guaranteed to be valid
  
  // Create secrets for user-provided sensitive env vars
  for (const envVar of spec.envVars) {
    // Only create secrets for sensitive vars that have values
    // Skip DB vars without values (they'll be populated from RDS)
    // But DO create secrets for DB vars WITH values (user-provided external databases)
    if (envVar.sensitive && envVar.value) {
      // INPUT VALIDATION: Validate env var name
      if (!/^[A-Z_][A-Z0-9_]{0,254}$/.test(envVar.name)) {
        throw new Error(`Invalid environment variable name: ${envVar.name}. Must be uppercase with underscores.`);
      }
      
      // Sanitize the env var name for use as CloudFormation logical ID
      const sanitizedName = envVar.name.replace(/[^a-zA-Z0-9]/g, '');
      const secretId = `Secret${sanitizedName}`;
      
      resources[secretId] = {
        Type: 'AWS::SecretsManager::Secret',
        Properties: {
          Name: `/prod/${spec.serviceName}/${envVar.name}`,
          Description: `Sensitive environment variable: ${envVar.name}`,
          SecretString: envVar.value,
          Tags: getStandardTags(tenantId, spec.serviceName),
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
  });
  
  if (fullUriVars.length > 0 && postgresServices.length > 0) {
    const firstPostgres = postgresServices[0];
    
    // INPUT VALIDATION: Validate postgres service name
    if (!/^[a-z][a-z0-9-]{0,62}$/.test(firstPostgres.name)) {
      throw new Error(`Invalid postgres service name: ${firstPostgres.name}`);
    }
    
    const dbName = firstPostgres.name.replace(/[^a-zA-Z0-9]/g, '');
    
    // Create IAM role for the Lambda with scoped permissions
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
              // SCOPED: Only allow access to secrets for this service
              Resource: { 'Fn::Sub': `arn:aws:secretsmanager:\${AWS::Region}:\${AWS::AccountId}:secret:/prod/${spec.serviceName}/*` },
            }, {
              Effect: 'Allow',
              Action: ['rds:DescribeDBInstances'],
              // DescribeDBInstances requires wildcard resource
              Resource: '*',
            }],
          },
        }],
      },
    };

    // Lambda function resource using S3-hosted package
    // SECURITY: Pre-built package prevents code injection attacks
    resources.DatabaseUrlConstructorFunction = {
      Type: 'AWS::Lambda::Function',
      Properties: {
        FunctionName: `prod-${spec.serviceName}-db-url-constructor`,
        Runtime: 'nodejs20.x',
        Handler: 'index.handler',
        Role: { 'Fn::GetAtt': ['DatabaseUrlConstructorRole', 'Arn'] },
        Code: {
          // Use S3-hosted package instead of inline code
          S3Bucket: LAMBDA_PACKAGES.databaseUrlConstructor.bucket,
          S3Key: LAMBDA_PACKAGES.databaseUrlConstructor.key,
        },
        Timeout: 30,
        Description: `Database URL constructor for ${spec.serviceName} (v${LAMBDA_PACKAGES.databaseUrlConstructor.version})`,
        Tags: getStandardTags(tenantId, spec.serviceName),
      },
    };

    // Create custom resource for each full_uri variable
    for (const envVar of fullUriVars) {
      // INPUT VALIDATION: Validate env var name
      if (!/^[A-Z_][A-Z0-9_]{0,254}$/.test(envVar.name)) {
        throw new Error(`Invalid environment variable name: ${envVar.name}`);
      }
      
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
          // All user data passed as parameters (validated by Lambda)
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
  
  // Create Lambda-backed custom resource to construct REDIS_URL with TLS
  // ALL redis_uri variables use Lambda (not just sensitive ones) to ensure TLS (rediss://)
  const redisUriVars = spec.envVars.filter(
    ev => ev.role === 'redis_uri' && !ev.value && ev.service === 'redis'
  );
  
  console.log('Checking for redis_uri vars:', {
    redisUriVarsCount: redisUriVars.length,
    redisUriVarNames: redisUriVars.map(v => v.name),
    redisServicesCount: redisServices.length,
    note: 'All redis_uri vars use Lambda for TLS support (rediss://)',
  });
  
  if (redisUriVars.length > 0 && redisServices.length > 0) {
    const firstRedis = redisServices[0];
    
    // INPUT VALIDATION: Validate redis service name
    if (!/^[a-z][a-z0-9-]{0,62}$/.test(firstRedis.name)) {
      throw new Error(`Invalid redis service name: ${firstRedis.name}`);
    }
    
    const cacheName = firstRedis.name.replace(/[^a-zA-Z0-9]/g, '');
    
    // Create IAM role for the Lambda with scoped permissions (if not already created)
    if (!resources.DatabaseUrlConstructorRole) {
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
                Resource: { 'Fn::Sub': `arn:aws:secretsmanager:\${AWS::Region}:\${AWS::AccountId}:secret:/prod/${spec.serviceName}/*` },
              }, {
                Effect: 'Allow',
                Action: ['rds:DescribeDBInstances'],
                Resource: '*', // DescribeDBInstances requires wildcard resource
              }, {
                Effect: 'Allow',
                Action: ['elasticache:DescribeCacheClusters', 'elasticache:DescribeServerlessCaches'],
                Resource: '*', // ElastiCache describe operations require wildcard
              }],
            },
          }],
        },
      };
    } else {
      // Add ElastiCache permissions to existing role
      const existingPolicy = resources.DatabaseUrlConstructorRole.Properties.Policies[0];
      existingPolicy.PolicyDocument.Statement.push({
        Effect: 'Allow',
        Action: ['elasticache:DescribeCacheClusters', 'elasticache:DescribeServerlessCaches'],
        Resource: '*',
      });
    }

    // Create Lambda function (if not already created)
    if (!resources.DatabaseUrlConstructorFunction) {
      resources.DatabaseUrlConstructorFunction = {
        Type: 'AWS::Lambda::Function',
        Properties: {
          FunctionName: `prod-${spec.serviceName}-db-url-constructor`,
          Runtime: 'nodejs20.x',
          Handler: 'index.handler',
          Role: { 'Fn::GetAtt': ['DatabaseUrlConstructorRole', 'Arn'] },
          Code: {
            S3Bucket: LAMBDA_PACKAGES.databaseUrlConstructor.bucket,
            S3Key: LAMBDA_PACKAGES.databaseUrlConstructor.key,
          },
          Timeout: 30,
          Description: `URL constructor for ${spec.serviceName} (v${LAMBDA_PACKAGES.databaseUrlConstructor.version})`,
          Tags: getStandardTags(tenantId, spec.serviceName),
        },
      };
    }

    // Create custom resource for each redis_uri variable
    for (const envVar of redisUriVars) {
      // INPUT VALIDATION: Validate env var name
      if (!/^[A-Z_][A-Z0-9_]{0,254}$/.test(envVar.name)) {
        throw new Error(`Invalid environment variable name: ${envVar.name}`);
      }
      
      const sanitizedName = envVar.name.replace(/[^a-zA-Z0-9]/g, '');
      const secretName = `/prod/${spec.serviceName}/${envVar.name}`;
      const cacheIdentifier = `prod-${spec.serviceName}-${firstRedis.name}`;
      
      // Using Serverless ElastiCache
      const cacheType = 'serverless';
      
      resources[`CustomResource${sanitizedName}`] = {
        Type: 'AWS::CloudFormation::CustomResource',
        Properties: {
          ServiceToken: { 'Fn::GetAtt': ['DatabaseUrlConstructorFunction', 'Arn'] },
          CacheIdentifier: cacheIdentifier,
          CacheType: cacheType,
          SecretName: secretName,
          ServiceName: spec.serviceName,
          EnvVarName: envVar.name,
          ResourceType: 'redis', // Indicate this is a Redis resource
        },
        DependsOn: [cacheName, 'DatabaseUrlConstructorFunction'],
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
      
      console.log(`Creating Lambda-backed custom resource for Redis ${envVar.name}`);
    }
  }
}
