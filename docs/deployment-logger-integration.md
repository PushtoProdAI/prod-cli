# Deployment Logger Integration Guide

This guide shows how to integrate the `deployment-logger` Edge Function with the CLI for comprehensive deployment operations logging.

## Overview

The `deployment-logger` function provides:
- **Deployment Lifecycle Tracking** - Start, success, failure, cancellation
- **Resource Information** - Platform, language, service details
- **Performance Metrics** - Duration tracking and error logging
- **Metadata Storage** - Flexible context storage

## API Reference

### Endpoint
```
POST /functions/v1/deployment-logger
```

### Authentication
Requires JWT token in Authorization header:
```
Authorization: Bearer <jwt_token>
```

## Usage Examples

### 1. Log Deployment Start

```typescript
// Log deployment start
const response = await fetch('/functions/v1/deployment-logger', {
  method: 'POST',
  headers: {
    'Authorization': `Bearer ${authToken}`,
    'Content-Type': 'application/json'
  },
  body: JSON.stringify({
    action: 'log_deployment',
    data: {
      user_id: 'user-uuid',
      operation_type: 'deploy',
      resource_type: 'app',
      resource_id: 'app-123',
      resource_name: 'my-awesome-app',
      status: 'started',
      platform: 'flyio',
      language: 'nodejs',
      service_type: 'database',
      service_provider: 'postgres',
      deployment_config: {
        region: 'us-east-1',
        instance_type: 't3.medium',
        environment: 'production'
      },
      metadata: {
        source: '/path/to/project',
        build_command: 'npm run build',
        start_command: 'npm start'
      }
    }
  })
})

const result = await response.json()
const operationId = result.data // Use this to update later
```

### 2. Update Deployment Status

```typescript
// On success
await fetch('/functions/v1/deployment-logger', {
  method: 'POST',
  headers: {
    'Authorization': `Bearer ${authToken}`,
    'Content-Type': 'application/json'
  },
  body: JSON.stringify({
    action: 'update_deployment',
    data: {
      operation_id: operationId,
      status: 'success',
      metadata: {
        url: 'https://my-app.fly.dev',
        resources_created: ['app', 'database', 'redis'],
        deployment_time: '2m 30s'
      }
    }
  })
})

// On failure
await fetch('/functions/v1/deployment-logger', {
  method: 'POST',
  headers: {
    'Authorization': `Bearer ${authToken}`,
    'Content-Type': 'application/json'
  },
  body: JSON.stringify({
    action: 'update_deployment',
    data: {
      operation_id: operationId,
      status: 'failed',
      error_message: 'Build timeout after 10 minutes',
      metadata: {
        error_code: 'BUILD_TIMEOUT',
        retry_count: 3,
        last_attempt: new Date().toISOString()
      }
    }
  })
})
```

## CLI Integration Points

### 1. Workflow Integration

Add to `cli/internal/agent/workflow.go`:

```go
// Log deployment start
func (w *Workflows) logDeploymentStart(ctx workflow.Context, platform string, spec analyzer.ProjectSpec) (string, error) {
    operationId, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentLogDeploymentStart, platform, spec).Get(ctx)
    if err != nil {
        slog.Error("Failed to log deployment start", "error", err)
        return "", err
    }
    return operationId, nil
}

// Update deployment status
func (w *Workflows) updateDeploymentStatus(ctx workflow.Context, operationId string, status string, metadata map[string]any) error {
    return workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, status, metadata).Get(ctx)
}
```

### 2. Activity Integration

Add to `cli/internal/agent/activities.go`:

```go
const (
    AgentLogDeploymentStart     = "agent.logDeploymentStart"
    AgentUpdateDeploymentStatus = "agent.updateDeploymentStatus"
)

// Add to activity registry
{Name: AgentLogDeploymentStart, ActFunc: a.logDeploymentStart},
{Name: AgentUpdateDeploymentStatus, ActFunc: a.updateDeploymentStatus},
```

### 3. Activity Implementation

