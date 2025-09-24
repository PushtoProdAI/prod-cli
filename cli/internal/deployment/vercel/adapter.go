package vercel

import (
	"context"
	"io"
	"log/slog"

	"github.com/go-errors/errors"

	"github.com/meroxa/prod/cli/internal/deployment"
	"github.com/meroxa/prod/cli/internal/deployment/pricing"
	"github.com/meroxa/prod/cli/internal/output"
)

// VercelDeploymentAdapter implements the DeploymentAdapter interface for Vercel
type VercelDeploymentAdapter struct {
	client         VercelClient
	writer         io.Writer
	pricingService pricing.Service
}

// NewVercelDeploymentAdapter creates a new Vercel deployment adapter
func NewVercelDeploymentAdapter(client VercelClient, writer io.Writer) *VercelDeploymentAdapter {
	if writer == nil {
		writer = output.NewNoOpWriter()
	}
	return &VercelDeploymentAdapter{
		client: client,
		writer: writer,
	}
}

// NewVercelDeploymentAdapterWithPricing creates a new Vercel deployment adapter with custom pricing service
func NewVercelDeploymentAdapterWithPricing(client VercelClient, writer io.Writer, pricingService pricing.Service) *VercelDeploymentAdapter {
	if writer == nil {
		writer = output.NewNoOpWriter()
	}
	return &VercelDeploymentAdapter{
		client:         client,
		writer:         writer,
		pricingService: pricingService,
	}
}

// NewDefaultVercelDeploymentAdapter creates a deployment adapter with the default CLI client
func NewDefaultVercelDeploymentAdapter(writer io.Writer) *VercelDeploymentAdapter {
	return NewVercelDeploymentAdapter(NewCLIVercelClient(), writer)
}

// SupportedStrategies returns the deployment strategies supported by Vercel
func (v *VercelDeploymentAdapter) SupportedStrategies() []deployment.DeploymentStrategy {
	return []deployment.DeploymentStrategy{
		deployment.StrategyVercel,
	}
}

// GenerateArtifacts generates deployment artifacts for the specified strategy
func (v *VercelDeploymentAdapter) GenerateArtifacts(spec *deployment.DeploymentSpec, strategy deployment.DeploymentStrategy) (deployment.Deployable, error) {
	// Validate that this is appropriate for Vercel
	if err := v.validateSpec(spec); err != nil {
		return nil, err
	}

	// Vercel supports static and serverless deployments
	if strategy != deployment.StrategyVercel {
		return nil, errors.Errorf("unsupported strategy for Vercel: %s", strategy)
	}

	// Use the queued deployment pattern for better visibility and control
	return NewVercelQueuedDeployment(v.client, spec, v.writer), nil
}

// EstimateCost estimates the cost of deployment on Vercel
func (v *VercelDeploymentAdapter) EstimateCost(spec *deployment.DeploymentSpec, strategy deployment.DeploymentStrategy) (deployment.CostEstimate, error) {
	slog.Info("Estimating costs for Vercel deployment", "name", spec.Name)

	// Build cost request from deployment spec
	cr := deployment.CostRequest{Services: make([]deployment.CostService, 0)}

	// Add services from spec
	for _, service := range spec.Services {
		cs := deployment.CostService{}
		switch service.Provider {
		case "postgresql", "redis", "mysql", "mongodb":
			// Vercel doesn't provide databases directly, skip them
			continue
		default:
			// For other services, add them to the cost estimation
			cs.Service = service
			cs.Plan = "hobby" // Default to hobby tier
		}
		cr.Services = append(cr.Services, cs)
	}

	// Add a service representing the web application hosting
	cs := deployment.CostService{
		Service: deployment.Service{
			Name:     "web-app",
			Provider: "vercel",
		},
		Plan: "hobby", // Default to hobby tier
	}
	cr.Services = append(cr.Services, cs)

	ce, err := v.estimateVercelCost(cr)
	return ce, err
}

func (v *VercelDeploymentAdapter) estimateVercelCost(cr deployment.CostRequest) (deployment.CostEstimate, error) {
	slog.Info("Estimating Vercel costs for request", "request", cr)

	ctx := context.Background()
	ce := deployment.CostEstimate{Services: make([]deployment.CostService, 0, len(cr.Services))}
	ce.Total = 0.0

	var pricingService pricing.Service
	if v.pricingService != nil {
		// Use injected pricing service (for testing)
		pricingService = v.pricingService
	} else {
		// Create pricing service with Vercel pricing provider (production)
		pricingProvider := NewPricingProvider()
		pricingService = pricing.NewPricingService(pricingProvider, pricing.DefaultRetries)
	}

	for _, service := range cr.Services {
		result, err := pricingService.EstimateCost(ctx, service)
		if err != nil {
			slog.Info("Failed to fetch pricing via LLM, using fallback", "service", service.Name, "error", err)
			return estimateVercelCostFallback(cr)
		}

		// Apply usage-based costs for storage (Vercel specific logic)
		service.Cost = pricing.ApplyUsageCosts(result.Cost, result.UsageCosts, float64(service.Storage), "GB")

		ce.Total += service.Cost
		ce.Services = append(ce.Services, service)
	}

	return ce, nil
}

func estimateVercelCostFallback(cr deployment.CostRequest) (deployment.CostEstimate, error) {
	slog.Info("Using fallback pricing for Vercel cost estimation")

	// Fallback pricing when LLM is unavailable
	ce := deployment.CostEstimate{Services: make([]deployment.CostService, 0, len(cr.Services))}
	ce.Total = 0.0

	for _, service := range cr.Services {
		// Default to hobby tier pricing for most services
		service.Cost = 0.0

		// Add some basic pricing for premium features
		if service.Provider == "vercel" {
			switch service.Plan {
			case "hobby":
				service.Cost = 0.0 // Hobby is free
			case "pro":
				service.Cost = 20.0 // Pro plan pricing
			case "enterprise":
				service.Cost = 400.0 // Enterprise starting price
			default:
				service.Cost = 0.0
			}
		}

		ce.Total += service.Cost
		ce.Services = append(ce.Services, service)
	}

	slog.Info("Fallback cost estimate", "total", ce.Total, "services", ce.Services)
	return ce, nil
}

// validateSpec validates that the deployment spec is suitable for Vercel
func (v *VercelDeploymentAdapter) validateSpec(spec *deployment.DeploymentSpec) error {
	// Check for unsupported services
	for _, service := range spec.Services {
		switch service.Provider {
		case "postgresql", "redis", "mysql", "mongodb":
			slog.Info("Note: Vercel does not provide database hosting. You'll need to use external database services", "provider", service.Provider)
			// Don't return an error, just log a note
		}
	}

	// Vercel supports both static sites and serverless functions
	// No additional validation needed
	return nil
}
