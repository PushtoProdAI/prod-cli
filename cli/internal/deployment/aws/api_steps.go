package aws

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/go-errors/errors"

	"github.com/pushtoprodai/prod-cli/internal/backend"
	backendaws "github.com/pushtoprodai/prod-cli/internal/backend/aws"
	"github.com/pushtoprodai/prod-cli/internal/deployment"
	"github.com/pushtoprodai/prod-cli/internal/output"
)

// CreateECRRepositoryStepConfig holds configuration for creating an ECR repository
type CreateECRRepositoryStepConfig struct {
	ID          string
	Description string
	ProjectName string
	AuthToken   string
	DependsOn   []string
}

// CreateECRRepositoryStep handles ECR repository creation in customer AWS account
type CreateECRRepositoryStep struct {
	BaseStep
	ProjectName string `json:"projectName"`
	AuthToken   string `json:"authToken"`
}

func NewCreateECRRepositoryStep(config CreateECRRepositoryStepConfig) *CreateECRRepositoryStep {
	return &CreateECRRepositoryStep{
		BaseStep: BaseStep{
			ID:          config.ID,
			Description: config.Description,
			DependsOn:   config.DependsOn,
		},
		ProjectName: config.ProjectName,
		AuthToken:   config.AuthToken,
	}
}

func (s *CreateECRRepositoryStep) Execute(ctx context.Context, client AWSClient, stepResults map[string]any) (any, error) {
	slog.Info("Creating ECR repository in customer AWS account", "project", s.ProjectName)

	// Call backend to create ECR repository in customer account (location: external)
	dockerGenerator := deployment.NewDockerGenerator(output.NewNoOpWriter(), []deployment.EnvVar{})
	defer dockerGenerator.Close()

	repoInfo, err := dockerGenerator.CreateDockerRepositoryExternal(ctx, s.AuthToken, s.ProjectName)
	if err != nil {
		return nil, errors.Errorf("failed to create ECR repository: %w", err)
	}

	slog.Info("ECR repository created", "repositoryUri", repoInfo.RepositoryURI)

	return map[string]any{
		"repositoryUri":  repoInfo.RepositoryURI,
		"repositoryName": repoInfo.RepositoryName,
		"exists":         repoInfo.Exists,
		"created":        repoInfo.Created,
	}, nil
}

func (s *CreateECRRepositoryStep) Rollback(ctx context.Context, client AWSClient, stepResults map[string]any) error {
	// No rollback for ECR repository creation
	// Repository can remain for future deployments
	return nil
}

// BuildAndPushECRStepConfig holds configuration for building and pushing Docker images to ECR
type BuildAndPushECRStepConfig struct {
	ID              string
	Description     string
	DeploymentSpec  *deployment.DeploymentSpec
	DockerGenerator *deployment.DockerGenerator
	BuildContext    string
	AuthToken       string
	DependsOn       []string
}

// BuildAndPushECRStep handles Docker image building and pushing to customer ECR
type BuildAndPushECRStep struct {
	BaseStep
	DeploymentSpec  *deployment.DeploymentSpec
	DockerGenerator *deployment.DockerGenerator
	BuildContext    string
	AuthToken       string
}

func NewBuildAndPushECRStep(config BuildAndPushECRStepConfig) *BuildAndPushECRStep {
	return &BuildAndPushECRStep{
		BaseStep: BaseStep{
			ID:          config.ID,
			Description: config.Description,
			DependsOn:   config.DependsOn,
		},
		DeploymentSpec:  config.DeploymentSpec,
		DockerGenerator: config.DockerGenerator,
		BuildContext:    config.BuildContext,
		AuthToken:       config.AuthToken,
	}
}

func (s *BuildAndPushECRStep) Execute(ctx context.Context, client AWSClient, stepResults map[string]any) (any, error) {
	slog.Info("Building and pushing Docker image to customer ECR", "project", s.DeploymentSpec.Name)

	// BuildAndPushExternal uses location="external" for customer ECR
	buildResult, pushResult, err := s.DockerGenerator.BuildAndPushExternal(ctx, s.DeploymentSpec, s.BuildContext, s.AuthToken)
	if err != nil {
		return nil, errors.Errorf("failed to build and push Docker image: %w", err)
	}

	slog.Info(
		"Docker image built and pushed",
		"imageID", buildResult.ImageID,
		"pushedImageURL", pushResult.PushedImageURL,
	)

	return map[string]any{
		"imageName":      buildResult.ImageName,
		"imageID":        buildResult.ImageID,
		"pushedImageURL": pushResult.PushedImageURL,
		"repositoryURL":  pushResult.RepositoryURL,
	}, nil
}

