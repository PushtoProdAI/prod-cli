package aws

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/go-errors/errors"

	"github.com/pushtoprodai/prod-cli/internal/backend"
	"github.com/pushtoprodai/prod-cli/internal/deployment"
	"github.com/pushtoprodai/prod-cli/internal/output"
)

// AWSDeployment represents a deployment to AWS using App Runner, RDS, and ElastiCache
type AWSDeployment struct {
	client          AWSClient
	backendClient   *backend.Client
	spec            *deployment.DeploymentSpec
	dockerGenerator *deployment.DockerGenerator
	useDockerfile   bool
	region          string
	writer          io.Writer
}

// NewAWSDeployment creates a new AWS deployment
func NewAWSDeployment(
	client AWSClient,
	spec *deployment.DeploymentSpec,
	dockerGenerator *deployment.DockerGenerator,
	useDockerfile bool,
	region string,
	writer io.Writer,
) *AWSDeployment {
	if writer == nil {
		writer = output.NewNoOpWriter()
	}

	return &AWSDeployment{
		client:          client,
		backendClient:   backend.NewClient(),
		spec:            spec,
		dockerGenerator: dockerGenerator,
		useDockerfile:   useDockerfile,
		region:          region,
		writer:          writer,
	}
}

// Deploy executes the deployment to AWS
func (ad *AWSDeployment) Deploy(ctx context.Context) ([]deployment.CreatedResource, error) {
	slog.Info("Starting AWS deployment", "project", ad.spec.Name, "region", ad.region)

	// Generate deployment steps
	steps := ad.generateAPISteps()

	// Execute steps using step executor
	stepExecutor := NewStepExecutor(ad.client, ad.writer)
	return stepExecutor.ExecuteSteps(ctx, steps)
}

// GetPreviousDeployment retrieves information about the previous deployment
func (ad *AWSDeployment) GetPreviousDeployment(ctx context.Context) (*deployment.DeploymentInfo, error) {
	slog.Info("Getting previous AWS deployment", "project", ad.spec.Name)

	// Get auth token from metadata
	authToken := ""
	if token, ok := ad.spec.Metadata["authToken"].(string); ok {
		authToken = token
	}

	if authToken == "" {
		return nil, errors.New("auth token not available for querying deployment history")
	}

	// Query deployment history from backend (returns last N successful deployments)
	history, err := ad.backendClient.GetDeploymentHistory(ctx, authToken, ad.spec.Name, "aws", 2)
	if err != nil {
		return nil, errors.Errorf("failed to get deployment history: %w", err)
	}

	// We need at least 2 deployments to have a "previous" one
	// The first one is the current deployment, the second is the previous
	if len(history) < 2 {
		return nil, errors.New("no previous deployment found (this might be your first deployment)")
	}

	previousDeploy := history[1]

	// Extract the image URL from metadata
	imageURL, ok := previousDeploy.Metadata["image_url"].(string)
	if !ok || imageURL == "" {
		return nil, errors.New("previous deployment missing image URL in metadata")
	}

	// Extract the URL from metadata (for display purposes)
	url, _ := previousDeploy.Metadata["url"].(string)

	slog.Info("Found previous deployment",
		"imageURL", imageURL,
		"completedAt", previousDeploy.CompletedAt,
		"operationID", previousDeploy.OperationID)

	return &deployment.DeploymentInfo{
		ID:        imageURL, // Use image URL as the deployment ID for rollback
		Status:    previousDeploy.Status,
		CreatedAt: previousDeploy.CompletedAt,
		URL:       url,
	}, nil
}

