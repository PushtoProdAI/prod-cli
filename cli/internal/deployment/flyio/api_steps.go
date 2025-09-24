package flyio

import (
	"context"
	"time"

	"github.com/go-errors/errors"

	"github.com/meroxa/prod/cli/internal/deployment"
)

// CreateFlyioAppStep creates a new Fly.io app
type CreateFlyioAppStep struct {
	BaseStep
	appName string
	region  string
}

func (c *CreateFlyioAppStep) Execute(ctx context.Context, client FlyioClient, stepResults map[string]interface{}) (interface{}, error) {
	app, err := client.CreateApp(ctx, CreateAppRequest{
		Name:    c.appName,
		Region:  c.region,
		OrgSlug: defaultOrg,
	})
	if err != nil {
		return nil, err
	}

	// Return as CreatedResource for consistency
	return deployment.CreatedResource{
		ID:   app.ID,
		Type: "app",
		Name: app.Name,
	}, nil
}

func (c *CreateFlyioAppStep) Rollback(ctx context.Context, client FlyioClient, stepResults map[string]interface{}) error {
	// Get the app ID from step results
	if appResult, ok := stepResults[c.GetID()]; ok {
		if resource, ok := appResult.(deployment.CreatedResource); ok {
			return client.DestroyApp(ctx, resource.ID)
		}
	}
	return errors.Errorf("could not find app ID for rollback")
}

// DeployFlyioConfigStep deploys configuration to a Fly.io app
type DeployFlyioConfigStep struct {
	BaseStep
	appName string // Changed from appID to appName since flyctl uses names
	config  *FlyioConfig
}

func (d *DeployFlyioConfigStep) Execute(ctx context.Context, client FlyioClient, stepResults map[string]interface{}) (interface{}, error) {
	// Use the app name directly (no template resolution needed)
	err := client.DeployApp(ctx, d.appName, d.config)
	if err != nil {
		return nil, err
	}

	return nil, nil
}

func (d *DeployFlyioConfigStep) Rollback(ctx context.Context, client FlyioClient, stepResults map[string]interface{}) error {
	// For config deployment, rollback might involve redeploying a previous config
	// For now, we'll just log that rollback is not implemented
	return errors.Errorf("config rollback not implemented")
}

// CreateFlyioServiceStep creates a backing service (database, volume, etc.)
type CreateFlyioServiceStep struct {
	BaseStep
	serviceType string
	name        string
	region      string
	size        int
}

func (c *CreateFlyioServiceStep) Execute(ctx context.Context, client FlyioClient, stepResults map[string]interface{}) (interface{}, error) {
	switch c.serviceType {
	case "postgres":
		cluster, err := client.CreatePostgres(ctx, CreatePostgresRequest{
			Name:       c.name,
			Region:     c.region,
			VolumeSize: c.size,
			Plan:       "basic", // Default to basic plan
		})
		if err != nil {
			return nil, err
		}

		// No need to wait - CreatePostgres already waits for provisioning
		return deployment.CreatedResource{
			ID:   cluster.ID,
			Type: "postgres_cluster",
			Name: cluster.Name,
			Metadata: map[string]interface{}{
				"cluster_id":        cluster.ID,
				"connection_string": cluster.ConnectionString,
			},
		}, nil

	case "redis":
		redis, err := client.CreateRedis(ctx, CreateRedisRequest{
			Name:   c.name,
			Region: c.region,
		})
		if err != nil {
			return nil, err
		}

		// Wait for Redis to be ready
		if err := c.waitForServiceReady(ctx, client, c.name, "redis"); err != nil {
			return nil, errors.Errorf("redis created but failed to become ready: %w", err)
		}

		return deployment.CreatedResource{
			ID:   redis.ID,
			Type: "redis",
			Name: redis.Name,
		}, nil

	// Volume creation would go here but is handled differently in Fly.io
	// Volumes must be created after the app exists

	default:
		return nil, errors.Errorf("unsupported service type: %s", c.serviceType)
	}
}

