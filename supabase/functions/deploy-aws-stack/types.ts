// Type definitions for AWS deployment

// Backing service type constants
export const BACKING_SERVICE_TYPE_RDS = 'rds' as const;
export const BACKING_SERVICE_TYPE_SERVERLESS_CACHE = 'serverless-cache' as const;

export interface EnvVar {
  name: string;
  value?: string;
  role?: string;    // "full_uri", "hostname", "port", "username", "password", "database_name", etc.
  service?: string; // "postgresql", "redis", etc.
  sensitive?: boolean; // true if variable contains sensitive data (API keys, passwords, etc.)
  sensitivityReason?: string; // explanation for why variable is sensitive
}

export interface DeploymentSpec {
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

export interface BackingService {
  type: typeof BACKING_SERVICE_TYPE_RDS | typeof BACKING_SERVICE_TYPE_SERVERLESS_CACHE;
  name: string;
  engine?: string;
  instanceClass?: string;
  allocatedStorage?: number;
  // Serverless cache specific options
  cacheUsageLimits?: {
    dataStorage?: {
      maximum?: number;
      unit?: 'GB';
    };
    ecpuPerSecond?: {
      maximum?: number;
    };
  };
  dailySnapshotTime?: string;
  majorEngineVersion?: string;
}

export interface DeploymentResult {
  stackId: string;
  stackName: string;
  status: string;
  outputs?: Record<string, string>;
  error?: string;
}
