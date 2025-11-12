// Backing services (RDS, ElastiCache) for AWS deployments

import type { DeploymentSpec } from './types.ts';

/**
 * Build backing service resources (RDS databases, ElastiCache clusters)
 * Creates password secrets, database instances, and cache clusters as specified
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
    } else if (service.type === 'elasticache') {
      buildElastiCacheCluster(spec.serviceName, service, tenantId, resources);
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
      Tags: [
        { Key: 'service', Value: serviceName },
        { Key: 'managed-by', Value: 'prod' },
        { Key: 'db-service', Value: service.name },
      ],
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
      Tags: [
        { Key: 'tenant', Value: tenantId },
        { Key: 'service', Value: serviceName },
      ],
    },
  };
}

/**
 * Build ElastiCache Redis cluster with subnet group
 */
function buildElastiCacheCluster(
  serviceName: string,
  service: any,
  tenantId: string,
  resources: any
): void {
  const cacheName = service.name.replace(/[^a-zA-Z0-9]/g, '');
  
  // Create subnet group for ElastiCache
  resources[`${cacheName}SubnetGroup`] = {
    Type: 'AWS::ElastiCache::SubnetGroup',
    Properties: {
      Description: 'Subnet group for ElastiCache',
      SubnetIds: [{ Ref: 'PrivateSubnetAZ1' }, { Ref: 'PrivateSubnetAZ2' }],
    },
  };

  // Create ElastiCache cluster
  resources[cacheName] = {
    Type: 'AWS::ElastiCache::CacheCluster',
    Properties: {
      ClusterName: `prod-${serviceName}-${service.name}`,
      Engine: 'redis',
      CacheNodeType: service.nodeType || 'cache.t3.micro',
      NumCacheNodes: service.numCacheNodes || 1,
      CacheSubnetGroupName: { Ref: `${cacheName}SubnetGroup` },
      VpcSecurityGroupIds: [{ Ref: 'BackingServiceSecurityGroup' }],
      Tags: [
        { Key: 'tenant', Value: tenantId },
        { Key: 'service', Value: serviceName },
      ],
    },
  };
}