Add to `cli/internal/agent/planning.go`:

```go
func (a *Activities) logDeploymentStart(ctx context.Context, platform string, spec analyzer.ProjectSpec) (string, error) {
    session := CtxSession(ctx)
    if session == nil {
        return "", workflow.NewPermanentError(errors.New("no session found in context"))
    }

    data := map[string]any{
        "user_id": session.UserID,
        "operation_type": "deploy",
        "resource_type": "app",
        "resource_id": fmt.Sprintf("%s-%s", platform, spec.Name),
        "resource_name": spec.Name,
        "status": "started",
        "platform": platform,
        "language": spec.Language,
        "deployment_config": map[string]any{
            "source": spec.Source,
            "build_command": spec.BuildCommand,
            "start_command": spec.StartCommand,
        },
        "metadata": map[string]any{
            "service_requirements": spec.ServiceRequirements,
            "env_vars_count": len(spec.EnvVars),
        },
    }

    operationId, err := a.callDeploymentLogger(ctx, session.AccessToken, "log_deployment", data)
    if err != nil {
        return "", errors.Errorf("failed to log deployment start: %w", err)
    }

    return operationId, nil
}

func (a *Activities) updateDeploymentStatus(ctx context.Context, operationId string, status string, metadata map[string]any) error {
    session := CtxSession(ctx)
    if session == nil {
        return workflow.NewPermanentError(errors.New("no session found in context"))
    }

    data := map[string]any{
        "operation_id": operationId,
        "status": status,
        "metadata": metadata,
    }

    _, err := a.callDeploymentLogger(ctx, session.AccessToken, "update_deployment", data)
    if err != nil {
        return errors.Errorf("failed to update deployment status: %w", err)
    }

    return nil
}

func (a *Activities) callDeploymentLogger(ctx context.Context, authToken string, action string, data map[string]any) (string, error) {
    payload := map[string]any{
        "action": action,
        "data": data,
    }

    jsonData, err := json.Marshal(payload)
    if err != nil {
        return "", errors.Errorf("failed to marshal deployment logger data: %w", err)
    }

    url := fmt.Sprintf("%s/deployment-logger", getBaseURL())
    req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
    if err != nil {
        return "", errors.Errorf("failed to create request: %w", err)
    }

    req.Header.Set("Content-Type", "application/json")
    if authToken != "" {
        req.Header.Set("Authorization", "Bearer "+authToken)
    }

    resp, err := a.httpClient.Do(req)
    if err != nil {
        return "", errors.Errorf("failed to send request: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return "", errors.Errorf("deployment logger request failed with status: %d", resp.StatusCode)
    }

    var result struct {
        Success bool   `json:"success"`
        Data    string `json:"data"`
    }

    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return "", errors.Errorf("failed to decode response: %w", err)
    }

    if !result.Success {
        return "", errors.New("deployment logger returned error")
    }

    return result.Data, nil
}
```

### 4. Platform-Specific Integration

#### FlyIO Deployment
```go
func (w *Workflows) deployFly(ctx workflow.Context, input DeployPlan) (deployResult, error) {
    // Log deployment start
    operationId, err := w.logDeploymentStart(ctx, "flyio", input.Spec)
    if err != nil {
        slog.Error("Failed to log deployment start", "error", err)
    }

    // ... existing deployment logic ...

    if err != nil {
        // Log deployment failure
        if operationId != "" {
            w.updateDeploymentStatus(ctx, operationId, "failed", map[string]any{
                "error": err.Error(),
                "platform": "flyio",
            })
        }
        return deployResult{Error: deployError{Summary: err.Error()}}, nil
    }

    // Log deployment success
    if operationId != "" {
        w.updateDeploymentStatus(ctx, operationId, "success", map[string]any{
            "url": fullUrl,
            "resources_created": createdResources,
            "platform": "flyio",
        })
    }

    return deployResult{Url: fullUrl}, nil
}
```

