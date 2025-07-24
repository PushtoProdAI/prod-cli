package render

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/meroxa/prod/cli/internal/deployment"
)

// CreatePostgresStepConfig holds configuration for creating a PostgreSQL service
type CreatePostgresStepConfig struct {
	// ID is the unique identifier for this step (e.g., "step-1")
	ID string
	// Description is a human-readable description of what this step does
	Description string
	// Name is the name of the PostgreSQL service to create (e.g., "myapp-postgres-1")
	Name string
	// DatabaseName is the name of the database to create within the PostgreSQL instance
	DatabaseName string
	// OwnerID is the Render workspace/owner ID where the service will be created
	OwnerID string
	// DependsOn lists the step IDs that must complete before this step runs
	DependsOn []string
}

// CreatePostgresStep handles PostgreSQL service creation
type CreatePostgresStep struct {
	BaseStep
	Name         string `json:"name"`
	DatabaseName string `json:"databaseName"`
	OwnerID      string `json:"ownerId"`
}

func NewCreatePostgresStep(config CreatePostgresStepConfig) *CreatePostgresStep {
	return &CreatePostgresStep{
		BaseStep: BaseStep{
			ID:          config.ID,
			Description: config.Description,
			DependsOn:   config.DependsOn,
		},
		Name:         config.Name,
		DatabaseName: config.DatabaseName,
		OwnerID:      config.OwnerID,
	}
}

