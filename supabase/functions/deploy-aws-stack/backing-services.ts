// Backing services (RDS, Serverless ElastiCache) for AWS deployments

import type { DeploymentSpec } from './types.ts';
import { getStandardTags } from './tags.ts';

/**
 * Build backing service resources (RDS databases, Serverless ElastiCache)
 * Creates password secrets, database instances, and serverless cache clusters as specified
 */
export function buildBackingServices(
  spec: DeploymentSpec,
  tenantId: string,
  resources: any
): void {
  if (!spec.backingServices) {
    return;
  }

  for (const service of spec.backingServices) {
    if (service.type === 'rds') {
      buildRDSInstance(spec.serviceName, service, tenantId, resources);
    } else if (service.type === 'serverless-cache') {
      buildServerlessElastiCache(spec.serviceName, service, tenantId, resources);
    }
  }
}

/**
 * Build RDS database instance with auto-generated password secret
 */
function buildRDSInstance(
  serviceName: string,
  service: any,
  tenantId: string,
  resources: any
): void {
  const dbName = service.name.replace(/[^a-zA-Z0-9]/g, '');
  
  // Generate random password secret
  // Exclude characters that have special meaning in URLs or can cause parsing issues
  resources[`${dbName}Password`] = {
    Type: 'AWS::SecretsManager::Secret',
    Properties: {
      Name: `/prod/${serviceName}/${service.name.toUpperCase()}_PASSWORD`,
      Description: `Password for ${service.name}`,
      GenerateSecretString: {
        SecretStringTemplate: JSON.stringify({ username: 'postgres' }),
        GenerateStringKey: 'password',
        PasswordLength: 32,
        ExcludeCharacters: '"@/:?#[]!$&\'()*+,;=\\% ',
      },
      Tags: getStandardTags(tenantId, serviceName),
    },
  };

  // Create RDS instance
  resources[dbName] = {
    Type: 'AWS::RDS::DBInstance',
    Properties: {
      DBInstanceIdentifier: `prod-${serviceName}-${service.name}`,
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
      Tags: getStandardTags(tenantId, serviceName),
    },
  };
}

/**
 * Build Serverless ElastiCache with Valkey engine
 * Serverless ElastiCache automatically scales and manages the cache infrastructure
 */
function buildServerlessElastiCache(
  serviceName: string,
  service: any,
  tenantId: string,
  resources: any
): void {
  const cacheName = service.name.replace(/[^a-zA-Z0-9]/g, '');

  // Build cache usage limits (default to sensible values if not specified)
  const cacheUsageLimits: any = {
    DataStorage: {
      Maximum: service.cacheUsageLimits?.dataStorage?.maximum || 10,
      Unit: 'GB',
    },
    ECPUPerSecond: {
      Maximum: service.cacheUsageLimits?.ecpuPerSecond?.maximum || 5000,
    },
  };

  // Create Serverless ElastiCache with Valkey engine
  // Note: Serverless ElastiCache uses SubnetIds directly (no subnet group needed)
  resources[cacheName] = {
    Type: 'AWS::ElastiCache::ServerlessCache',
    Properties: {
      ServerlessCacheName: `prod-${serviceName}-${service.name}`,
      Engine: 'valkey',
      MajorEngineVersion: service.majorEngineVersion || '7',
      Description: `Serverless cache for ${serviceName}`,
      CacheUsageLimits: cacheUsageLimits,
      DailySnapshotTime: service.dailySnapshotTime || '03:00',
      SubnetIds: [{ Ref: 'PrivateSubnetAZ1' }, { Ref: 'PrivateSubnetAZ2' }],
      SecurityGroupIds: [{ Ref: 'BackingServiceSecurityGroup' }],
      Tags: getStandardTags(tenantId, serviceName),
    },
    DependsOn: ['PrivateSubnetAZ1', 'PrivateSubnetAZ2', 'BackingServiceSecurityGroup'],
  };
}
