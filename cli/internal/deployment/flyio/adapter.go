package flyio

import (
	"context"
	"io"
	"log/slog"

	"github.com/go-errors/errors"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
	"github.com/pushtoprodai/prod-cli/internal/deployment/pricing"
	"github.com/pushtoprodai/prod-cli/internal/llm"
)

// FlyioDeploymentAdapter implements the DeploymentAdapter interface for Fly.io
type FlyioDeploymentAdapter struct {
	client          FlyioClient
	writer          io.Writer
	pricingService  pricing.Service
	llmClient       llm.Client
	dockerGenerator *deployment.DockerGenerator
}

// NewFlyioDeploymentAdapter creates a new Fly.io deployment adapter
func NewFlyioDeploymentAdapter(client FlyioClient, writer io.Writer, llmClient llm.Client) *FlyioDeploymentAdapter {
	return &FlyioDeploymentAdapter{
		client:          client,
		writer:          writer,
		llmClient:       llmClient,
		dockerGenerator: deployment.NewDockerGenerator(writer, []deployment.EnvVar{}),
	}
}

// NewFlyioDeploymentAdapterWithPricing creates a new Fly.io deployment adapter with custom pricing service
func NewFlyioDeploymentAdapterWithPricing(client FlyioClient, writer io.Writer, pricingService pricing.Service) *FlyioDeploymentAdapter {
	return &FlyioDeploymentAdapter{
		client:         client,
		writer:         writer,
		pricingService: pricingService,
	}
}

// NewDefaultFlyioDeploymentAdapter creates a deployment adapter with the default client
func NewDefaultFlyioDeploymentAdapter(writer io.Writer, llmClient llm.Client) *FlyioDeploymentAdapter {
	return NewFlyioDeploymentAdapter(NewFlyctlClient(writer), writer, llmClient)
}

// SupportedStrategies returns the deployment strategies supported by Fly.io
func (fda *FlyioDeploymentAdapter) SupportedStrategies() []deployment.DeploymentStrategy {
	// Fly.io doesn't have blueprints - that's a Render concept
	// We only have one strategy: step-by-step deployment
	return []deployment.DeploymentStrategy{
		deployment.StrategyFlyio,
	}
}

// GenerateArtifacts generates deployment artifacts for the specified strategy
func (fda *FlyioDeploymentAdapter) GenerateArtifacts(spec *deployment.DeploymentSpec, strategy deployment.DeploymentStrategy) (deployment.Deployable, error) {
	// Fly.io only has one deployment approach - no blueprints
	if strategy != deployment.StrategyFlyio {
		return nil, errors.Errorf("unsupported deployment strategy for Fly.io: %s (only %s is supported)", strategy, deployment.StrategyFlyio)
	}
	return NewFlyioQueuedDeployment(fda.client, spec, fda.dockerGenerator, fda.writer), nil
}

// GenerateArtifactsWithSource generates deployment artifacts with source path
func (fda *FlyioDeploymentAdapter) GenerateArtifactsWithSource(spec *deployment.DeploymentSpec, strategy deployment.DeploymentStrategy, sourcePath string) (deployment.Deployable, error) {
	// Only one strategy supported now
	if strategy != deployment.StrategyFlyio {
		return nil, errors.Errorf("unsupported deployment strategy for Fly.io: %s (only %s is supported)", strategy, deployment.StrategyFlyio)
	}

	// Pass the source path through metadata so it can be used during deployment
	if spec.Metadata == nil {
		spec.Metadata = make(map[string]any)
	}
	spec.Metadata["buildContext"] = sourcePath

	return NewFlyioQueuedDeployment(fda.client, spec, fda.dockerGenerator, fda.writer), nil
}

// EstimateCost estimates the cost of deployment on Fly.io
func (fda *FlyioDeploymentAdapter) EstimateCost(ctx context.Context, spec *deployment.DeploymentSpec, strategy deployment.DeploymentStrategy) (deployment.CostEstimate, error) {
	slog.Info("Estimating costs for spec", "spec", spec, "strategy", strategy)

	// Build cost request from deployment spec
	cr := deployment.CostRequest{Services: make([]deployment.CostService, 0)}

	// Add services from spec
	for _, service := range spec.Services {
		cs := deployment.CostService{}
		switch service.Provider {
		case "postgresql":
			cs.Service = service
			cs.Plan = "basic" // Default plan
			cs.Storage = 10   // Default 10GB storage
		case "redis":
			cs.Service = service
			cs.Plan = "pay-as-you-go" // Default plan (Upstash Redis - pay per use)
		default:
			continue
		}
		cr.Services = append(cr.Services, cs)
	}

	// Add a service representing the web service (machine)
	cs := deployment.CostService{
		Service: deployment.Service{
			Name:     "web",
			Provider: "web",
		},
		Plan: "shared-cpu-1x", // Default machine plan
	}
	cr.Services = append(cr.Services, cs)

	ce, err := fda.estimateFlyioCost(ctx, cr)
	return ce, err
}

