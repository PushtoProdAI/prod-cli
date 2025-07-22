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
}

func NewQueuedDeployment(client RenderClient, spec *deployment.DeploymentSpec, dockerGenerator *deployment.DockerGenerator, useDockerfile bool) *QueuedDeployment {
	return &QueuedDeployment{
		client:          client,
		spec:            spec,
		dockerGenerator: dockerGenerator,
		useDockerfile:   useDockerfile,
	}
}

func (qd *QueuedDeployment) Deploy(ctx context.Context) error {
	// Step 1: Get the first workspace to use as owner
	workspaces, err := qd.client.ListWorkspaces(ctx)
	if err != nil {
		return fmt.Errorf("failed to list workspaces: %w", err)
	}

	if len(workspaces) == 0 {
		return fmt.Errorf("no workspaces found")
	}

	ownerID := workspaces[0].ID
	fmt.Printf("Using workspace: %s (ID: %s)\n", workspaces[0].Name, ownerID)

	// Generate steps with the owner ID
	steps := qd.generateAPISteps(ownerID)

	// Execute steps with dependency resolution
	stepExecutor := NewStepExecutor(qd.client)
	return stepExecutor.ExecuteSteps(ctx, steps)
}


func (qd *QueuedDeployment) generateAPISteps(ownerID string) []RenderAPIStep {
	var steps []RenderAPIStep
	stepCounter := 1
	serviceCount := make(map[string]int)

	// Steps N+: Create backing services and fetch their connection info
	var connectionSteps []string

	for provider := range qd.spec.ServiceCounts() {
		serviceCount[provider]++

		// Create service step
		createStepID := fmt.Sprintf("step-%d", stepCounter)
		var serviceStep RenderAPIStep

		switch provider {
		case "postgresql":
			serviceStep = NewCreatePostgresStep(
				createStepID,
				fmt.Sprintf("Create PostgreSQL database service (%s-postgres-%d)", qd.spec.Name, serviceCount[provider]),
				fmt.Sprintf("%s-postgres-%d", qd.spec.Name, serviceCount[provider]),
				fmt.Sprintf("%s_db", qd.spec.Name),
				ownerID,
				[]string{},
			)
		case "redis":
			serviceStep = NewCreateRedisStep(
				createStepID,
				fmt.Sprintf("Create Redis key-value store service (%s-redis-%d)", qd.spec.Name, serviceCount[provider]),
				fmt.Sprintf("%s-redis-%d", qd.spec.Name, serviceCount[provider]),
				ownerID,
				[]string{},
			)
		default:
			continue
		}

		steps = append(steps, serviceStep)
		stepCounter++

		// Fetch connection info step
		connectionStepID := fmt.Sprintf("step-%d", stepCounter)
		var connectionDescription string

		switch provider {
		case "postgresql":
			connectionDescription = "Retrieve PostgreSQL connection information"
		case "redis":
			connectionDescription = "Retrieve Redis connection information"
		}

		connectionStep := NewGetConnectionInfoStep(
			connectionStepID,
			connectionDescription,
			provider,
			createStepID,
			[]string{createStepID},
		)

		steps = append(steps, connectionStep)
		connectionSteps = append(connectionSteps, connectionStepID)
		stepCounter++
	}

	// Final step: Create web service with environment variables from connection strings
	var dependsOn []string
	if len(connectionSteps) > 0 {
		dependsOn = connectionSteps
	}

	// Prepare environment variables (will be resolved dynamically)
	envVars := make(map[string]string)
	for provider := range qd.spec.ServiceCounts() {
		switch provider {
		case "postgresql":
			envVars["DATABASE_URL"] = "{postgres_internal_connection_string}"
		case "redis":
			envVars["REDIS_URL"] = "{redis_internal_connection_string}"
		}
	}

	// Configure deployment method (Docker vs native)
	var buildCommand, startCommand, env, dockerfile string
	if qd.useDockerfile && qd.dockerGenerator != nil {
		// Generate Dockerfile for Docker-based deployment
		dockerArtifacts, err := qd.dockerGenerator.GenerateDockerfile(qd.spec)
		if err != nil {
			// Fall back to native deployment if Docker generation fails
			buildCommand, startCommand, env = qd.getNativeDeploymentConfig()
		} else {
			env = "docker"
			dockerfile = dockerArtifacts.Dockerfile
			buildCommand = ""
			startCommand = ""
		}
	} else {
		buildCommand, startCommand, env = qd.getNativeDeploymentConfig()
	}

	// Create web service step
	webServiceStep := NewCreateWebServiceStep(
		fmt.Sprintf("step-%d", stepCounter),
		"Create web service with database connection environment variables",
		fmt.Sprintf("%s-web", qd.spec.Name),
		"web_service",
		ownerID,
		buildCommand,
		startCommand,
		env,
		dockerfile,
		envVars,
		connectionSteps,
		dependsOn,
	)

	steps = append(steps, webServiceStep)

	return steps
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