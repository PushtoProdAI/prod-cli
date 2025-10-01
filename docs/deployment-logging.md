# Deployment Operations Logging

This document describes the deployment operations logging system implemented to track all deployment lifecycle events.

## Overview

The deployment logging system provides:

1. **Deployment Lifecycle Tracking** - Track deployment start, success, failure, and cancellation
2. **Resource Information** - Platform, language, service type, and provider tracking
3. **Performance Metrics** - Duration tracking and error logging
4. **Metadata Storage** - Flexible metadata storage for additional context

## Database Schema

### Deployment Operations Table

```sql
CREATE TABLE audit.deployment_operations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID REFERENCES auth.users(id) ON DELETE SET NULL,
    operation_type TEXT NOT NULL, -- 'deploy', 'rollback', 'scale', 'delete', etc.
    resource_type TEXT NOT NULL, -- 'stack', 'service', 'container', etc.
    resource_id TEXT NOT NULL,
    resource_name TEXT,
    status TEXT NOT NULL, -- 'started', 'success', 'failed', 'cancelled'
    platform TEXT,
    language TEXT,
    service_type TEXT,
    service_provider TEXT,
    deployment_config JSONB,
    error_message TEXT,
    started_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    completed_at TIMESTAMP WITH TIME ZONE,
    duration_seconds INTEGER,
    ip_address INET,
    user_agent TEXT,
    metadata JSONB,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);
```

### Key Functions

- `audit.log_deployment_operation()` - Log deployment events
- `audit.update_deployment_operation()` - Update deployment status

## Usage Examples

### 1. Log Deployment Start

```typescript
const operationId = await logDeploymentOperation(
  supabase,
  'deploy',           // operation_type
  'stack',           // resource_type
  'stack-123',       // resource_id
  'my-app',          // resource_name (optional)
  'started',         // status
  'aws',             // platform
  'nodejs',          // language
  'database',        // service_type
  'postgres',        // service_provider
  undefined,          // error_message
  {                 // metadata
    region: 'us-east-1',
    instance_type: 't3.medium'
  }
)
```

### 2. Update Deployment Status

```typescript
// On success
await updateDeploymentOperation(
  supabase,
  operationId,
  'success',
  undefined, // error_message
  {         // metadata
    final_status: 'deployed',
    resources_created: ['ec2-instance', 'rds-database']
  }
)

// On failure
await updateDeploymentOperation(
  supabase,
  operationId,
  'failed',
  'Build timeout after 10 minutes',
  {         // metadata
    error_code: 'BUILD_TIMEOUT',
    retry_count: 3
  }
)
```

### 3. Query Deployment Operations

```sql
-- Get recent deployments
SELECT * FROM audit.deployment_operations 
WHERE created_at > NOW() - INTERVAL '1 day'
ORDER BY created_at DESC;

-- Get failed deployments
SELECT * FROM audit.deployment_operations 
WHERE status = 'failed'
ORDER BY created_at DESC;

-- Get deployments by platform
SELECT * FROM audit.deployment_operations 
WHERE platform = 'aws'
ORDER BY created_at DESC;

-- Get deployment statistics
SELECT 
  platform,
  language,
  status,
  COUNT(*) as count,
  AVG(duration_seconds) as avg_duration
FROM audit.deployment_operations 
WHERE created_at > NOW() - INTERVAL '7 days'
GROUP BY platform, language, status;
```

## Integration with Existing Functions

The `record-stack` function has been updated to include deployment logging:

1. **Start Logging**: When a stack usage recording begins
2. **Success Logging**: When usage stats are successfully updated
3. **Failure Logging**: When errors occur during the process

## Monitoring and Alerts

Set up monitoring for:

1. **Failed Deployments**: Alert on deployment failures
2. **Long-Running Deployments**: Alert on deployments exceeding expected duration
3. **Deployment Volume**: Monitor deployment frequency and patterns
4. **Error Patterns**: Track common failure reasons

## Security Considerations

1. **Access Control**: All deployment logging functions require service role access
2. **Data Privacy**: Sensitive information should be excluded from metadata
3. **Retention**: Consider implementing log retention policies for large deployments
4. **Audit Trail**: Deployment logs provide an immutable audit trail

## Performance Considerations

1. **Indexing**: Optimized indexes for common queries (user_id, status, created_at)
2. **Async Logging**: Deployment logging is non-blocking to avoid impacting deployment performance
3. **Metadata Size**: Keep metadata reasonable to avoid storage bloat
4. **Cleanup**: Consider implementing cleanup for old deployment logs

## Troubleshooting

### Common Issues

1. **Permission Errors**: Ensure service role has access to audit schema
2. **Missing Operations**: Check if logging is properly integrated in deployment flows
3. **Performance Impact**: Monitor if logging is affecting deployment performance
4. **Storage Growth**: Monitor audit table size and implement retention if needed

### Debug Queries

```sql
-- Check recent deployment operations
SELECT * FROM audit.deployment_operations 
WHERE created_at > NOW() - INTERVAL '1 hour'
ORDER BY created_at DESC;

-- Check for failed operations
SELECT * FROM audit.deployment_operations 
WHERE status = 'failed'
AND created_at > NOW() - INTERVAL '1 day'
ORDER BY created_at DESC;

-- Check operation durations
SELECT 
  operation_type,
  AVG(duration_seconds) as avg_duration,
  MAX(duration_seconds) as max_duration,
  MIN(duration_seconds) as min_duration
FROM audit.deployment_operations 
WHERE completed_at IS NOT NULL
GROUP BY operation_type;
```
