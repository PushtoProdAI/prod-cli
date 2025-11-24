/**
 * CloudFormation Custom Resource Lambda Function
 * Constructs DATABASE_URL from RDS endpoint and password secret
 * 
 * This function is deployed as a pre-built package to avoid code injection risks.
 * User-provided data is passed only as CloudFormation parameters, never in code.
 */

const { SecretsManagerClient, CreateSecretCommand, UpdateSecretCommand, DeleteSecretCommand, GetSecretValueCommand } = require('@aws-sdk/client-secrets-manager');
const { RDSClient, DescribeDBInstancesCommand } = require('@aws-sdk/client-rds');
const { ElastiCacheClient, DescribeServerlessCachesCommand, DescribeCacheClustersCommand } = require('@aws-sdk/client-elasticache');
const https = require('https');
const url = require('url');

const secretsManager = new SecretsManagerClient({});
const rds = new RDSClient({});
const elasticache = new ElastiCacheClient({});

/**
 * Send response back to CloudFormation
 */
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

/**
 * Validate input parameters to prevent injection attacks
 */
function validateInputs(properties) {
  const { DBInstanceId, PasswordSecretArn, SecretName, ServiceName, EnvVarName, ResourceType, CacheIdentifier, CacheType } = properties;
  
  // AWS resource identifier format: starts with letter, alphanumeric and hyphens, max 63 chars
  const AWS_IDENTIFIER_REGEX = /^[a-zA-Z][a-zA-Z0-9-]{0,62}$/;
  
  // Validate resource type
  const resourceType = ResourceType || 'postgres'; // Default to postgres for backward compatibility
  if (!['postgres', 'redis'].includes(resourceType)) {
    throw new Error('Invalid ResourceType. Must be "postgres" or "redis"');
  }
  
  // Validate based on resource type
  if (resourceType === 'postgres') {
    // Validate DB instance identifier format
    if (!DBInstanceId || !AWS_IDENTIFIER_REGEX.test(DBInstanceId)) {
      throw new Error('Invalid DBInstanceId format');
    }
  } else if (resourceType === 'redis') {
    // Validate cache identifier format
    if (!CacheIdentifier || !AWS_IDENTIFIER_REGEX.test(CacheIdentifier)) {
      throw new Error('Invalid CacheIdentifier format');
    }
    // Validate cache type
    if (!CacheType || !['serverless', 'cluster'].includes(CacheType)) {
      throw new Error('Invalid CacheType. Must be "serverless" or "cluster"');
    }
  }
  
  // Validate secret name format
  if (!/^\/prod\/[a-zA-Z0-9-]+\/[A-Z_]+$/.test(SecretName)) {
    throw new Error('Invalid SecretName format');
  }
  
  // Validate service name (alphanumeric and hyphens only)
  if (!/^[a-zA-Z0-9-]+$/.test(ServiceName)) {
    throw new Error('Invalid ServiceName format');
  }
  
  // Validate env var name (uppercase alphanumeric and underscores only)
  if (!/^[A-Z_]+$/.test(EnvVarName)) {
    throw new Error('Invalid EnvVarName format');
  }
  
  return { DBInstanceId, PasswordSecretArn, SecretName, ServiceName, EnvVarName, ResourceType: resourceType, CacheIdentifier, CacheType };
}

/**
 * Main Lambda handler
 */