// Rollback rolls back to a previous deployment
func (ad *AWSDeployment) Rollback(ctx context.Context, targetDeploymentID string) error {
	slog.Info("Rolling back AWS deployment", "project", ad.spec.Name, "targetImage", targetDeploymentID)

	// targetDeploymentID is the previous image URL from GetPreviousDeployment()

	// Get auth token from metadata
	authToken := ""
	if token, ok := ad.spec.Metadata["authToken"].(string); ok {
		authToken = token
	}

	if authToken == "" {
		return errors.New("auth token not available for rollback")
	}

	// Build deployment spec with the target (previous) image
	// Get configuration from current spec
	cpu := appRunnerCPU
	if cpuStr, ok := ad.spec.Metadata["cpu"].(string); ok && cpuStr != "" {
		cpu = cpuStr
	}

	memory := appRunnerMemory
	if memStr, ok := ad.spec.Metadata["memory"].(string); ok && memStr != "" {
		memory = memStr
	}

	port := ad.determinePort()
	if portInt, ok := ad.spec.Metadata["port"].(int); ok && portInt > 0 {
		port = portInt
	}

	// Build the deployment spec for rollback (with previous image, no migrations)
	deploymentSpec, err := BuildAWSDeploymentSpec(
		ad.spec.Name,
		targetDeploymentID, // Use the previous image URL
		cpu,
		memory,
		port,
		ad.spec.EnvVars,
		ad.spec.Services,
		"",  // Don't run migrations on rollback
		nil, // createAppRunner defaults to true
	)
	if err != nil {
		return errors.Errorf("failed to build rollback deployment spec: %w", err)
	}

	slog.Info("Initiating CloudFormation stack update for rollback",
		"service", ad.spec.Name,
		"previousImage", targetDeploymentID)

	// Deploy the stack with the previous image (this triggers an update)
	result, err := ad.backendClient.DeployAWSStack(ctx, authToken, deploymentSpec)
	if err != nil {
		return errors.Errorf("failed to initiate stack rollback: %w", err)
	}

	if result.Error != "" {
		return errors.Errorf("stack rollback initiation failed: %s", result.Error)
	}

	slog.Info("CloudFormation stack rollback initiated",
		"stackId", result.StackID,
		"stackName", result.StackName,
		"status", result.Status)

	// Note: The workflow will handle waiting for stack completion
	// We just initiate the rollback here

	slog.Info("AWS deployment rollback completed successfully",
		"service", ad.spec.Name,
		"previousImage", targetDeploymentID)

	return nil
}

// generateAPISteps generates the sequence of API calls needed for deployment
func (ad *AWSDeployment) generateAPISteps() []AWSAPIStep {
	var steps []AWSAPIStep
	stepCounter := 1

	// Step 1-2: Docker build/push and ECR setup (if using Docker)
	if ad.useDockerfile && ad.dockerGenerator != nil && deployment.IsDockerAvailable() {
		dockerSteps := ad.createDockerDeploymentSteps(stepCounter)
		steps = append(steps, dockerSteps...)
		stepCounter += len(dockerSteps)
	}

	// Step N: Create backing services (RDS, ElastiCache) if not updating or missing
	var connectionStepIDs []string
	if !ad.spec.IsUpdate || len(ad.spec.ExistingDatabases) == 0 {
		backingServiceSteps, connStepIDs, nextCounter := ad.createBackingServiceSteps(stepCounter)
		steps = append(steps, backingServiceSteps...)
		connectionStepIDs = connStepIDs
		stepCounter = nextCounter
	}

	// Step N+1: Create App Runner service
	appRunnerStep := ad.createAppRunnerServiceStep(stepCounter, connectionStepIDs)
	if appRunnerStep != nil {
		steps = append(steps, appRunnerStep)
	}

	return steps
}

// createDockerDeploymentSteps creates Docker build/push steps for AWS ECR
func (ad *AWSDeployment) createDockerDeploymentSteps(startCounter int) []AWSAPIStep {
	var steps []AWSAPIStep

	// Get auth token from metadata
	authToken := ""
	if at, ok := ad.spec.Metadata["authToken"].(string); ok {
		authToken = at
	}

	// Step 1: Create/get ECR repository in customer AWS account (via backend)
	ecrRepoStepID := fmt.Sprintf("step-%d", startCounter)
	ecrRepoStep := NewCreateECRRepositoryStep(CreateECRRepositoryStepConfig{
		ID:          ecrRepoStepID,
		Description: "Create ECR repository in your AWS account",
		ProjectName: ad.spec.Name,
		AuthToken:   authToken,
		DependsOn:   []string{},
	})
	steps = append(steps, ecrRepoStep)

	// Step 2: Build and push Docker image to customer ECR
	buildPushStepID := fmt.Sprintf("step-%d", startCounter+1)
	buildPushStep := NewBuildAndPushECRStep(BuildAndPushECRStepConfig{
		ID:              buildPushStepID,
		Description:     "Build and push Docker image to your ECR",
		DeploymentSpec:  ad.spec,
		DockerGenerator: ad.dockerGenerator,
		BuildContext:    ad.getBuildContext(),
		AuthToken:       authToken,
		DependsOn:       []string{ecrRepoStepID},
	})
	steps = append(steps, buildPushStep)

	return steps
}

