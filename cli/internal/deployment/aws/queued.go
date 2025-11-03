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

	// TODO: Implement deployment logic
	// Steps will include:
	// 1. Create/get ECR repository
	// 2. Build and push Docker image to ECR
	// 3. Create VPC resources if needed (or use default VPC)
	// 4. Create RDS instances for PostgreSQL databases
	// 5. Create ElastiCache clusters for Redis
	// 6. Store database credentials in Secrets Manager
	// 7. Create App Runner service with image and env vars
	// 8. Wait for service to be ready
	// 9. Return created resources

	slog.Warn("AWS deployment not yet implemented - returning stub resources")

	// Return stub resources for now
	resources := []deployment.CreatedResource{
		{
			ID:   "stub-app-runner-service",
			Type: "app_runner_service",
			Name: ad.spec.Name,
			Metadata: map[string]any{
				"status": "stub",
				"region": ad.region,
			},
		},
	}

	return resources, nil
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

	// Step 1: Create/Get ECR Repository
	steps = append(steps, ad.createECRRepositoryStep(stepCounter))
	stepCounter++

	// Step 2: Build and push Docker image
	// (This will be handled separately using Docker client)

	// Step 3-N: Create backing services (RDS, ElastiCache)
	backingServiceSteps, nextCounter := ad.createBackingServiceSteps(stepCounter)
	steps = append(steps, backingServiceSteps...)
	stepCounter = nextCounter

	// Step N+1: Create App Runner service
	steps = append(steps, ad.createAppRunnerServiceStep(stepCounter))
	stepCounter++

	return steps
}

func (ad *AWSDeployment) createECRRepositoryStep(stepID int) AWSAPIStep {
	// TODO: Implement ECR repository creation step
	// This will create an ECR repository or return the existing one

	return nil
}

func (ad *AWSDeployment) createBackingServiceSteps(startCounter int) ([]AWSAPIStep, int) {
	var steps []AWSAPIStep
	stepCounter := startCounter

	// TODO: Implement backing service creation steps
	// For each service in spec.Services:
	// - If postgresql: create RDS instance
	// - If redis: create ElastiCache cluster
	// - Store credentials in Secrets Manager
	// - Add connection strings to environment variables

	for _, service := range ad.spec.Services {
		switch service.Provider {
		case "postgresql":
			// Create RDS instance step
			slog.Info("Would create RDS instance", "name", service.Name)
			stepCounter++
		case "redis":
			// Create ElastiCache cluster step
			slog.Info("Would create ElastiCache cluster", "name", service.Name)
			stepCounter++
		}
	}

	return steps, stepCounter
}

func (ad *AWSDeployment) createAppRunnerServiceStep(stepID int) AWSAPIStep {
	// TODO: Implement App Runner service creation step
	// This will:
	// 1. Collect all environment variables (including DB connection strings)
	// 2. Create the App Runner service with the ECR image
	// 3. Configure auto-scaling, health checks, etc.

	return nil
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
