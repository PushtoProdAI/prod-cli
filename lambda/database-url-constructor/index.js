/**
 * CloudFormation Custom Resource Lambda Function
 * Constructs DATABASE_URL from RDS endpoint and password secret
 * 
 * This function is deployed as a pre-built package to avoid code injection risks.
 * User-provided data is passed only as CloudFormation parameters, never in code.
 */

const { SecretsManagerClient, CreateSecretCommand, UpdateSecretCommand, DeleteSecretCommand, GetSecretValueCommand } = require('@aws-sdk/client-secrets-manager');
const { RDSClient, DescribeDBInstancesCommand } = require('@aws-sdk/client-rds');
const https = require('https');
const url = require('url');

const secretsManager = new SecretsManagerClient({});
const rds = new RDSClient({});

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
  const { DBInstanceId, PasswordSecretArn, SecretName, ServiceName, EnvVarName } = properties;
  
  // Validate DB instance identifier format
  if (!/^[a-zA-Z][a-zA-Z0-9-]{0,62}$/.test(DBInstanceId)) {
    throw new Error('Invalid DBInstanceId format');
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
  
  return { DBInstanceId, PasswordSecretArn, SecretName, ServiceName, EnvVarName };
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
    const { DBInstanceId, PasswordSecretArn, SecretName, ServiceName, EnvVarName } = validated;

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

    // Get DB instance details
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
    const databaseUrl = `postgresql://postgres:${encodedPassword}@${endpoint}:${port}/postgres`;
    
    console.log(`Constructed database URL (password hidden)`);

    // Create or update the secret
    const secretParams = {
      Name: SecretName,
      Description: `Database connection URL for ${EnvVarName}`,
      SecretString: databaseUrl,
      Tags: [
        { Key: 'service', Value: ServiceName },
        { Key: 'managed-by', Value: 'prod' },
        { Key: 'env-var', Value: EnvVarName },
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
        SecretString: databaseUrl,
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