func (fda *FlyioDeploymentAdapter) estimateFlyioCost(ctx context.Context, cr deployment.CostRequest) (deployment.CostEstimate, error) {
	slog.Info("Estimating Fly.io costs for request", "request", cr)

	ce := deployment.CostEstimate{Services: make([]deployment.CostService, 0, len(cr.Services))}
	ce.Total = 0.0

	// Fetch live Redis pricing once for all services
	var redisPricing map[string]float64
	if flyctlClient, ok := fda.client.(*FlyctlClient); ok {
		livePricing, err := flyctlClient.GetRedisPricing(ctx)
		if err != nil {
			slog.Warn("Failed to fetch live Redis pricing", "error", err)
		} else {
			redisPricing = livePricing
			slog.Info("Fetched live Redis pricing", "plans", len(redisPricing))
		}
	}

	var pricingService pricing.Service
	if fda.pricingService != nil {
		// Use injected pricing service (for testing)
		pricingService = fda.pricingService
	} else {
		// Create pricing service with Flyio pricing provider (production)
		pricingProvider := NewPricingProvider()
		pricingService = pricing.NewPricingService(pricingProvider, pricing.DefaultRetries, fda.llmClient)
	}

	for _, service := range cr.Services {
		// For Redis, use deterministic pricing from flyctl instead of LLM
		if service.Service.Provider == "redis" && redisPricing != nil {
			cost := fda.getFlyioFallbackServiceCost(service.Service.Provider, service.Plan, service.Storage, redisPricing)
			service.Cost = cost
			ce.Total += service.Cost
			ce.Services = append(ce.Services, service)
			continue
		}

		// For non-Redis services, use LLM pricing
		result, err := pricingService.EstimateCost(ctx, service)
		if err != nil {
			slog.Info("Failed to fetch pricing via LLM, using fallback", "service", service.Service.Name, "error", err)
			return fda.estimateFlyioCostFallback(ctx, cr)
		}

		// Apply usage-based costs for storage (Flyio specific logic)
		service.Cost = pricing.ApplyUsageCosts(result.Cost, result.UsageCosts, float64(service.Storage), "GB")

		ce.Total += service.Cost
		ce.Services = append(ce.Services, service)
	}

	return ce, nil
}

func (fda *FlyioDeploymentAdapter) estimateFlyioCostFallback(ctx context.Context, cr deployment.CostRequest) (deployment.CostEstimate, error) {
	slog.Info("Using Fly.io fallback pricing")

	ce := deployment.CostEstimate{Services: make([]deployment.CostService, 0, len(cr.Services))}
	ce.Total = 0.0

	// Fetch live Redis pricing if available
	var redisPricing map[string]float64
	if flyctlClient, ok := fda.client.(*FlyctlClient); ok {
		livePricing, err := flyctlClient.GetRedisPricing(ctx)
		if err != nil {
			slog.Warn("Failed to fetch live Redis pricing, using defaults", "error", err)
		} else {
			redisPricing = livePricing
			slog.Info("Fetched live Redis pricing", "plans", len(redisPricing))
		}
	}

	for _, service := range cr.Services {
		cost := fda.getFlyioFallbackServiceCost(service.Provider, service.Plan, service.Storage, redisPricing)
		service.Cost = cost
		ce.Total += service.Cost
		ce.Services = append(ce.Services, service)
	}
	return ce, nil
}

func (fda *FlyioDeploymentAdapter) getFlyioFallbackServiceCost(provider, plan string, storage int, liveRedisPricing map[string]float64) float64 {
	pricing := GetEstimatedPricing()

	switch provider {
	case "web":
		if cost, ok := pricing.Machines[plan]; ok {
			return cost
		}
		return pricing.Machines["shared-cpu-1x"] // Default
	case "postgresql":
		if cost, ok := pricing.Databases[plan]; ok {
			// Add storage cost
			storageCost := float64(storage) * pricing.Storage
			return cost + storageCost
		}
		return pricing.Databases["basic"] + (float64(storage) * pricing.Storage) // Default
	case "redis":
		// Use live pricing if available
		if liveRedisPricing != nil {
			// Try exact match first
			if cost, ok := liveRedisPricing[plan]; ok {
				return cost
			}
			// Default to pay-as-you-go if available
			if cost, ok := liveRedisPricing["pay-as-you-go"]; ok {
				return cost
			}
		}
		// Fall back to static pricing
		if cost, ok := pricing.Redis[plan]; ok {
			return cost
		}
		return pricing.Redis["pay-as-you-go"] // Default to pay-as-you-go
	default:
		return 0.0
	}
}

// Removed unused helper methods - these are now in blueprint.go and queued.go
// where they're actually used
