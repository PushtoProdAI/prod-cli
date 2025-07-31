package render

import (
	"context"
	"fmt"

	"github.com/meroxa/prod/cli/internal/deployment"
)

type QueuedDeployment struct {
	client          RenderClient
	spec            *deployment.DeploymentSpec
	dockerGenerator *deployment.DockerGenerator
	useDockerfile   bool
	buildContext    string
	tenantID        string
}

func NewQueuedDeployment(client RenderClient, spec *deployment.DeploymentSpec, dockerGenerator *deployment.DockerGenerator, useDockerfile bool) *QueuedDeployment {
	buildContext := "."
	if bc, ok := spec.Metadata["buildContext"].(string); ok {
		buildContext = bc
	}

	tenantID := ""
	if tid, ok := spec.Metadata["tenantID"].(string); ok {
		tenantID = tid
	}

	return &QueuedDeployment{
		client:          client,
		spec:            spec,
		dockerGenerator: dockerGenerator,
		useDockerfile:   useDockerfile,
		buildContext:    buildContext,
		tenantID:        tenantID,
	}
}

func (qd *QueuedDeployment) Deploy(ctx context.Context) ([]deployment.CreatedResource, error) {
	// Step 1: Get the first workspace to use as owner
	workspaces, err := qd.client.ListWorkspaces(ctx)
	if err != nil {
		return []deployment.CreatedResource{}, fmt.Errorf("failed to list workspaces: %w", err)
	}

	if len(workspaces) == 0 {
		return []deployment.CreatedResource{}, fmt.Errorf("no workspaces found")
	}

	ownerID := workspaces[0].Owner.ID

	// Generate steps with the owner ID
	steps := qd.GenerateAPISteps(ownerID)

	// Execute steps with dependency resolution
	stepExecutor := NewStepExecutor(qd.client)
	return stepExecutor.ExecuteSteps(ctx, steps)
}

func (qd *QueuedDeployment) GenerateAPISteps(ownerID string) []RenderAPIStep {
	var steps []RenderAPIStep
	stepCounter := 1

	// Create backing services and get their connection step IDs
	backingServiceSteps, connectionStepIDs, nextCounter := qd.createBackingServiceSteps(ownerID, stepCounter)
	steps = append(steps, backingServiceSteps...)
	stepCounter = nextCounter

	// Prepare environment variables for services
	envVars := qd.prepareServiceEnvVars()

	// Configure deployment (Docker or native)
	deploymentConfig := qd.configureDeployment(ownerID, connectionStepIDs, &steps, &stepCounter)

	// Create web service step
	webServiceStep := qd.createWebServiceStep(ownerID, envVars, connectionStepIDs, deploymentConfig, stepCounter)
	steps = append(steps, webServiceStep)

	return steps
}

// backingServiceResult contains the steps and metadata for backing service creation
type backingServiceResult struct {
	steps             []RenderAPIStep
	connectionStepIDs []string
	nextCounter       int
}

// createBackingServiceSteps creates database and cache service steps
func (qd *QueuedDeployment) createBackingServiceSteps(ownerID string, startCounter int) ([]RenderAPIStep, []string, int) {
	var steps []RenderAPIStep
	var connectionStepIDs []string
	stepCounter := startCounter
	serviceCount := make(map[string]int)

	for provider := range qd.spec.ServiceCounts() {
		serviceCount[provider]++

		// Create the service
		createStepID := fmt.Sprintf("step-%d", stepCounter)
		serviceStep := qd.createServiceStep(provider, createStepID, ownerID, serviceCount[provider])
		if serviceStep == nil {
			continue // Skip unsupported service types
		}

		steps = append(steps, serviceStep)
		stepCounter++

		// Create connection info retrieval step
		connectionStepID := fmt.Sprintf("step-%d", stepCounter)
		connectionStep := qd.createConnectionInfoStep(provider, connectionStepID, createStepID)

		steps = append(steps, connectionStep)
		connectionStepIDs = append(connectionStepIDs, connectionStepID)
		stepCounter++
	}

	return steps, connectionStepIDs, stepCounter
}

// createServiceStep creates a step for a specific service type
func (qd *QueuedDeployment) createServiceStep(provider, stepID, ownerID string, count int) RenderAPIStep {
	switch provider {
	case "postgresql":
		return NewCreatePostgresStep(CreatePostgresStepConfig{
			ID:           stepID,
			Description:  fmt.Sprintf("Create PostgreSQL database service (%s-postgres-%d)", qd.spec.Name, count),
			Name:         fmt.Sprintf("%s-postgres-%d", qd.spec.Name, count),
			DatabaseName: fmt.Sprintf("%s_db", qd.spec.Name),
			OwnerID:      ownerID,
			DependsOn:    []string{},
		})
	case "redis":
		return NewCreateRedisStep(CreateRedisStepConfig{
			ID:          stepID,
			Description: fmt.Sprintf("Create Redis key-value store service (%s-redis-%d)", qd.spec.Name, count),
			Name:        fmt.Sprintf("%s-redis-%d", qd.spec.Name, count),
			OwnerID:     ownerID,
			DependsOn:   []string{},
		})
	default:
		return nil
	}
}

