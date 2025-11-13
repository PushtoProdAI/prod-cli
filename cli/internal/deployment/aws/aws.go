package aws

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/go-errors/errors"

	backend "github.com/meroxa/prod/cli/internal/backend/aws"
	"github.com/meroxa/prod/cli/internal/config"
	"github.com/meroxa/prod/cli/internal/deployment"
	"github.com/meroxa/prod/cli/internal/llm"
	"github.com/meroxa/prod/cli/internal/output"
)

const (
	// RDS PostgreSQL configuration
	postgresInstanceClass = "db.t3.micro"
	postgresEngine        = "postgres"
	postgresEngineVersion = "16"
	postgresStorage       = 20 // GB
	postgresStorageType   = "gp3"

	// ElastiCache Redis configuration
	redisNodeType      = "cache.t3.micro"
	redisEngine        = "redis"
	redisEngineVersion = "7.0"

	// App Runner configuration
	appRunnerCPU    = "1 vCPU"
	appRunnerMemory = "2 GB"

	// Default region (will be overridden by user selection)
	defaultRegion = "us-east-1"
)

// AWSPricing contains pricing information for AWS services
type AWSPricing struct {
	AppRunner   map[string]float64 `json:"app_runner"`
	RDS         map[string]float64 `json:"rds"`
	Redis       map[string]float64 `json:"redis"`
	Storage     float64            `json:"storage_per_gb"`
	LastFetched time.Time          `json:"last_fetched"`
}

// fallbackPricing provides static pricing when AWS Pricing API fails
// App Runner costs vary based on active vs. provisioned hours
// These estimates assume ~12 hours active per day (moderate production usage)
// Actual costs range from ~$10/month (always idle) to ~$57/month (always active) for 1vCPU/2GB
var fallbackPricing = AWSPricing{
	AppRunner: map[string]float64{
		"1vCPU_2GB": 33.12, // ~12hrs active/day: (1vCPU×$0.064 + 2GB×$0.007)×12hrs active + 2GB×$0.007×24hrs provisioned
		"2vCPU_4GB": 66.24, // Scales linearly with vCPU/memory
		"4vCPU_8GB": 132.48,
	},
	RDS: map[string]float64{
		"db.t3.micro":  12.41, // ~$0.017/hour
		"db.t3.small":  24.82, // ~$0.034/hour
		"db.t3.medium": 49.64, // ~$0.068/hour
		"db.t4g.micro": 10.22, // ~$0.014/hour (ARM-based)
	},
	Redis: map[string]float64{
		"cache.t3.micro":  11.68, // ~$0.016/hour
		"cache.t3.small":  23.36, // ~$0.032/hour
		"cache.t4g.micro": 9.49,  // ~$0.013/hour (ARM-based)
	},
	Storage:     0.115, // $0.115/GB-month for gp3
	LastFetched: time.Date(2025, 1, 30, 0, 0, 0, 0, time.UTC),
}

// AWSDeploymentAdapter implements the DeploymentAdapter interface for AWS
type AWSDeploymentAdapter struct {
	client    AWSClient
	writer    io.Writer
	llmClient llm.Client
	region    string
}

// NewAWSDeploymentAdapter creates a new AWS deployment adapter
func NewAWSDeploymentAdapter(client AWSClient, region string, writer io.Writer, llmClient llm.Client) *AWSDeploymentAdapter {
	if writer == nil {
		writer = output.NewNoOpWriter()
	}
	if region == "" {
		region = defaultRegion
	}
	return &AWSDeploymentAdapter{
		client:    client,
		writer:    writer,
		llmClient: llmClient,
		region:    region,
	}
}

// SupportedStrategies returns the deployment strategies supported by AWS
func (ada *AWSDeploymentAdapter) SupportedStrategies() []deployment.DeploymentStrategy {
	return []deployment.DeploymentStrategy{
		deployment.StrategyAWS,
	}
}