func (s *BuildAndPushECRStep) Rollback(ctx context.Context, client AWSClient, stepResults map[string]any) error {
	// No rollback needed for Docker build/push
	// The image will remain in ECR unused
	return nil
}

// CreateAppRunnerServiceStepConfig holds configuration for creating an App Runner service
type CreateAppRunnerServiceStepConfig struct {
	ID                string
	Description       string
	ServiceName       string
	ECRStepID         string // Step ID that created/pushed the ECR image
	EnvVars           []deployment.EnvVar
	Services          []deployment.Service // Database and cache services
	ConnectionStepIDs []string
	CPU               string
	Memory            string
	Port              int
	AuthToken         string
	MigrationCommand  string
	IsUpdate          bool // True if updating existing deployment
	DependsOn         []string
}

// CreateAppRunnerServiceStep handles App Runner service creation via CloudFormation
type CreateAppRunnerServiceStep struct {
	BaseStep
	ServiceName       string               `json:"serviceName"`
	ECRStepID         string               `json:"ecrStepId"`
	EnvVars           []deployment.EnvVar  `json:"envVars"`
	Services          []deployment.Service `json:"services"`
	ConnectionStepIDs []string             `json:"connectionStepIds"`
	CPU               string               `json:"cpu"`
	Memory            string               `json:"memory"`
	Port              int                  `json:"port"`
	AuthToken         string               `json:"authToken"`
	MigrationCommand  string               `json:"migrationCommand"`
	IsUpdate          bool                 `json:"isUpdate"`
}

func NewCreateAppRunnerServiceStep(config CreateAppRunnerServiceStepConfig) *CreateAppRunnerServiceStep {
	return &CreateAppRunnerServiceStep{
		BaseStep: BaseStep{
			ID:          config.ID,
			Description: config.Description,
			DependsOn:   config.DependsOn,
		},
		ServiceName:       config.ServiceName,
		ECRStepID:         config.ECRStepID,
		EnvVars:           config.EnvVars,
		Services:          config.Services,
		ConnectionStepIDs: config.ConnectionStepIDs,
		CPU:               config.CPU,
		Memory:            config.Memory,
		Port:              config.Port,
		AuthToken:         config.AuthToken,
		MigrationCommand:  config.MigrationCommand,
		IsUpdate:          config.IsUpdate,
	}
}

func (s *CreateAppRunnerServiceStep) Execute(ctx context.Context, client AWSClient, stepResults map[string]any) (any, error) {
	slog.Info("Initiating AWS CloudFormation deployment", "service", s.ServiceName)

	// Get the pushed image URL from ECR step results
	ecrResult, ok := stepResults[s.ECRStepID].(map[string]any)
	if !ok {
		return nil, errors.New("ECR step result not found or invalid format")
	}

	pushedImageURL, ok := ecrResult["pushedImageURL"].(string)
	if !ok || pushedImageURL == "" {
		return nil, errors.New("pushed image URL not found in ECR step result")
	}

	// On first deploy WITH migrations, don't create App Runner yet (it will be added after migration)
	// On first deploy WITHOUT migrations, create App Runner immediately (single-phase)
	// On updates, App Runner already exists so CreateAppRunner is not set (defaults to true in template)
	var createAppRunner *bool
	if !s.IsUpdate && s.MigrationCommand != "" {
		// First deploy with migrations: defer App Runner creation until after migration
		falseVal := false
		createAppRunner = &falseVal
		slog.Info("First deploy with migrations - App Runner will be created after migration completes")
	} else if !s.IsUpdate && s.MigrationCommand == "" {
		// First deploy without migrations: create App Runner immediately
		slog.Info("First deploy without migrations - App Runner will be created in initial stack")
		// createAppRunner stays nil, defaults to true in template
	}

	// Build deployment spec using shared helper
	deploymentSpec, err := BuildAWSDeploymentSpec(
		s.ServiceName,
		pushedImageURL,
		s.CPU,
		s.Memory,
		s.Port,
		s.EnvVars,
		s.Services,
		s.MigrationCommand,
		createAppRunner,
	)
	if err != nil {
		return nil, errors.Errorf("failed to build deployment spec: %w", err)
	}

	// Call backend to initiate CloudFormation stack deployment
	backendClient := backend.NewClient()

	slog.Info(
		"Calling backend to initiate CloudFormation stack deployment",
		"service", s.ServiceName,
		"image", pushedImageURL,
		"cpu", s.CPU,
		"memory", s.Memory,
		"backingServicesCount", len(deploymentSpec.BackingServices),
	)

	// Deploy stack - backend returns immediately without polling
	result, err := backendClient.DeployAWSStack(ctx, s.AuthToken, deploymentSpec)
	if err != nil {
		return nil, errors.Errorf("failed to initiate AWS stack deployment: %w", err)
	}

	if result.Error != "" {
		return nil, errors.Errorf("CloudFormation deployment initiation failed: %s", result.Error)
	}

	slog.Info(
		"CloudFormation stack deployment initiated",
		"stackId", result.StackID,
		"stackName", result.StackName,
		"status", result.Status,
	)

	// Return immediately with stack info - polling will happen in workflow
	return deployment.CreatedResource{
		ID:   result.StackID,
		Type: "cloudformation_stack",
		Name: s.ServiceName,
		Metadata: map[string]any{
			"stackId":   result.StackID,
			"stackName": result.StackName,
			"status":    result.Status,
			"image":     pushedImageURL,
			"cpu":       s.CPU,
			"memory":    s.Memory,
			"port":      s.Port,
		},
	}, nil
}

