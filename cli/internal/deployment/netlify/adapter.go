package netlify

import (
	"context"
	"io"
	"log/slog"

	"github.com/go-errors/errors"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
	"github.com/pushtoprodai/prod-cli/internal/deployment/pricing"
	"github.com/pushtoprodai/prod-cli/internal/llm"
	"github.com/pushtoprodai/prod-cli/internal/output"
)

// NetlifyDeploymentAdapter implements the DeploymentAdapter interface for Netlify
type NetlifyDeploymentAdapter struct {
	client    NetlifyClient
	writer    io.Writer
	llmClient llm.Client
}

// NewNetlifyDeploymentAdapter creates a new Netlify deployment adapter
func NewNetlifyDeploymentAdapter(client NetlifyClient, writer io.Writer, llmClient llm.Client) *NetlifyDeploymentAdapter {
	if writer == nil {
		writer = output.NewNoOpWriter()
	}
	return &NetlifyDeploymentAdapter{
		client:    client,
		writer:    writer,
		llmClient: llmClient,
	}
}

// NewDefaultNetlifyDeploymentAdapter creates a deployment adapter with the default CLI client
func NewDefaultNetlifyDeploymentAdapter(writer io.Writer, llmClient llm.Client) *NetlifyDeploymentAdapter {
	return NewNetlifyDeploymentAdapter(NewCLINetlifyClient(), writer, llmClient)
}

// SupportedStrategies returns the deployment strategies supported by Netlify
func (n *NetlifyDeploymentAdapter) SupportedStrategies() []deployment.DeploymentStrategy {
	return []deployment.DeploymentStrategy{
		deployment.StrategyNetlify,
	}
}

// GenerateArtifacts generates deployment artifacts for the specified strategy
func (n *NetlifyDeploymentAdapter) GenerateArtifacts(spec *deployment.DeploymentSpec, strategy deployment.DeploymentStrategy) (deployment.Deployable, error) {
	// Validate that this is appropriate for Netlify
	if err := n.validateSpec(spec); err != nil {
		return nil, err
	}

	// Netlify only supports static deployments
	if strategy != deployment.StrategyNetlify {
		return nil, errors.Errorf("unsupported strategy for Netlify: %s", strategy)
	}

	// Use the queued deployment pattern for better visibility and control
	return NewNetlifyQueuedDeployment(n.client, spec, n.writer), nil
}

// EstimateCost estimates the cost of deployment on Netlify
func (n *NetlifyDeploymentAdapter) EstimateCost(ctx context.Context, spec *deployment.DeploymentSpec, strategy deployment.DeploymentStrategy) (deployment.CostEstimate, error) {
	slog.Info("Estimating costs for Netlify deployment", "name", spec.Name)

	// Build cost request from deployment spec
	cr := deployment.CostRequest{Services: make([]deployment.CostService, 0)}

	// Add services from spec
	for _, service := range spec.Services {
		cs := deployment.CostService{}
		switch service.Provider {
		case "postgresql", "redis", "mysql", "mongodb":
			// Netlify doesn't support these services, skip them
			continue
		default:
			// For other services, add them to the cost estimation
			cs.Service = service
			cs.Plan = "free" // Default to free tier
		}
		cr.Services = append(cr.Services, cs)
	}

	// Add a service representing the static site hosting
	cs := deployment.CostService{
		Service: deployment.Service{
			Name:     "static-site",
			Provider: "netlify",
		},
		Plan: "free", // Default to free tier
	}
	cr.Services = append(cr.Services, cs)

	ce, err := n.estimateNetlifyCost(ctx, cr)
	return ce, err
}

func (n *NetlifyDeploymentAdapter) estimateNetlifyCost(ctx context.Context, cr deployment.CostRequest) (deployment.CostEstimate, error) {
	slog.Info("Estimating Netlify costs", "serviceCount", len(cr.Services))

	ce := deployment.CostEstimate{Services: make([]deployment.CostService, 0, len(cr.Services))}
	ce.Total = 0.0

	// Create pricing service with Netlify pricing provider
	pricingProvider := NewPricingProvider()
	pricingService := pricing.NewPricingService(pricingProvider, 3, n.llmClient)

	for _, service := range cr.Services {
		result, err := pricingService.EstimateCost(ctx, service)
		if err != nil {
			slog.Info("Failed to fetch pricing via LLM, using fallback", "service", service.Name, "error", err)
			return estimateNetlifyCostFallback(cr)
		}

		// Apply usage-based costs for storage (Netlify specific logic)
		service.Cost = pricing.ApplyUsageCosts(result.Cost, result.UsageCosts, float64(service.Storage), "GB")

		ce.Total += service.Cost
		ce.Services = append(ce.Services, service)
	}

	slog.Info("Final cost estimate", "total", ce.Total, "services", ce.Services)

	return ce, nil
}

func estimateNetlifyCostFallback(cr deployment.CostRequest) (deployment.CostEstimate, error) {
	slog.Info("Using fallback pricing for Netlify cost estimation")

	// Fallback pricing when LLM is unavailable
	ce := deployment.CostEstimate{Services: make([]deployment.CostService, 0, len(cr.Services))}
	ce.Total = 0.0

	for _, service := range cr.Services {
		// Default to free tier for most services
		service.Cost = 0.0

		// Add some basic pricing for premium features
		if service.Provider == "functions" && service.Plan != "free" {
			service.Cost = 19.0 // Pro plan pricing
		} else if service.Provider == "forms" && service.Plan != "free" {
			service.Cost = 19.0 // Pro plan pricing
		}

		ce.Total += service.Cost
		ce.Services = append(ce.Services, service)
	}

	slog.Info("Fallback cost estimate", "total", ce.Total, "services", ce.Services)
	return ce, nil
}

// validateSpec validates that the deployment spec is suitable for Netlify
func (n *NetlifyDeploymentAdapter) validateSpec(spec *deployment.DeploymentSpec) error {
	// Check for unsupported services
	for _, service := range spec.Services {
		switch service.Provider {
		case "postgresql", "redis", "mysql", "mongodb":
			return errors.Errorf("netlify does not support %s hosting. Netlify is for static sites and serverless functions only", service.Provider)
		}
	}

	// Check if this appears to be a backend service
	if spec.StartCommand != "" {
		// Check if it's a static site generator
		knownStaticCommands := []string{"next export", "nuxt generate", "gatsby build", "hugo", "jekyll build"}
		isStatic := false
		for _, cmd := range knownStaticCommands {
			if spec.StartCommand == cmd || spec.BuildCommand == cmd {
				isStatic = true
				break
			}
		}

		if !isStatic && spec.StartCommand != "" {
			slog.Info("Warning: Netlify is designed for static sites", "startCommand", spec.StartCommand)
		}
	}

	return nil
}