// GenerateArtifacts generates deployment artifacts based on the strategy
func (ada *AWSDeploymentAdapter) GenerateArtifacts(spec *deployment.DeploymentSpec, strategy deployment.DeploymentStrategy) (deployment.Deployable, error) {
	// AWS deployments always use Docker for App Runner
	useDockerfile := true

	switch strategy {
	case deployment.StrategyAWS:
		// Create a new DockerGenerator with the spec's environment variables
		// This ensures build-time env vars are available during Docker build
		dockerGen := deployment.NewDockerGenerator(ada.writer, spec.EnvVars)
		deployment := NewAWSDeployment(ada.client, spec, dockerGen, useDockerfile, ada.region, ada.writer)
		return deployment, nil
	default:
		return nil, errors.Errorf("unsupported strategy: %s", strategy)
	}
}

// EstimateCost estimates the monthly cost for deploying to AWS using CloudFormation template
func (ada *AWSDeploymentAdapter) EstimateCost(ctx context.Context, spec *deployment.DeploymentSpec, strategy deployment.DeploymentStrategy) (deployment.CostEstimate, error) {
	slog.Debug("Estimating AWS deployment costs", "name", spec.Name, "region", ada.region)

	estimate, err := ada.estimateCostFromTemplate(ctx, spec)
	if err != nil {
		return deployment.CostEstimate{}, errors.Errorf("failed to estimate costs: %w", err)
	}

	return estimate, nil
}

// estimateCostFromTemplate uses CloudFormation template to calculate pricing
func (ada *AWSDeploymentAdapter) estimateCostFromTemplate(ctx context.Context, spec *deployment.DeploymentSpec) (deployment.CostEstimate, error) {
	// Build deployment spec for template generation
	// Convert deployment.DeploymentSpec to backend.AWSDeploymentSpec
	backingServices := make([]backend.BackingService, 0, len(spec.Services))
	for _, service := range spec.Services {
		switch service.Provider {
		case "postgresql":
			backingServices = append(backingServices, backend.BackingService{
				Type:             "rds",
				Name:             service.Name,
				Engine:           postgresEngine,
				InstanceClass:    postgresInstanceClass,
				AllocatedStorage: postgresStorage,
			})
		case "redis":
			backingServices = append(backingServices, backend.BackingService{
				Type:          "elasticache",
				Name:          service.Name,
				NodeType:      redisNodeType,
				NumCacheNodes: 1,
			})
		}
	}

	// Convert env vars
	backendEnvVars := make([]backend.EnvVar, len(spec.EnvVars))
	for i, ev := range spec.EnvVars {
		backendEnvVars[i] = backend.EnvVar{
			Name:              ev.Name,
			Value:             ev.Value,
			Role:              ev.Role,
			Service:           ev.Service,
			Sensitive:         ev.Sensitive,
			SensitivityReason: ev.SensitivityReason,
		}
	}

	deploymentSpec := backend.AWSDeploymentSpec{
		ServiceName:      spec.Name,
		ImageURL:         "placeholder.dkr.ecr.us-east-1.amazonaws.com/app:latest", // Placeholder for pricing
		CPU:              appRunnerCPU,
		Memory:           appRunnerMemory,
		Port:             8080, // Default port
		EnvVars:          backendEnvVars,
		BackingServices:  backingServices,
		MigrationCommand: spec.MigrationCommand,
	}

	// Get auth token from context (assuming it's available)
	authToken := ""
	if token, ok := ctx.Value("auth_token").(string); ok {
		authToken = token
	}

	// Call edge function to generate template
	backendURL := fmt.Sprintf("%s/%s", config.GetSupabaseURL(), "functions/v1")
	backendClient := backend.NewClient(backendURL)
	templateResp, err := backendClient.PreviewCloudFormationTemplate(ctx, authToken, deploymentSpec)
	if err != nil {
		return deployment.CostEstimate{}, errors.Errorf("failed to generate CloudFormation template: %w", err)
	}

	slog.Info("CloudFormation template generated", "serviceName", templateResp.ServiceName)

	// Parse template and estimate costs using AWS Pricing API
	pricer, err := NewTemplatePricer(ctx, ada.region)
	if err != nil {
		return deployment.CostEstimate{}, errors.Errorf("failed to create template pricer: %w", err)
	}

	estimate, err := pricer.EstimateCostFromTemplate(ctx, templateResp.Template)
	if err != nil {
		return deployment.CostEstimate{}, errors.Errorf("failed to estimate cost from template: %w", err)
	}

	return estimate, nil
}
