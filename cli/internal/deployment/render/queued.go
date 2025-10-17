package render

import (
	"context"
	"fmt"
	"io"

	"github.com/go-errors/errors"

	"github.com/meroxa/prod/cli/internal/deployment"
	"github.com/meroxa/prod/cli/internal/output"
)

type QueuedDeployment struct {
	client          RenderClient
	spec            *deployment.DeploymentSpec
	dockerGenerator *deployment.DockerGenerator
	useDockerfile   bool
	buildContext    string
	authToken       string
	writer          io.Writer
}

func NewQueuedDeployment(client RenderClient, spec *deployment.DeploymentSpec, dockerGenerator *deployment.DockerGenerator, useDockerfile bool, writer io.Writer) *QueuedDeployment {
	if writer == nil {
		writer = output.NewNoOpWriter()
	}

	buildContext := "."
	if bc, ok := spec.Metadata["buildContext"].(string); ok {
		buildContext = bc
	}
	authToken := ""
	if at, ok := spec.Metadata["authToken"].(string); ok {
		authToken = at
	}

	return &QueuedDeployment{
		client:          client,
		spec:            spec,
		dockerGenerator: dockerGenerator,
		useDockerfile:   useDockerfile,
		buildContext:    buildContext,
		authToken:       authToken,
		writer:          writer,
	}
}

func (qd *QueuedDeployment) Deploy(ctx context.Context) ([]deployment.CreatedResource, error) {
	// Step 1: Get the first workspace to use as owner
	workspaces, err := qd.client.ListWorkspaces(ctx)
	if err != nil {
		return []deployment.CreatedResource{}, errors.Errorf("failed to list workspaces: %w", err)
	}

	if len(workspaces) == 0 {
		return []deployment.CreatedResource{}, errors.Errorf("no workspaces found")
	}

	ownerID := workspaces[0].Owner.ID

	// Generate steps with the owner ID
	steps := qd.GenerateAPISteps(ownerID)

	stepExecutor := NewStepExecutor(qd.client, qd.writer)
	return stepExecutor.ExecuteSteps(ctx, steps)
}

func (qd *QueuedDeployment) GenerateAPISteps(ownerID string) []RenderAPIStep {
	var steps []RenderAPIStep
	stepCounter := 1

	var connectionStepIDs []string
	if !qd.spec.IsUpdate || len(qd.spec.ExistingDatabases) == 0 {
		backingServiceSteps, connStepIDs, nextCounter := qd.createBackingServiceSteps(ownerID, stepCounter)
		steps = append(steps, backingServiceSteps...)
		connectionStepIDs = connStepIDs
		stepCounter = nextCounter
	} else if qd.spec.IsUpdate && len(qd.spec.ExistingDatabases) > 0 {
		backingServiceSteps, connStepIDs, nextCounter := qd.createMissingBackingServiceSteps(ownerID, stepCounter)
		steps = append(steps, backingServiceSteps...)
		connectionStepIDs = connStepIDs
		stepCounter = nextCounter
	}

	deploymentConfig := qd.configureDeployment(ownerID, connectionStepIDs, &steps, &stepCounter)

	if qd.spec.IsUpdate {
		triggerDeployStep := NewTriggerDeployStep(TriggerDeployStepConfig{
			ID:                 fmt.Sprintf("step-%d", stepCounter),
			Description:        "Trigger deployment to Render",
			ServiceID:          qd.spec.ExistingProjectID,
			DockerImageStepID:  deploymentConfig.dockerImageStepID,
			RegistryCredStepID: deploymentConfig.registryCredStepID,
			OwnerID:            ownerID,
			DependsOn:          []string{deploymentConfig.dockerImageStepID, deploymentConfig.registryCredStepID},
		})
		steps = append(steps, triggerDeployStep)
	} else {
		webServiceStep := qd.createWebServiceStep(ownerID, qd.spec.EnvVars, connectionStepIDs, deploymentConfig, stepCounter)
		steps = append(steps, webServiceStep)
	}

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

// createMissingBackingServiceSteps creates only the database services that don't already exist
func (qd *QueuedDeployment) createMissingBackingServiceSteps(ownerID string, startCounter int) ([]RenderAPIStep, []string, int) {
	var steps []RenderAPIStep
	var connectionStepIDs []string
	stepCounter := startCounter
	serviceCount := make(map[string]int)

	for provider := range qd.spec.ServiceCounts() {
		// Skip if this database already exists
		exists := false
		for _, existingDB := range qd.spec.ExistingDatabases {
			if existingDB == provider {
				exists = true
				break
			}
		}
		if exists {
			continue
		}

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
		AuthToken:       qd.authToken,
		DependsOn:       []string{},
	})
	steps = append(steps, dockerStep)

	// Registry credential step
	regCredStepID := fmt.Sprintf("step-%d", startCounter+1)
	registryCredStep := NewCreateRegistryCredentialStep(CreateRegistryCredentialStepConfig{
		ID:          regCredStepID,
		Description: "Create or find Docker registry credential in Render",
		Name:        fmt.Sprintf("%s-registry-cred", qd.spec.Name),
		AuthToken:   qd.authToken,
		ProjectName: qd.spec.Name,
		OwnerID:     ownerID,
		DependsOn:   []string{dockerStepID},
	})
	steps = append(steps, registryCredStep)

	return steps
}

// createWebServiceStep creates the final web service deployment step
func (qd *QueuedDeployment) createWebServiceStep(ownerID string, envVars []deployment.EnvVar, connectionStepIDs []string, config *deploymentConfig, stepCounter int) RenderAPIStep {
	return NewCreateWebServiceStep(CreateWebServiceStepConfig{
		ID:                 fmt.Sprintf("step-%d", stepCounter),
		Description:        "Create web service with database connection environment variables",
		Name:               fmt.Sprintf("%s-web", qd.spec.Name),
		Type:               "web_service",
		OwnerID:            ownerID,
		BuildCommand:       config.buildCommand,
		StartCommand:       config.startCommand,
		PreDeployCommand:   qd.spec.MigrationCommand,
		Environment:        config.environment,
		Dockerfile:         config.dockerfile,
		DockerImageStepID:  config.dockerImageStepID,
		RegistryCredStepID: config.registryCredStepID,
		AuthToken:          qd.authToken,
		ProjectName:        qd.spec.Name,
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

func (qd *QueuedDeployment) GetCurrentDeployment(ctx context.Context) (*deployment.DeploymentInfo, error) {
	return nil, errors.Errorf("rollback not yet implemented for Render")
}

func (qd *QueuedDeployment) GetPreviousDeployment(ctx context.Context) (*deployment.DeploymentInfo, error) {
	return nil, errors.Errorf("rollback not yet implemented for Render")
}

func (qd *QueuedDeployment) Rollback(ctx context.Context, targetDeploymentID string) error {
	return errors.Errorf("rollback not yet implemented for Render")
}