// createConnectionInfoStep creates a step to fetch connection information
func (qd *QueuedDeployment) createConnectionInfoStep(provider, stepID, serviceStepID string) RenderAPIStep {
	var description string
	switch provider {
	case "postgresql":
		description = "Retrieve PostgreSQL connection information"
	case "redis":
		description = "Retrieve Redis connection information"
	default:
		description = fmt.Sprintf("Retrieve %s connection information", provider)
	}

	return NewGetConnectionInfoStep(GetConnectionInfoStepConfig{
		ID:            stepID,
		Description:   description,
		ServiceType:   provider,
		ServiceStepID: serviceStepID,
		DependsOn:     []string{serviceStepID},
	})
}

// prepareServiceEnvVars creates environment variable placeholders for services
// These placeholders will be replaced with actual connection strings during execution
func (qd *QueuedDeployment) prepareServiceEnvVars() map[string]string {
	envVars := make(map[string]string)
	// Note: These are placeholder values that will be replaced by actual connection strings
	// from the connection info steps during CreateWebServiceStep execution
	for provider := range qd.spec.ServiceCounts() {
		switch provider {
		case "postgresql":
			envVars["DATABASE_URL"] = "" // Will be replaced with actual connection string
		case "redis":
			envVars["REDIS_URL"] = "" // Will be replaced with actual connection string
		}
	}
	return envVars
}

// deploymentConfig holds the configuration for web service deployment
type deploymentConfig struct {
	buildCommand       string
	startCommand       string
	environment        string
	dockerfile         string
	dockerImageStepID  string
	registryCredStepID string
	dependencies       []string
}

// configureDeployment sets up Docker or native deployment configuration
func (qd *QueuedDeployment) configureDeployment(ownerID string, connectionStepIDs []string, steps *[]RenderAPIStep, stepCounter *int) *deploymentConfig {
	config := &deploymentConfig{}

	if qd.useDockerfile && qd.dockerGenerator != nil && deployment.IsDockerAvailable() {
		// Docker deployment configuration
		dockerSteps := qd.createDockerDeploymentSteps(ownerID, *stepCounter)
		*steps = append(*steps, dockerSteps...)
		*stepCounter += len(dockerSteps)

		// Extract step IDs from docker steps
		if len(dockerSteps) >= 2 {
			config.dockerImageStepID = dockerSteps[0].GetID()
			config.registryCredStepID = dockerSteps[1].GetID()
		}

		config.environment = "docker"
		config.dependencies = append(connectionStepIDs, config.dockerImageStepID, config.registryCredStepID)
	} else {
		// Native deployment configuration
		config.buildCommand, config.startCommand, config.environment = qd.getNativeDeploymentConfig()
		config.dependencies = connectionStepIDs
	}

	return config
}

// createDockerDeploymentSteps creates Docker build/push and registry credential steps
func (qd *QueuedDeployment) createDockerDeploymentSteps(ownerID string, startCounter int) []RenderAPIStep {
	var steps []RenderAPIStep

	// Docker build and push step
	dockerStepID := fmt.Sprintf("step-%d", startCounter)
	dockerStep := NewBuildAndPushStep(BuildAndPushStepConfig{
		ID:              dockerStepID,
		Description:     "Build and push Docker image to registry",
		DeploymentSpec:  qd.spec,
		DockerGenerator: qd.dockerGenerator,
		BuildContext:    qd.buildContext,
		TenantID:        qd.tenantID,
		DependsOn:       []string{},
	})
	steps = append(steps, dockerStep)

	// Registry credential step
	regCredStepID := fmt.Sprintf("step-%d", startCounter+1)
	registryCredStep := NewCreateRegistryCredentialStep(CreateRegistryCredentialStepConfig{
		ID:          regCredStepID,
		Description: "Create or find Docker registry credential in Render",
		Name:        fmt.Sprintf("%s-registry-cred", qd.spec.Name),
		TenantID:    qd.tenantID,
		OwnerID:     ownerID,
		DependsOn:   []string{dockerStepID},
	})
	steps = append(steps, registryCredStep)

	return steps
}

// createWebServiceStep creates the final web service deployment step
func (qd *QueuedDeployment) createWebServiceStep(ownerID string, envVars map[string]string, connectionStepIDs []string, config *deploymentConfig, stepCounter int) RenderAPIStep {
	return NewCreateWebServiceStep(CreateWebServiceStepConfig{
		ID:                 fmt.Sprintf("step-%d", stepCounter),
		Description:        "Create web service with database connection environment variables",
		Name:               fmt.Sprintf("%s-web", qd.spec.Name),
		Type:               "web_service",
		OwnerID:            ownerID,
		BuildCommand:       config.buildCommand,
		StartCommand:       config.startCommand,
		Environment:        config.environment,
		Dockerfile:         config.dockerfile,
		DockerImageStepID:  config.dockerImageStepID,
		RegistryCredStepID: config.registryCredStepID,
		TenantID:           qd.tenantID,
		EnvVars:            envVars,
		ConnectionStepIDs:  connectionStepIDs,
		DependsOn:          config.dependencies,
	})
}

func (qd *QueuedDeployment) getNativeDeploymentConfig() (buildCommand, startCommand, env string) {
	// Use configurable build/start commands or defaults
	buildCommand = "npm run build"
	startCommand = "npm start"
	if qd.spec.BuildCommand != "" {
		buildCommand = qd.spec.BuildCommand
	}
	if qd.spec.StartCommand != "" {
		startCommand = qd.spec.StartCommand
	}

	// Set environment based on language
	switch qd.spec.Language {
	case "node", "nodejs", "javascript":
		env = "node"
	case "python":
		env = "python3"
	case "go", "golang":
		env = "go"
	default:
		env = "docker" // Default to docker for unsupported languages
	}

	return buildCommand, startCommand, env
}
