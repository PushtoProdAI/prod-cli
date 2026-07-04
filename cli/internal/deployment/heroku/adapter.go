package heroku

import (
	"context"
	"io"
	"log/slog"
	"strings"

	"github.com/go-errors/errors"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
	"github.com/pushtoprodai/prod-cli/internal/deployment/pricing"
	"github.com/pushtoprodai/prod-cli/internal/llm"
	"github.com/pushtoprodai/prod-cli/internal/output"
)

// HerokuDeploymentAdapter implements the DeploymentAdapter interface for Heroku
type HerokuDeploymentAdapter struct {
	client    *HerokuClient
	writer    io.Writer
	llmClient llm.Client
}

// NewHerokuDeploymentAdapter creates a new Heroku deployment adapter
func NewHerokuDeploymentAdapter(client *HerokuClient, writer io.Writer, llmClient llm.Client) *HerokuDeploymentAdapter {
	if writer == nil {
		writer = output.NewNoOpWriter()
	}
	return &HerokuDeploymentAdapter{
		client:    client,
		writer:    writer,
		llmClient: llmClient,
	}
}

// NewDefaultHerokuDeploymentAdapter creates a deployment adapter with the default client
func NewDefaultHerokuDeploymentAdapter(writer io.Writer, llmClient llm.Client) *HerokuDeploymentAdapter {
	return NewHerokuDeploymentAdapter(NewHerokuClient("", writer), writer, llmClient)
}

// SupportedStrategies returns the deployment strategies supported by Heroku
func (hda *HerokuDeploymentAdapter) SupportedStrategies() []deployment.DeploymentStrategy {
	return []deployment.DeploymentStrategy{
		deployment.StrategyHeroku,
	}
}

// GenerateArtifacts generates deployment artifacts for the specified strategy
func (hda *HerokuDeploymentAdapter) GenerateArtifacts(spec *deployment.DeploymentSpec, strategy deployment.DeploymentStrategy) (deployment.Deployable, error) {
	// Validate the deployment spec
	if err := hda.validateSpec(spec); err != nil {
		return nil, errors.Errorf("invalid deployment spec for Heroku: %w", err)
	}

	// Heroku only has one deployment approach
	if strategy != deployment.StrategyHeroku {
		return nil, errors.Errorf("unsupported deployment strategy for Heroku: %s", strategy)
	}

	// Use the queued deployment approach with API steps
	return NewQueuedDeployment(hda.client, spec, hda.writer), nil
}

// GenerateArtifactsWithSource generates deployment artifacts with source path
func (hda *HerokuDeploymentAdapter) GenerateArtifactsWithSource(spec *deployment.DeploymentSpec, strategy deployment.DeploymentStrategy, sourcePath string) (deployment.Deployable, error) {
	// Validate the deployment spec
	if err := hda.validateSpec(spec); err != nil {
		return nil, errors.Errorf("invalid deployment spec for Heroku: %w", err)
	}

	// Heroku only has one deployment approach
	if strategy != deployment.StrategyHeroku {
		return nil, errors.Errorf("unsupported deployment strategy for Heroku: %s", strategy)
	}

	// Pass the source path through metadata so it can be used during deployment
	if spec.Metadata == nil {
		spec.Metadata = make(map[string]any)
	}
	spec.Metadata["buildContext"] = sourcePath

	// Use the queued deployment approach with API steps
	return NewQueuedDeployment(hda.client, spec, hda.writer), nil
}

// EstimateCost estimates the cost of deployment on Heroku
func (hda *HerokuDeploymentAdapter) EstimateCost(ctx context.Context, spec *deployment.DeploymentSpec, strategy deployment.DeploymentStrategy) (deployment.CostEstimate, error) {
	slog.Info("Estimating costs for spec", "spec", spec, "strategy", strategy)

	// Build cost request from deployment spec
	cr := deployment.CostRequest{Services: make([]deployment.CostService, 0)}

	// Add services from spec
	for _, service := range spec.Services {
		cs := deployment.CostService{}
		switch service.Provider {
		case "postgresql":
			cs.Service = service
			cs.Plan = "essential-0"
			cs.Storage = 0
		case "redis":
			cs.Service = service
			cs.Plan = "mini"
		case "mysql":
			cs.Service = service
			cs.Plan = "kitefin" // JawsDB MySQL plan
		case "mongodb":
			cs.Service = service
			cs.Plan = "shared-single-small" // MongoLab plan
		default:
			continue
		}
		cr.Services = append(cr.Services, cs)
	}

	// Add a service representing the web dyno
	cs := deployment.CostService{
		Service: deployment.Service{
			Name:     "web",
			Provider: "web",
		},
		Plan: "basic", // Default to basic dyno
	}
	cr.Services = append(cr.Services, cs)

	return hda.estimateCost(ctx, cr)
}