exports.handler = async (event, context) => {
  console.log('Event:', JSON.stringify(event, null, 2));
  
  try {
    const { RequestType, ResourceProperties } = event;
    
    // Validate all inputs before processing
    const validated = validateInputs(ResourceProperties);
    const { DBInstanceId, PasswordSecretArn, SecretName, ServiceName, EnvVarName, ResourceType, CacheIdentifier, CacheType } = validated;

    // Handle DELETE request
    if (RequestType === 'Delete') {
      try {
        await secretsManager.send(new DeleteSecretCommand({
          SecretId: SecretName,
          ForceDeleteWithoutRecovery: true,
        }));
        console.log(`Successfully deleted secret: ${SecretName}`);
      } catch (err) {
        // Secret may not exist, which is fine during cleanup
        console.log(`Delete error (may not exist): ${err.message}`);
      }
      await sendResponse(event, context, 'SUCCESS', { PhysicalResourceId: SecretName });
      return;
    }

    let connectionUrl;

    if (ResourceType === 'redis') {
      // Handle Redis/ElastiCache
      console.log(`Describing ElastiCache: ${CacheIdentifier} (type: ${CacheType})`);
      
      let endpoint, port;
      
      if (CacheType === 'serverless') {
        // Serverless ElastiCache
        const cacheResponse = await elasticache.send(new DescribeServerlessCachesCommand({
          ServerlessCacheName: CacheIdentifier,
        }));
        
        if (!cacheResponse.ServerlessCaches || cacheResponse.ServerlessCaches.length === 0) {
          throw new Error(`Serverless cache not found: ${CacheIdentifier}`);
        }
        
        const cache = cacheResponse.ServerlessCaches[0];
        
        if (!cache.Endpoint) {
          throw new Error(`Serverless cache ${CacheIdentifier} does not have an endpoint yet`);
        }
        
        endpoint = cache.Endpoint.Address;
        port = cache.Endpoint.Port;
      } else {
        // Regular ElastiCache cluster
        const cacheResponse = await elasticache.send(new DescribeCacheClustersCommand({
          CacheClusterId: CacheIdentifier,
          ShowCacheNodeInfo: true,
        }));
        
        if (!cacheResponse.CacheClusters || cacheResponse.CacheClusters.length === 0) {
          throw new Error(`Cache cluster not found: ${CacheIdentifier}`);
        }
        
        const cluster = cacheResponse.CacheClusters[0];
        
        if (!cluster.CacheNodes || cluster.CacheNodes.length === 0) {
          throw new Error(`Cache cluster ${CacheIdentifier} has no nodes`);
        }
        
        const node = cluster.CacheNodes[0];
        if (!node.Endpoint) {
          throw new Error(`Cache cluster ${CacheIdentifier} does not have an endpoint yet`);
        }
        
        endpoint = node.Endpoint.Address;
        port = node.Endpoint.Port;
      }
      
      console.log(`Redis endpoint: ${endpoint}:${port}`);
      
      // Construct REDIS_URL with TLS (rediss://)
      // Serverless ElastiCache has encryption in transit enabled by default
      connectionUrl = `rediss://${endpoint}:${port}`;
      
      console.log(`Constructed Redis URL with TLS`);
      
    } else {
      // Handle PostgreSQL/RDS (original logic)
      console.log(`Describing DB instance: ${DBInstanceId}`);
      const dbResponse = await rds.send(new DescribeDBInstancesCommand({
        DBInstanceIdentifier: DBInstanceId,
      }));
      
      if (!dbResponse.DBInstances || dbResponse.DBInstances.length === 0) {
        throw new Error(`DB instance not found: ${DBInstanceId}`);
      }
      
      const dbInstance = dbResponse.DBInstances[0];
      
      if (!dbInstance.Endpoint) {
        throw new Error(`DB instance ${DBInstanceId} does not have an endpoint yet`);
      }
      
      const endpoint = dbInstance.Endpoint.Address;
      const port = dbInstance.Endpoint.Port;
      
      console.log(`DB endpoint: ${endpoint}:${port}`);

      // Get password from Secrets Manager
      console.log(`Retrieving password from: ${PasswordSecretArn}`);
      const passwordResponse = await secretsManager.send(new GetSecretValueCommand({
        SecretId: PasswordSecretArn,
      }));
      
      if (!passwordResponse.SecretString) {
        throw new Error('Password secret is empty');
      }
      
      const passwordData = JSON.parse(passwordResponse.SecretString);
      const password = passwordData.password;
      
      if (!password) {
        throw new Error('Password field not found in secret');
      }

      // Construct DATABASE_URL using URL encoding for password
      // This ensures special characters in password don't break the URL
      const encodedPassword = encodeURIComponent(password);
      connectionUrl = `postgresql://postgres:${encodedPassword}@${endpoint}:${port}/postgres`;
      
      console.log(`Constructed database URL (password hidden)`);
    }

    // Create or update the secret
    const secretParams = {
      Name: SecretName,
      Description: `${ResourceType === 'redis' ? 'Redis' : 'Database'} connection URL for ${EnvVarName}`,
      SecretString: connectionUrl,
      Tags: [
        { Key: 'service', Value: ServiceName },
        { Key: 'managed-by', Value: 'prod' },
        { Key: 'env-var', Value: EnvVarName },
        { Key: 'resource-type', Value: ResourceType },
      ],
    };

    let secretArn;
    if (RequestType === 'Create') {
      console.log(`Creating secret: ${SecretName}`);
      const createResponse = await secretsManager.send(new CreateSecretCommand(secretParams));
      secretArn = createResponse.ARN;
    } else if (RequestType === 'Update') {
      console.log(`Updating secret: ${SecretName}`);
      const updateResponse = await secretsManager.send(new UpdateSecretCommand({
        SecretId: SecretName,
        SecretString: connectionUrl,
      }));
      secretArn = updateResponse.ARN;
    }

    console.log(`Successfully ${RequestType === 'Create' ? 'created' : 'updated'} secret: ${secretArn}`);

    await sendResponse(event, context, 'SUCCESS', {
      PhysicalResourceId: SecretName,
      Data: { SecretArn: secretArn },
    });
  } catch (error) {
    console.error('Error processing request:', error);
    await sendResponse(event, context, 'FAILED', {
      Reason: error.message || 'Unknown error',
      PhysicalResourceId: event.PhysicalResourceId || 'FAILED',
    });
  }
};