// getBuildContext returns the build context from spec metadata or defaults to "."
func (ad *AWSDeployment) getBuildContext() string {
	if bc, ok := ad.spec.Metadata["buildContext"].(string); ok {
		return bc
	}
	return "."
}

func (ad *AWSDeployment) createBackingServiceSteps(startCounter int) ([]AWSAPIStep, []string, int) {
	var steps []AWSAPIStep
	var connectionStepIDs []string
	stepCounter := startCounter

	// TODO: Implement backing service creation steps
	// For each service in spec.Services:
	// - If postgresql: create RDS instance, store creds in Secrets Manager
	// - If redis: create ElastiCache cluster
	// - Return step IDs for connection string injection

	for _, service := range ad.spec.Services {
		switch service.Provider {
		case "postgresql":
			slog.Info("Would create RDS instance", "name", service.Name)
			stepCounter++
		case "redis":
			slog.Info("Would create ElastiCache cluster", "name", service.Name)
			stepCounter++
		}
	}

	return steps, connectionStepIDs, stepCounter
}

func (ad *AWSDeployment) createAppRunnerServiceStep(stepID int, connectionStepIDs []string) AWSAPIStep {
	// Find the ECR build/push step ID (should be step-2 if we have Docker steps)
	ecrStepID := "step-2" // The build and push step

	// Get auth token from spec metadata
	authToken := ""
	if token, ok := ad.spec.Metadata["authToken"].(string); ok {
		authToken = token
	}

	// Determine the port to use (respects user-defined PORT env var)
	port := ad.determinePort()

	// Create App Runner service step (which deploys via CloudFormation)
	appRunnerStep := NewCreateAppRunnerServiceStep(CreateAppRunnerServiceStepConfig{
		ID:                fmt.Sprintf("step-%d", stepID),
		Description:       "Deploy AWS infrastructure via CloudFormation",
		ServiceName:       ad.spec.Name,
		ECRStepID:         ecrStepID,
		EnvVars:           ad.spec.EnvVars,
		Services:          ad.spec.Services, // Pass backing services (databases, caches)
		ConnectionStepIDs: connectionStepIDs,
		CPU:               appRunnerCPU,
		Memory:            appRunnerMemory,
		Port:              port,
		AuthToken:         authToken,
		MigrationCommand:  ad.spec.MigrationCommand,
		IsUpdate:          ad.spec.IsUpdate,
		DependsOn:         append([]string{ecrStepID}, connectionStepIDs...),
	})

	return appRunnerStep
}

// determinePort determines the port for the application
// Priority: 1) PORT env var from spec, 2) language default, 3) 8080 fallback
func (ad *AWSDeployment) determinePort() int {
	// First check if PORT is already defined in env vars
	for _, ev := range ad.spec.EnvVars {
		if ev.Name == "PORT" && ev.Value != "" {
			var portInt int
			if _, err := fmt.Sscanf(ev.Value, "%d", &portInt); err == nil && portInt > 0 {
				slog.Info("Using PORT from environment variables", "port", portInt)
				return portInt
			}
		}
	}

	// Fall back to language-specific default
	port := ad.getPortForLanguage(ad.spec.Language)
	slog.Info("Using language-specific default port", "language", ad.spec.Language, "port", port)
	return port
}

// getPortForLanguage returns the default port for the given language
func (ad *AWSDeployment) getPortForLanguage(language string) int {
	switch language {
	case "python":
		return 8000
	case "node", "nodejs", "javascript":
		return 3000
	case "go":
		return 8080
	default:
		return 8080
	}
}

// Helper method to generate resource names
func (ad *AWSDeployment) generateResourceName(resourceType string, suffix string) string {
	baseName := ad.spec.Name
	if suffix != "" {
		return fmt.Sprintf("%s-%s-%s", baseName, resourceType, suffix)
	}
	return fmt.Sprintf("%s-%s", baseName, resourceType)
}

// Helper method to generate tags for AWS resources
func (ad *AWSDeployment) generateTags() map[string]string {
	return map[string]string{
		"ManagedBy": "Prod",
		"Project":   ad.spec.Name,
		"Region":    ad.region,
	}
}
