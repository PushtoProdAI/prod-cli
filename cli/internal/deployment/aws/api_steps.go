package aws

import (
	"context"
	"log/slog"

	"github.com/go-errors/errors"

	"github.com/meroxa/prod/cli/internal/backend"
	"github.com/meroxa/prod/cli/internal/deployment"
	"github.com/meroxa/prod/cli/internal/output"
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

	slog.Info("Docker image built and pushed",
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
	ConnectionStepIDs []string
	CPU               string
	Memory            string
	Port              int
	AuthToken         string
	DependsOn         []string
}

// CreateAppRunnerServiceStep handles App Runner service creation via CloudFormation
type CreateAppRunnerServiceStep struct {
	BaseStep
	ServiceName       string              `json:"serviceName"`
	ECRStepID         string              `json:"ecrStepId"`
	EnvVars           []deployment.EnvVar `json:"envVars"`
	ConnectionStepIDs []string            `json:"connectionStepIds"`
	CPU               string              `json:"cpu"`
	Memory            string              `json:"memory"`
	Port              int                 `json:"port"`
	AuthToken         string              `json:"authToken"`
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
		ConnectionStepIDs: config.ConnectionStepIDs,
		CPU:               config.CPU,
		Memory:            config.Memory,
		Port:              config.Port,
		AuthToken:         config.AuthToken,
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

	// Collect environment variables including database connections
	envVarMap := make(map[string]string)
	for _, envVar := range s.EnvVars {
		if envVar.Value != "" {
			envVarMap[envVar.Name] = envVar.Value
		}
	}

	// Add connection strings from backing services
	for _, connStepID := range s.ConnectionStepIDs {
		if connResult, ok := stepResults[connStepID].(map[string]any); ok {
			if connStr, ok := connResult["connectionString"].(string); ok {
				// TODO: Map to appropriate env var name based on service type
				envVarMap["DATABASE_URL"] = connStr
			}
		}
	}

	// Call backend to initiate CloudFormation stack deployment
	backendClient := backend.NewClient()

	deploymentSpec := backend.AWSDeploymentSpec{
		ServiceName:     s.ServiceName,
		ImageURL:        pushedImageURL,
		CPU:             s.CPU,
		Memory:          s.Memory,
		Port:            s.Port,
		EnvVars:         envVarMap,
		BackingServices: []backend.BackingService{},
	}

	slog.Info("Calling backend to initiate CloudFormation stack deployment",
		"service", s.ServiceName,
		"image", pushedImageURL,
		"cpu", s.CPU,
		"memory", s.Memory,
	)

	// Deploy stack - backend returns immediately without polling
	result, err := backendClient.DeployAWSStack(ctx, s.AuthToken, deploymentSpec)
	if err != nil {
		return nil, errors.Errorf("failed to initiate AWS stack deployment: %w", err)
	}

	if result.Error != "" {
		return nil, errors.Errorf("CloudFormation deployment initiation failed: %s", result.Error)
	}

	slog.Info("CloudFormation stack deployment initiated",
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