func (hda *HerokuDeploymentAdapter) estimateCost(ctx context.Context, cr deployment.CostRequest) (deployment.CostEstimate, error) {
	slog.Info("Estimating costs for request", "request", cr)

	ce := deployment.CostEstimate{Services: make([]deployment.CostService, 0, len(cr.Services))}
	ce.Total = 0.0

	pricingProvider := NewPricingProvider()
	pricingService := pricing.NewPricingService(pricingProvider, pricing.DefaultRetries, hda.llmClient)

	for _, service := range cr.Services {
		result, err := pricingService.EstimateCost(ctx, service)
		if err != nil {
			slog.Info("Failed to fetch pricing via LLM, using fallback", "service", service.Name, "error", err)
			return estimateHerokuCostFallback(cr), nil
		}

		service.Cost = result.Cost
		ce.Total += service.Cost
		ce.Services = append(ce.Services, service)
	}

	return ce, nil
}

func estimateHerokuCostFallback(cr deployment.CostRequest) deployment.CostEstimate {
	slog.Info("Using Heroku fallback pricing", "lastUpdated", FallbackPricing.LastFetched.Format("2006-01-02"))

	ce := deployment.CostEstimate{Services: make([]deployment.CostService, 0, len(cr.Services))}
	ce.Total = 0.0

	for _, service := range cr.Services {
		cost := getHerokuFallbackServiceCost(service.Provider, service.Plan)
		service.Cost = cost
		ce.Total += service.Cost
		ce.Services = append(ce.Services, service)
	}
	return ce
}

// validateSpec validates that the deployment spec is suitable for Heroku
func (hda *HerokuDeploymentAdapter) validateSpec(spec *deployment.DeploymentSpec) error {
	if spec == nil {
		return errors.Errorf("deployment spec cannot be nil")
	}

	// Skip validation for rollbacks - we're reverting to a previously working deployment
	if spec.IsRollback {
		return nil
	}

	// Heroku doesn't support static-only deployments (needs a dyno)
	if spec.IsStatic && spec.StartCommand == "" {
		return errors.Errorf("Heroku requires a web process; static sites need a server (e.g., nginx, serve)")
	}

	// Warn about unsupported services
	for _, service := range spec.Services {
		switch service.Provider {
		case "postgresql", "redis", "mysql", "mongodb":
			// Supported
		default:
			slog.Warn("Service may not be directly supported by Heroku addons",
				"service", service.Provider,
				"name", service.Name)
		}
	}

	return nil
}

func getHerokuFallbackServiceCost(provider, plan string) float64 {
	switch provider {
	case "web":
		// Dyno pricing
		if plan == "" {
			plan = "basic" // Default to basic
		}
		if cost, ok := FallbackPricing.Dynos[plan]; ok {
			return cost
		}
		return FallbackPricing.Dynos["basic"] // Default

	case "postgresql":
		// Heroku Postgres pricing
		if plan == "" {
			plan = "essential-0"
		}
		if cost, ok := FallbackPricing.Databases[plan]; ok {
			return cost
		}
		return FallbackPricing.Databases["essential-0"]

	case "redis":
		// Heroku Redis pricing
		if plan == "" {
			plan = "mini"
		}
		if cost, ok := FallbackPricing.Redis[plan]; ok {
			return cost
		}
		return FallbackPricing.Redis["mini"]

	case "mysql":
		// JawsDB MySQL addon pricing (approximation)
		if strings.Contains(plan, "kitefin") {
			return 10.0 // Kitefin shared plan
		}
		return 10.0 // Default

	case "mongodb":
		// MongoLab addon pricing (approximation)
		if strings.Contains(plan, "shared") {
			return 15.0 // Shared plan
		}
		return 15.0 // Default

	default:
		return 0.0
	}
}
