package aws

import (
	"context"
	"io"
	"log/slog"
	"time"

	"github.com/go-errors/errors"

	"github.com/meroxa/prod/cli/internal/deployment"
	"github.com/meroxa/prod/cli/internal/deployment/pricing"
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

// fallbackPricing provides static pricing when LLM lookup fails
var fallbackPricing = AWSPricing{
	AppRunner: map[string]float64{
		"1vCPU_2GB": 51.84, // $0.007/vCPU-hour + $0.003/GB-hour
		"2vCPU_4GB": 103.68,
		"4vCPU_8GB": 207.36,
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
	client          AWSClient
	dockerGenerator *deployment.DockerGenerator
	writer          io.Writer
	llmClient       llm.Client
	region          string
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
		client:          client,
		dockerGenerator: deployment.NewDockerGenerator(writer, []deployment.EnvVar{}),
		writer:          writer,
		llmClient:       llmClient,
		region:          region,
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
		deployment := NewAWSDeployment(ada.client, spec, ada.dockerGenerator, useDockerfile, ada.region, ada.writer)
		return deployment, nil
	default:
		return nil, errors.Errorf("unsupported strategy: %s", strategy)
	}
}

// EstimateCost estimates the monthly cost for deploying to AWS
func (ada *AWSDeploymentAdapter) EstimateCost(ctx context.Context, spec *deployment.DeploymentSpec, strategy deployment.DeploymentStrategy) (deployment.CostEstimate, error) {
	slog.Info("Estimating costs for AWS deployment", "spec", spec, "strategy", strategy)

	cr := deployment.CostRequest{Services: make([]deployment.CostService, 0)}

	// Add backing services (RDS, ElastiCache)
	for _, service := range spec.Services {
		cs := deployment.CostService{}
		switch service.Provider {
		case "postgresql":
			cs.Service = service
			cs.Plan = postgresInstanceClass
			cs.Storage = postgresStorage
		case "redis":
			cs.Service = service
			cs.Plan = redisNodeType
		default:
			continue
		}
		cr.Services = append(cr.Services, cs)
	}

	// Add App Runner web service
	cs := deployment.CostService{
		Service: deployment.Service{
			Name:     "web",
			Provider: "apprunner",
		},
		Plan: "1vCPU_2GB",
	}
	cr.Services = append(cr.Services, cs)

	ce, _ := ada.estimateCost(ctx, cr)
	return ce, nil
}

func (ada *AWSDeploymentAdapter) estimateCost(ctx context.Context, cr deployment.CostRequest) (deployment.CostEstimate, error) {
	slog.Info("Estimating costs for AWS request", "request", cr)

	ce := deployment.CostEstimate{Services: make([]deployment.CostService, 0, len(cr.Services))}
	ce.Total = 0.0

	// Create pricing service with AWS pricing provider
	pricingProvider := NewPricingProvider()
	pricingService := pricing.NewPricingService(pricingProvider, pricing.DefaultRetries, ada.llmClient)

	for _, service := range cr.Services {
		result, err := pricingService.EstimateCost(ctx, service)
		if err != nil {
			slog.Info("Failed to fetch pricing via LLM, using fallback", "service", service.Name, "error", err)
			return estimateCostFallback(cr)
		}

		// Apply usage-based costs for storage (AWS specific logic)
		service.Cost = pricing.ApplyUsageCosts(result.Cost, result.UsageCosts, float64(service.Storage), "GB")

		ce.Total += service.Cost
		ce.Services = append(ce.Services, service)
	}

	return ce, nil
}

func estimateCostFallback(cr deployment.CostRequest) (deployment.CostEstimate, error) {
	slog.Info("Using fallback pricing", "lastUpdated", fallbackPricing.LastFetched.Format("2006-01-02"))

	ce := deployment.CostEstimate{Services: make([]deployment.CostService, 0, len(cr.Services))}
	ce.Total = 0.0

	for _, service := range cr.Services {
		cost := getFallbackServiceCost(service.Provider, service.Plan, service.Storage)
		service.Cost = cost
		ce.Total += service.Cost
		ce.Services = append(ce.Services, service)
	}
	return ce, nil
}

func getFallbackServiceCost(provider, plan string, storage int) float64 {
	switch provider {
	case "postgresql":
		baseCost := fallbackPricing.RDS[plan]
		if baseCost == 0 {
			baseCost = fallbackPricing.RDS["db.t3.micro"] // default
		}
		storageCost := float64(storage) * fallbackPricing.Storage
		return baseCost + storageCost
	case "redis":
		cost := fallbackPricing.Redis[plan]
		if cost == 0 {
			cost = fallbackPricing.Redis["cache.t3.micro"] // default
		}
		return cost
	case "apprunner":
		cost := fallbackPricing.AppRunner[plan]
		if cost == 0 {
			cost = fallbackPricing.AppRunner["1vCPU_2GB"] // default
		}
		return cost
	default:
		return 0.0
	}
}