func (s *CreateAppRunnerServiceStep) Rollback(ctx context.Context, client AWSClient, stepResults map[string]any) error {
	// TODO: Implement CloudFormation stack deletion for rollback
	slog.Info("Would rollback CloudFormation stack", "service", s.ServiceName)
	// For now, we'll leave the stack in place for manual cleanup
	// In production, we would call CloudFormation DeleteStack API
	return nil
}

// BuildAWSDeploymentSpec creates a backend.AWSDeploymentSpec from deployment configuration
// This helper is used by both steps (during initial deploy) and activities (during updates)
func BuildAWSDeploymentSpec(
	serviceName, imageURL, cpu, memory string,
	port int,
	envVars []deployment.EnvVar,
	services []deployment.Service,
	migrationCommand string,
	createAppRunner *bool,
) (backend.AWSDeploymentSpec, error) {
	// Convert EnvVars to backend format with PORT injection
	backendEnvVars := make([]backend.EnvVar, 0, len(envVars)+1)
	hasPort := false
	for _, envVar := range envVars {
		backendEnvVars = append(backendEnvVars, backend.EnvVar{
			Name:              envVar.Name,
			Value:             envVar.Value,
			Role:              envVar.Role,
			Service:           envVar.Service,
			Sensitive:         envVar.Sensitive,
			SensitivityReason: envVar.SensitivityReason,
		})
		if envVar.Name == "PORT" {
			hasPort = true
		}
	}

	// Add PORT environment variable if not already present
	if !hasPort {
		backendEnvVars = append(backendEnvVars, backend.EnvVar{
			Name:  "PORT",
			Value: fmt.Sprintf("%d", port),
			Role:  "user",
		})
		slog.Info("Added PORT environment variable", "port", port)
	} else {
		slog.Info("PORT environment variable already defined by user")
	}

	// Convert deployment services to backing services
	backingServices := make([]backend.BackingService, 0, len(services))
	for _, svc := range services {
		var serviceType string
		if svc.Provider == "postgresql" || svc.Provider == "mysql" {
			serviceType = "rds"
		} else if svc.Provider == "redis" {
			// Use Serverless ElastiCache with Valkey engine for Redis
			serviceType = serverlessCacheType
		} else {
			slog.Warn("Unsupported service provider for AWS", "provider", svc.Provider)
			continue
		}

		backingService := backend.BackingService{
			Type: serviceType,
			Name: svc.Name,
		}

		if svc.Provider == "postgresql" {
			backingService.Engine = "postgres"
			backingService.InstanceClass = "db.t3.micro"
			backingService.AllocatedStorage = 20
		} else if svc.Provider == "mysql" {
			backingService.Engine = "mysql"
			backingService.InstanceClass = "db.t3.micro"
			backingService.AllocatedStorage = 20
		} else if svc.Provider == "redis" {
			// Configure Serverless ElastiCache with sensible defaults
			backingService.MajorEngineVersion = serverlessCacheEngineVersion
			backingService.CacheUsageLimits = &backendaws.CacheUsageLimits{
				DataStorage: &backendaws.DataStorageLimit{
					Maximum: serverlessCacheDataStorageGB,
					Unit:    serverlessCacheStorageUnit,
				},
				ECPUPerSecond: &backendaws.ECPULimit{
					Maximum: serverlessCacheMaxECPU,
				},
			}
		}

		backingServices = append(backingServices, backingService)
	}

	return backend.AWSDeploymentSpec{
		ServiceName:      serviceName,
		ImageURL:         imageURL,
		CPU:              cpu,
		Memory:           memory,
		Port:             port,
		EnvVars:          backendEnvVars,
		BackingServices:  backingServices,
		MigrationCommand: migrationCommand,
		CreateAppRunner:  createAppRunner,
	}, nil
}
