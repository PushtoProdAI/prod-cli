package render

import (
	"context"
	"fmt"
	"time"
)

// CreateProjectStep handles project creation
type CreateProjectStep struct {
	BaseStep
	Name        string `json:"name"`
	Environment string `json:"environment"`
}

func NewCreateProjectStep(id, description, name, environment string, dependsOn []string) *CreateProjectStep {
	return &CreateProjectStep{
		BaseStep: BaseStep{
			ID:          id,
			Description: description,
			DependsOn:   dependsOn,
		},
		Name:        name,
		Environment: environment,
	}
}

func (s *CreateProjectStep) Execute(ctx context.Context, client RenderClient, stepResults map[string]interface{}) (interface{}, error) {
	project, err := client.CreateProject(ctx, CreateProjectRequest{
		Name:        s.Name,
		Environment: s.Environment,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create project: %w", err)
	}
	return project, nil
}

func (s *CreateProjectStep) Rollback(ctx context.Context, client RenderClient, stepResults map[string]interface{}) error {
	if result, exists := stepResults[s.ID]; exists {
		if project, ok := result.(*RenderProject); ok {
			return client.DeleteProject(ctx, project.ID)
		}
	}
	return nil
}

// CreatePostgresStep handles PostgreSQL service creation
type CreatePostgresStep struct {
	BaseStep
	Name         string `json:"name"`
	DatabaseName string `json:"databaseName"`
	OwnerID      string `json:"ownerId"`
}

func NewCreatePostgresStep(id, description, name, databaseName, ownerID string, dependsOn []string) *CreatePostgresStep {
	return &CreatePostgresStep{
		BaseStep: BaseStep{
			ID:          id,
			Description: description,
			DependsOn:   dependsOn,
		},
		Name:         name,
		DatabaseName: databaseName,
		OwnerID:      ownerID,
	}
}

func (s *CreatePostgresStep) Execute(ctx context.Context, client RenderClient, stepResults map[string]interface{}) (interface{}, error) {
	postgres, err := client.CreatePostgres(ctx, CreatePostgresRequest{
		Name:         s.Name,
		OwnerID:      s.OwnerID,
		DatabaseName: s.DatabaseName,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create postgres: %w", err)
	}
	return postgres, nil
}

func (s *CreatePostgresStep) Rollback(ctx context.Context, client RenderClient, stepResults map[string]interface{}) error {
	// Note: Render may not support service deletion, so this might be a no-op
	// In a real implementation, you'd implement the appropriate cleanup
	return nil
}

// CreateRedisStep handles Redis service creation
type CreateRedisStep struct {
	BaseStep
	Name    string `json:"name"`
	OwnerID string `json:"ownerId"`
}

func NewCreateRedisStep(id, description, name, ownerID string, dependsOn []string) *CreateRedisStep {
	return &CreateRedisStep{
		BaseStep: BaseStep{
			ID:          id,
			Description: description,
			DependsOn:   dependsOn,
		},
		Name:    name,
		OwnerID: ownerID,
	}
}

func (s *CreateRedisStep) Execute(ctx context.Context, client RenderClient, stepResults map[string]interface{}) (interface{}, error) {
	redis, err := client.CreateRedis(ctx, CreateRedisRequest{
		Name:    s.Name,
		OwnerID: s.OwnerID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create redis: %w", err)
	}
	return redis, nil
}

func (s *CreateRedisStep) Rollback(ctx context.Context, client RenderClient, stepResults map[string]interface{}) error {
	// Note: Render may not support service deletion, so this might be a no-op
	return nil
}

// GetConnectionInfoStep handles fetching service connection information
type GetConnectionInfoStep struct {
	BaseStep
	ServiceType   string `json:"serviceType"` // "postgres" or "redis"
	ServiceStepID string `json:"serviceStepId"`
}

func NewGetConnectionInfoStep(id, description, serviceType, serviceStepID string, dependsOn []string) *GetConnectionInfoStep {
	return &GetConnectionInfoStep{
		BaseStep: BaseStep{
			ID:          id,
			Description: description,
			DependsOn:   dependsOn,
		},
		ServiceType:   serviceType,
		ServiceStepID: serviceStepID,
	}
}

func (s *GetConnectionInfoStep) Execute(ctx context.Context, client RenderClient, stepResults map[string]interface{}) (interface{}, error) {
	// Get the service from the previous step
	serviceResult, exists := stepResults[s.ServiceStepID]
	if !exists {
		return nil, fmt.Errorf("service step %s not found", s.ServiceStepID)
	}

	service, ok := serviceResult.(*RenderService)
	if !ok {
		return nil, fmt.Errorf("invalid service result type")
	}

	const maxRetries = 10
	const retryDelay = 5 * time.Second

	// Retry fetching connection information until service is ready or timeout
	for attempt := 1; attempt <= maxRetries; attempt++ {
		var connInfo interface{}
		var err error

		switch s.ServiceType {
		case "postgresql":
			connInfo, err = client.GetPostgresConnectionInfo(ctx, service.ID)
		case "redis":
			connInfo, err = client.GetRedisConnectionInfo(ctx, service.ID)
		default:
			return nil, fmt.Errorf("unsupported service type: %s", s.ServiceType)
		}

		// If successful, return the connection info
		if err == nil && connInfo != nil {
			return connInfo, nil
		}

		// If this is the last attempt, return the error
		if attempt == maxRetries {
			return nil, fmt.Errorf("failed to get %s connection info after %d attempts: %w", s.ServiceType, maxRetries, err)
		}

		// Wait before retrying, but respect context cancellation
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(retryDelay):
			// Continue to next retry
		}
	}

	return nil, fmt.Errorf("failed to get connection info for %s service %s after %d attempts", s.ServiceType, service.ID, maxRetries)
}

func (s *GetConnectionInfoStep) Rollback(ctx context.Context, client RenderClient, stepResults map[string]interface{}) error {
	// No rollback needed for fetching connection info
	return nil
}

// CreateWebServiceStep handles web service creation
type CreateWebServiceStep struct {
	BaseStep
	Name              string            `json:"name"`
	Type              string            `json:"type"`
	OwnerID           string            `json:"ownerId"`
	BuildCommand      string            `json:"buildCommand"`
	StartCommand      string            `json:"startCommand"`
	Environment       string            `json:"environment"`
	Dockerfile        string            `json:"dockerfile,omitempty"`
	EnvVars           map[string]string `json:"envVars"`
	ConnectionStepIDs []string          `json:"connectionStepIds"` // IDs of connection info steps
}

func NewCreateWebServiceStep(id, description, name, serviceType, ownerID, buildCommand, startCommand, env, dockerfile string, envVars map[string]string, connectionStepIDs []string, dependsOn []string) *CreateWebServiceStep {
	return &CreateWebServiceStep{
		BaseStep: BaseStep{
			ID:          id,
			Description: description,
			DependsOn:   dependsOn,
		},
		Name:              name,
		Type:              serviceType,
		OwnerID:           ownerID,
		BuildCommand:      buildCommand,
		StartCommand:      startCommand,
		Environment:       env,
		Dockerfile:        dockerfile,
		EnvVars:           envVars,
		ConnectionStepIDs: connectionStepIDs,
	}
}

func (s *CreateWebServiceStep) Execute(ctx context.Context, client RenderClient, stepResults map[string]interface{}) (interface{}, error) {
	// Resolve connection strings from previous steps
	resolvedEnvVars := make(map[string]string)

	// Copy static env vars
	for k, v := range s.EnvVars {
		resolvedEnvVars[k] = v
	}

	// Resolve dynamic connection strings
	for _, stepID := range s.ConnectionStepIDs {
		if result, exists := stepResults[stepID]; exists {
			switch connInfo := result.(type) {
			case *PostgresConnectionInfo:
				resolvedEnvVars["DATABASE_URL"] = connInfo.InternalConnectionString
			case *RedisConnectionInfo:
				resolvedEnvVars["REDIS_URL"] = connInfo.InternalConnectionString
			}
		}
	}

	// Convert map to slice of env vars
	var envVarSlice []CreateServiceEnvVar
	for key, value := range resolvedEnvVars {
		envVarSlice = append(envVarSlice, CreateServiceEnvVar{
			Key:   key,
			Value: value,
		})
	}

	webService, err := client.CreateWebService(ctx, CreateWebServiceRequest{
		Name:         s.Name,
		Type:         s.Type,
		OwnerID:      s.OwnerID,
		BuildCommand: s.BuildCommand,
		StartCommand: s.StartCommand,
		EnvVars:      envVarSlice,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create web service: %w", err)
	}
	return webService, nil
}

func (s *CreateWebServiceStep) Rollback(ctx context.Context, client RenderClient, stepResults map[string]interface{}) error {
	// Note: Render may not support service deletion, so this might be a no-op
	return nil
}