#### Netlify Deployment
```go
func (w *Workflows) deployNetlify(ctx workflow.Context, input DeployPlan) (deployResult, error) {
    // Log deployment start
    operationId, err := w.logDeploymentStart(ctx, "netlify", input.Spec)
    if err != nil {
        slog.Error("Failed to log deployment start", "error", err)
    }

    // ... existing deployment logic ...

    if err != nil {
        // Log deployment failure
        if operationId != "" {
            w.updateDeploymentStatus(ctx, operationId, "failed", map[string]any{
                "error": err.Error(),
                "platform": "netlify",
            })
        }
        return deployResult{Error: deployError{Summary: err.Error()}}, nil
    }

    // Log deployment success
    if operationId != "" {
        w.updateDeploymentStatus(ctx, operationId, "success", map[string]any{
            "url": deploymentURL,
            "resources_created": createdResources,
            "platform": "netlify",
        })
    }

    return deployResult{Url: deploymentURL}, nil
}
```

## Querying Deployment Logs

### SQL Queries

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
WHERE platform = 'flyio'
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

## Monitoring and Alerts

### Key Metrics to Monitor

1. **Deployment Success Rate**
   ```sql
   SELECT 
     platform,
     (COUNT(*) FILTER (WHERE status = 'success'))::float / COUNT(*) as success_rate
   FROM audit.deployment_operations 
   WHERE created_at > NOW() - INTERVAL '24 hours'
   GROUP BY platform;
   ```

2. **Average Deployment Duration**
   ```sql
   SELECT 
     platform,
     AVG(duration_seconds) as avg_duration_seconds
   FROM audit.deployment_operations 
   WHERE status = 'success' 
   AND created_at > NOW() - INTERVAL '7 days'
   GROUP BY platform;
   ```

3. **Common Failure Reasons**
   ```sql
   SELECT 
     error_message,
     COUNT(*) as failure_count
   FROM audit.deployment_operations 
   WHERE status = 'failed' 
   AND created_at > NOW() - INTERVAL '7 days'
   GROUP BY error_message
   ORDER BY failure_count DESC;
   ```

## Testing

### Test Deployment Logging

```bash
# Test deployment start
curl -X POST https://your-project.supabase.co/functions/v1/deployment-logger \
  -H "Authorization: Bearer YOUR_JWT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "action": "log_deployment",
    "data": {
      "operation_type": "deploy",
      "resource_type": "app",
      "resource_id": "test-app-123",
      "status": "started",
      "platform": "flyio",
      "language": "nodejs"
    }
  }'

# Test deployment update
curl -X POST https://your-project.supabase.co/functions/v1/deployment-logger \
  -H "Authorization: Bearer YOUR_JWT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "action": "update_deployment",
    "data": {
      "operation_id": "OPERATION_ID_FROM_PREVIOUS_RESPONSE",
      "status": "success",
      "metadata": {
        "url": "https://test-app.fly.dev"
      }
    }
  }'
```

## Best Practices

1. **Always log deployment start** before beginning deployment
2. **Update status on completion** (success or failure)
3. **Include relevant metadata** for debugging and monitoring
4. **Handle errors gracefully** - don't let logging failures break deployments
5. **Use consistent operation types** and resource types
6. **Include user context** when available
7. **Monitor the logging system itself** for failures

## Troubleshooting

### Common Issues

1. **Permission Errors**: Ensure JWT token has proper permissions
2. **Network Timeouts**: Implement retry logic for logging calls
3. **Missing Operations**: Check if logging is called at the right times
4. **Performance Impact**: Use async logging to avoid blocking deployments

### Debug Queries

```sql
-- Check recent deployment operations
SELECT * FROM audit.deployment_operations 
WHERE created_at > NOW() - INTERVAL '1 hour'
ORDER BY created_at DESC;

-- Check for operations without completion
SELECT * FROM audit.deployment_operations 
WHERE status = 'started' 
AND created_at < NOW() - INTERVAL '1 hour'
ORDER BY created_at DESC;
```
