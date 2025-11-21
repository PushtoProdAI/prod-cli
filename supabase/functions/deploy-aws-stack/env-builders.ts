// Functions for building environment variables and secrets for ECS and App Runner

import type { DeploymentSpec } from './types.ts';

/**
 * Build runtime environment variables (non-sensitive, direct values)
 * These go into RuntimeEnvironmentVariables for App Runner or Environment for ECS
 */
export function buildEnvironmentVariables(spec: DeploymentSpec, resources: any): any[] {
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
  
  // Process Redis backing services (serverless-cache only)
  const redisServices = spec.backingServices?.filter(s => s.type === 'serverless-cache') || [];
  
  // Build Redis connection components for each service
  // Note: Only host/port are built here for non-sensitive env vars
  // Redis URIs with TLS (rediss://) are constructed by Lambda custom resource
  const redisConnectionInfo: Record<string, any> = {};
  for (const service of redisServices) {
    const cacheName = service.name.replace(/[^a-zA-Z0-9]/g, '');
    
    // Serverless ElastiCache uses Endpoint.Address and Endpoint.Port
    redisConnectionInfo[service.name] = {
      host: { 'Fn::GetAtt': [cacheName, 'Endpoint.Address'] },
      port: { 'Fn::GetAtt': [cacheName, 'Endpoint.Port'] },
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
    } else if (envVar.service === 'redis' && !envVar.value) {
      // Handle Redis-related env vars without values (will be populated from ElastiCache)
      const firstRedis = redisServices[0];
      if (firstRedis && redisConnectionInfo[firstRedis.name]) {
        const redisInfo = redisConnectionInfo[firstRedis.name];
        
        switch (envVar.role) {
          case 'redis_uri':
            // Redis URI is ALWAYS constructed by Lambda with TLS (rediss://)
            // Mark as added - it will be handled in RuntimeEnvironmentSecrets
            addedEnvVars.add(envVar.name);
            break;
          case 'redis_host':
            // Host can be non-sensitive (useful for debugging, monitoring)
            envVars.push({ Name: envVar.name, Value: redisInfo.host });
            addedEnvVars.add(envVar.name);
            break;
          case 'redis_port':
            // Port can be non-sensitive (useful for debugging, monitoring)
            envVars.push({ Name: envVar.name, Value: redisInfo.port });
            addedEnvVars.add(envVar.name);
            break;
          case 'redis_password':
            // Serverless ElastiCache in VPC doesn't use passwords
            // Mark as added but don't populate (would go to Secrets Manager if needed)
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

/**
 * Build runtime environment secrets (sensitive vars from Secrets Manager)
 * These go into RuntimeEnvironmentSecrets for App Runner or Secrets for ECS
 * Returns ECS format with "ValueFrom" key
 */
export function buildEnvironmentSecrets(spec: DeploymentSpec, resources: any): any[] {
  const secrets: any[] = [];
  const addedSecrets = new Set<string>();
  
  // Process PostgreSQL backing services for database credentials
  const postgresServices = spec.backingServices?.filter(s => s.type === 'rds') || [];
  
  // Process Redis backing services (serverless-cache only)
  const redisServices = spec.backingServices?.filter(s => s.type === 'serverless-cache') || [];
  
  for (const envVar of spec.envVars) {
    // Handle database-related sensitive env vars WITHOUT values (will be populated from RDS)
    if (envVar.service === 'postgresql' && !envVar.value && envVar.sensitive) {
      const firstPostgres = postgresServices[0];
      if (!firstPostgres) continue;
      
      const dbName = firstPostgres.name.replace(/[^a-zA-Z0-9]/g, '');
      
      // Database passwords are already stored in Secrets Manager by RDS resource
      // ECS requires the full ARN format with :password:: suffix to extract the password field
      if (envVar.role === 'password') {
        secrets.push({
          Name: envVar.name,
          ValueFrom: {
            'Fn::Sub': [
              '${SecretArn}:password::',
              { SecretArn: { Ref: `${dbName}Password` } },
            ],
          },
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
    } else if (envVar.service === 'redis' && !envVar.value && envVar.role === 'redis_uri') {
      // Handle Redis URI - ALWAYS use Lambda custom resource for TLS support
      // Redis URIs require TLS (rediss://) for Serverless ElastiCache
      const firstRedis = redisServices[0];
      if (!firstRedis) continue;
      
      const sanitizedName = envVar.name.replace(/[^a-zA-Z0-9]/g, '');
      
      secrets.push({
        Name: envVar.name,
        ValueFrom: { 'Fn::GetAtt': [`CustomResource${sanitizedName}`, 'SecretArn'] },
      });
      addedSecrets.add(envVar.name);
      console.log(`Using Lambda-constructed TLS Redis URL for ${envVar.name}`);
    } else if (envVar.sensitive && envVar.value) {
      // Handle ALL sensitive env vars WITH values (API keys, user-provided DATABASE_URL, etc.)
      // ECS requires the full ARN, and simple secrets (not JSON) don't need a suffix
      const sanitizedName = envVar.name.replace(/[^a-zA-Z0-9]/g, '');
      const secretId = `Secret${sanitizedName}`;
      
      secrets.push({
        Name: envVar.name,
        ValueFrom: { Ref: secretId },  // Ref returns the ARN for AWS::SecretsManager::Secret
      });
      addedSecrets.add(envVar.name);
      
      console.log(`Using Secrets Manager for ${envVar.name}`);
    }
  }
  
  return secrets;
}