// waitForServiceReady polls the service status until it's ready
func (c *CreateFlyioServiceStep) waitForServiceReady(ctx context.Context, client FlyioClient, serviceName string, serviceType string) error {
	// Use configuration constants from flyio.go
	timeoutCtx, cancel := context.WithTimeout(ctx, serviceReadyTimeout)
	defer cancel()

	ticker := time.NewTicker(serviceReadyInterval)
	defer ticker.Stop()

	for {
		select {
		case <-timeoutCtx.Done():
			return errors.Errorf("%s service %s failed to become ready within %v", serviceType, serviceName, serviceReadyTimeout)
		case <-ticker.C:
			// Check service status based on type
			ready := false
			var err error

			switch serviceType {
			case "postgres":
				// Check if PostgreSQL is ready by trying to get connection info
				_, err = client.GetPostgresConnectionInfo(ctx, serviceName)
				ready = err == nil
			case "redis":
				// Check if Redis is ready by trying to get connection info
				_, err = client.GetRedisConnectionInfo(ctx, serviceName)
				ready = err == nil
			}

			if ready {
				return nil
			}
		}
	}
}

func (c *CreateFlyioServiceStep) Rollback(ctx context.Context, client FlyioClient, stepResults map[string]interface{}) error {
	// Get the service ID from step results
	if serviceResult, ok := stepResults[c.GetID()]; ok {
		if resource, ok := serviceResult.(deployment.CreatedResource); ok {
			// For now, we'll just log that service deletion is not implemented
			// In a real implementation, you would call the appropriate delete method
			return errors.Errorf("service deletion not implemented for %s", resource.Type)
		}
	}
	return errors.Errorf("could not find service ID for rollback")
}

// AttachPostgresStep attaches a PostgreSQL database to a Fly.io app
type AttachPostgresStep struct {
	BaseStep
	appName       string
	serviceStepID string // ID of the step that created the postgres cluster
	variableName  string
}

func (s *AttachPostgresStep) Execute(ctx context.Context, client FlyioClient, stepResults map[string]interface{}) (interface{}, error) {
	// Get the cluster ID from the previous step
	clusterID := ""
	if serviceResult, ok := stepResults[s.serviceStepID]; ok {
		if resource, ok := serviceResult.(deployment.CreatedResource); ok {
			if metadata, ok := resource.Metadata["cluster_id"].(string); ok {
				clusterID = metadata
			}
		}
	}

	if clusterID == "" {
		return nil, errors.Errorf("could not find cluster ID from step %s", s.serviceStepID)
	}

	// Attach using cluster ID
	err := client.AttachPostgres(ctx, AttachPostgresRequest{
		AppName:      s.appName,
		ClusterID:    clusterID,
		VariableName: s.variableName,
	})
	if err != nil {
		return nil, errors.Errorf("failed to attach postgres: %w", err)
	}

	// Return success indicator
	return map[string]string{
		"status":       "attached",
		"app":          s.appName,
		"cluster_id":   clusterID,
		"variableName": s.variableName,
	}, nil
}

func (s *AttachPostgresStep) Rollback(ctx context.Context, client FlyioClient, stepResults map[string]interface{}) error {
	// Detaching would require removing the database user and connection
	// For now, we'll log that detachment is not implemented
	return errors.Errorf("postgres detachment not implemented")
}

// AttachRedisStep attaches a Redis database to a Fly.io app
type AttachRedisStep struct {
	BaseStep
	appName      string
	redisName    string
	variableName string
}

func (s *AttachRedisStep) Execute(ctx context.Context, client FlyioClient, stepResults map[string]interface{}) (interface{}, error) {
	// Attach the Redis database to the app
	err := client.AttachRedis(ctx, AttachRedisRequest{
		AppName:      s.appName,
		RedisName:    s.redisName,
		VariableName: s.variableName,
	})
	if err != nil {
		return nil, errors.Errorf("failed to attach redis: %w", err)
	}

	// Return success indicator
	return map[string]string{
		"status":       "attached",
		"app":          s.appName,
		"redis":        s.redisName,
		"variableName": s.variableName,
	}, nil
}

func (s *AttachRedisStep) Rollback(ctx context.Context, client FlyioClient, stepResults map[string]interface{}) error {
	// Detaching would require removing the Redis connection
	// For now, we'll log that detachment is not implemented
	return errors.Errorf("redis detachment not implemented")
}
