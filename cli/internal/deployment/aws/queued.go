package aws

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/go-errors/errors"

	"github.com/meroxa/prod/cli/internal/deployment"
	"github.com/meroxa/prod/cli/internal/output"
)

// AWSDeployment represents a deployment to AWS using App Runner, RDS, and ElastiCache
type AWSDeployment struct {
	client          AWSClient
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

	// TODO: Implement by querying App Runner service by name/tags
	// This will check if an App Runner service with this name already exists

	return nil, errors.New("GetPreviousDeployment not yet implemented for AWS")
}

// Rollback rolls back to a previous deployment
func (ad *AWSDeployment) Rollback(ctx context.Context, targetDeploymentID string) error {
	slog.Info("Rolling back AWS deployment", "project", ad.spec.Name, "target", targetDeploymentID)

	// TODO: Implement rollback logic
	// For App Runner, this would involve:
	// 1. Finding the previous image tag from deployment history
	// 2. Updating the App Runner service to use that image
	// 3. Waiting for the service to stabilize

	return errors.New("Rollback not yet implemented for AWS")
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
		Description: "Create ECR repository in customer AWS account",
		ProjectName: ad.spec.Name,
		AuthToken:   authToken,
		DependsOn:   []string{},
	})
	steps = append(steps, ecrRepoStep)

	// Step 2: Build and push Docker image to customer ECR
	buildPushStepID := fmt.Sprintf("step-%d", startCounter+1)
	buildPushStep := NewBuildAndPushECRStep(BuildAndPushECRStepConfig{
		ID:              buildPushStepID,
		Description:     "Build and push Docker image to customer ECR",
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

	// Create App Runner service step
	appRunnerStep := NewCreateAppRunnerServiceStep(CreateAppRunnerServiceStepConfig{
		ID:                fmt.Sprintf("step-%d", stepID),
		Description:       "Create AWS App Runner service",
		ServiceName:       ad.spec.Name,
		ECRStepID:         ecrStepID,
		EnvVars:           ad.spec.EnvVars,
		ConnectionStepIDs: connectionStepIDs,
		CPU:               appRunnerCPU,
		Memory:            appRunnerMemory,
		Port:              8080, // Default port, TODO: make configurable
		DependsOn:         append([]string{ecrStepID}, connectionStepIDs...),
	})

	return appRunnerStep
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