func (s *CreatePostgresStep) Execute(ctx context.Context, client RenderClient, stepResults map[string]interface{}) (interface{}, error) {
	postgres, err := client.CreatePostgres(ctx, CreatePostgresRequest{
		Name:         s.Name,
		OwnerID:      s.OwnerID,
		DatabaseName: s.DatabaseName,
		Plan:         "starter",
		Version:      "16",
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

// CreateRedisStepConfig holds configuration for creating a Redis service
type CreateRedisStepConfig struct {
	// ID is the unique identifier for this step (e.g., "step-1")
	ID string
	// Description is a human-readable description of what this step does
	Description string
	// Name is the name of the Redis service to create (e.g., "myapp-redis-1")
	Name string
	// OwnerID is the Render workspace/owner ID where the service will be created
	OwnerID string
	// DependsOn lists the step IDs that must complete before this step runs
	DependsOn []string
}

// CreateRedisStep handles Redis service creation
type CreateRedisStep struct {
	BaseStep
	Name    string `json:"name"`
	OwnerID string `json:"ownerId"`
}

func NewCreateRedisStep(config CreateRedisStepConfig) *CreateRedisStep {
	return &CreateRedisStep{
		BaseStep: BaseStep{
			ID:          config.ID,
			Description: config.Description,
			DependsOn:   config.DependsOn,
		},
		Name:    config.Name,
		OwnerID: config.OwnerID,
	}
}

func (s *CreateRedisStep) Execute(ctx context.Context, client RenderClient, stepResults map[string]interface{}) (interface{}, error) {
	redis, err := client.CreateRedis(ctx, CreateRedisRequest{
		Name:    s.Name,
		OwnerID: s.OwnerID,
		Plan:    "starter",
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

// GetConnectionInfoStepConfig holds configuration for fetching service connection information
type GetConnectionInfoStepConfig struct {
	// ID is the unique identifier for this step (e.g., "step-1")
	ID string
	// Description is a human-readable description of what this step does
	Description string
	// ServiceType is the type of service ("postgresql" or "redis")
	ServiceType string
	// ServiceStepID is the ID of the step that created the service we're getting info for
	ServiceStepID string
	// DependsOn lists the step IDs that must complete before this step runs
	DependsOn []string
}

// GetConnectionInfoStep handles fetching service connection information
type GetConnectionInfoStep struct {
	BaseStep
	ServiceType   string `json:"serviceType"` // "postgres" or "redis"
	ServiceStepID string `json:"serviceStepId"`
}

func NewGetConnectionInfoStep(config GetConnectionInfoStepConfig) *GetConnectionInfoStep {
	return &GetConnectionInfoStep{
		BaseStep: BaseStep{
			ID:          config.ID,
			Description: config.Description,
			DependsOn:   config.DependsOn,
		},
		ServiceType:   config.ServiceType,
		ServiceStepID: config.ServiceStepID,
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

// BuildAndPushStepConfig holds configuration for building and pushing Docker images
type BuildAndPushStepConfig struct {
	// ID is the unique identifier for this step (e.g., "step-1")
	ID string
	// Description is a human-readable description of what this step does
	Description string
	// DeploymentSpec contains the deployment configuration including app name and metadata
	DeploymentSpec *deployment.DeploymentSpec
	// DockerGenerator handles Docker image building and registry operations
	DockerGenerator *deployment.DockerGenerator
	// BuildContext is the directory context for the Docker build (typically ".")
	BuildContext string
	// TenantID is used for multi-tenant Docker registry configurations
	TenantID string
	// DependsOn lists the step IDs that must complete before this step runs
	DependsOn []string
}

// BuildAndPushStep handles Docker image building and pushing to registry
type BuildAndPushStep struct {
	BaseStep
	DeploymentSpec  *deployment.DeploymentSpec
	DockerGenerator *deployment.DockerGenerator
	BuildContext    string
	TenantID        string
}

func NewBuildAndPushStep(config BuildAndPushStepConfig) *BuildAndPushStep {
	return &BuildAndPushStep{
		BaseStep: BaseStep{
			ID:          config.ID,
			Description: config.Description,
			DependsOn:   config.DependsOn,
		},
		DeploymentSpec:  config.DeploymentSpec,
		DockerGenerator: config.DockerGenerator,
		BuildContext:    config.BuildContext,
		TenantID:        config.TenantID,
	}
}

func (s *BuildAndPushStep) Execute(ctx context.Context, client RenderClient, stepResults map[string]interface{}) (interface{}, error) {
	// Build and push the Docker image
	_, _, err := s.DockerGenerator.BuildAndPush(ctx, s.DeploymentSpec, s.BuildContext, s.TenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to build and push Docker image: %w", err)
	}

	// We only care that it succeeded, return nil
	return nil, nil
}

func (s *BuildAndPushStep) Rollback(ctx context.Context, client RenderClient, stepResults map[string]interface{}) error {
	// No rollback needed for Docker build/push
	// The image will just remain in the registry unused
	return nil
}

// CreateRegistryCredentialStepConfig holds configuration for creating registry credentials
type CreateRegistryCredentialStepConfig struct {
	// ID is the unique identifier for this step (e.g., "step-1")
	ID string
	// Description is a human-readable description of what this step does
	Description string
	// Name is the name of the registry credential to create
	Name string
	// TenantID is used for multi-tenant Docker registry configurations
	TenantID string
	// OwnerID is the Render workspace/owner ID where the credential will be created
	OwnerID string
	// DependsOn lists the step IDs that must complete before this step runs
	DependsOn []string
}

// CreateRegistryCredentialStep handles creating Docker registry credentials in Render
type CreateRegistryCredentialStep struct {
	BaseStep
	Name     string `json:"name"`
	TenantID string `json:"tenantId"`
	OwnerID  string `json:"ownerId"`
}

func NewCreateRegistryCredentialStep(config CreateRegistryCredentialStepConfig) *CreateRegistryCredentialStep {
	return &CreateRegistryCredentialStep{
		BaseStep: BaseStep{
			ID:          config.ID,
			Description: config.Description,
			DependsOn:   config.DependsOn,
		},
		Name:     config.Name,
		TenantID: config.TenantID,
		OwnerID:  config.OwnerID,
	}
}

func (s *CreateRegistryCredentialStep) Execute(ctx context.Context, client RenderClient, stepResults map[string]interface{}) (interface{}, error) {
	// First, check if a registry credential with this name already exists
	existingCreds, err := client.ListRegistryCredentials(ctx, s.OwnerID)
	if err != nil {
		return nil, fmt.Errorf("failed to list existing registry credentials: %w", err)
	}

	// Look for an existing credential with the same name
	for _, cred := range existingCreds {
		if cred.Name == s.Name {
			return cred, nil
		}
	}

	// No existing credential found, create a new one

	// Get pull credentials from the Docker generator
	dockerGenerator := deployment.NewDockerGenerator()
	defer dockerGenerator.Close()

	pullCreds, err := dockerGenerator.GetPullCredentials(ctx, s.TenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to get pull credentials: %w", err)
	}

	// Create registry credential in Render
	registryCred, err := client.CreateRegistryCredential(ctx, CreateRegistryCredentialRequest{
		Name:      s.Name,
		Username:  pullCreds.Username,
		AuthToken: pullCreds.Token,
		Registry:  "AWS_ECR",
		OwnerID:   s.OwnerID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create registry credential: %w", err)
	}

	return registryCred, nil
}

func (s *CreateRegistryCredentialStep) Rollback(ctx context.Context, client RenderClient, stepResults map[string]interface{}) error {
	// TODO: Implement deletion of registry credential if needed
	return nil
}

// CreateWebServiceStepConfig holds configuration for creating a web service step
type CreateWebServiceStepConfig struct {
	// ID is the unique identifier for this step (e.g., "step-1")
	ID string
	// Description is a human-readable description of what this step does
	Description string
	// Name is the name of the web service to create (e.g., "myapp-web")
	Name string
	// Type is the service type, typically "web_service"
	Type string
	// OwnerID is the Render workspace/owner ID where the service will be created
	OwnerID string
	// BuildCommand is the command to build the application (e.g., "npm run build")
	BuildCommand string
	// StartCommand is the command to start the application (e.g., "npm start")
	StartCommand string
	// Environment is the runtime environment (e.g., "node", "python3", "docker")
	Environment string
	// Dockerfile is the path to the Dockerfile for Docker deployments
	Dockerfile string
	// DockerImageStepID is the ID of the step that built/pushed the Docker image
	DockerImageStepID string
	// RegistryCredStepID is the ID of the step that created registry credentials
	RegistryCredStepID string
	// TenantID is used for multi-tenant Docker registry configurations
	TenantID string
	// EnvVars are the environment variables to set on the service
	EnvVars map[string]string
	// ConnectionStepIDs are the IDs of steps that provide connection info (e.g., database URLs)
	ConnectionStepIDs []string
	// DependsOn lists the step IDs that must complete before this step runs
	DependsOn []string
}

// CreateWebServiceStep handles web service creation
type CreateWebServiceStep struct {
	BaseStep
	Name               string            `json:"name"`
	Type               string            `json:"type"`
	OwnerID            string            `json:"ownerId"`
	BuildCommand       string            `json:"buildCommand"`
	StartCommand       string            `json:"startCommand"`
	Environment        string            `json:"environment"`
	Dockerfile         string            `json:"dockerfile,omitempty"`
	DockerImageStepID  string            `json:"dockerImageStepId,omitempty"`  // ID of build & push step
	RegistryCredStepID string            `json:"registryCredStepId,omitempty"` // ID of registry credential step
	TenantID           string            `json:"tenantId,omitempty"`           // For Docker deployments
	EnvVars            map[string]string `json:"envVars"`
	ConnectionStepIDs  []string          `json:"connectionStepIds"` // IDs of connection info steps
}

func NewCreateWebServiceStep(config CreateWebServiceStepConfig) *CreateWebServiceStep {
	return &CreateWebServiceStep{
		BaseStep: BaseStep{
			ID:          config.ID,
			Description: config.Description,
			DependsOn:   config.DependsOn,
		},
		Name:               config.Name,
		Type:               config.Type,
		OwnerID:            config.OwnerID,
		BuildCommand:       config.BuildCommand,
		StartCommand:       config.StartCommand,
		Environment:        config.Environment,
		Dockerfile:         config.Dockerfile,
		DockerImageStepID:  config.DockerImageStepID,
		RegistryCredStepID: config.RegistryCredStepID,
		TenantID:           config.TenantID,
		EnvVars:            config.EnvVars,
		ConnectionStepIDs:  config.ConnectionStepIDs,
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

	req := CreateWebServiceRequest{
		Name:    s.Name,
		Type:    s.Type,
		OwnerID: s.OwnerID,
		EnvVars: envVarSlice,
	}

	// Check if we have a Docker image from a previous step
	if s.DockerImageStepID != "" && s.RegistryCredStepID != "" {

		// Get the registry credential from the previous step
		registryCredResult, exists := stepResults[s.RegistryCredStepID]
		if !exists {
			return nil, fmt.Errorf("registry credential step %s not found", s.RegistryCredStepID)
		}

		registryCred, ok := registryCredResult.(*RegistryCredential)
		if !ok {
			return nil, fmt.Errorf("invalid registry credential result type")
		}

		// Get pull credentials to construct the image path
		dockerGenerator := deployment.NewDockerGenerator()
		defer dockerGenerator.Close()

		pullCreds, err := dockerGenerator.GetPullCredentials(ctx, s.TenantID)
		if err != nil {
			return nil, fmt.Errorf("failed to get pull credentials: %w", err)
		}

		// Construct the Docker image path
		imagePath := fmt.Sprintf("%s/%s:latest", strings.TrimSuffix(pullCreds.URL, "/"), pullCreds.Repository)

		req.Image = &ImageDetails{
			OwnerID:              s.OwnerID,
			RegistryCredentialID: registryCred.ID,
			ImagePath:            imagePath,
		}

		envSpecificDetails := &WebServiceEnvSpecificDetails{
			RegistryCredentialID: registryCred.ID,
		}

		serviceDetails := &WebServiceDetails{
			Runtime:            "image",
			Plan:               "starter",
			EnvSpecificDetails: envSpecificDetails,
		}

		req.ServiceDetails = serviceDetails

		// Don't set build/start commands for Docker deployments
	} else {
		// Native deployment - set build and start commands
		req.BuildCommand = s.BuildCommand
		req.StartCommand = s.StartCommand
	}

	webService, err := client.CreateWebService(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to create web service: %w", err)
	}
	return webService, nil
}

func (s *CreateWebServiceStep) Rollback(ctx context.Context, client RenderClient, stepResults map[string]interface{}) error {
	// Note: Render may not support service deletion, so this might be a no-op
	return nil
}
