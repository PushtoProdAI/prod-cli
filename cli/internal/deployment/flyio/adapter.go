package flyio

import (
	"context"
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/meroxa/prod/cli/baml_client"
	"github.com/meroxa/prod/cli/internal/deployment"
)

// FlyioDeploymentAdapter implements the DeploymentAdapter interface for Fly.io
type FlyioDeploymentAdapter struct {
	client FlyioClient
	writer io.Writer
}

// NewFlyioDeploymentAdapter creates a new Fly.io deployment adapter
func NewFlyioDeploymentAdapter(client FlyioClient, writer io.Writer) *FlyioDeploymentAdapter {
	return &FlyioDeploymentAdapter{
		client: client,
	}
}

// NewDefaultFlyioDeploymentAdapter creates a deployment adapter with the default client
func NewDefaultFlyioDeploymentAdapter(writer io.Writer) *FlyioDeploymentAdapter {
	return NewFlyioDeploymentAdapter(NewFlyioClient(), writer)
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
		return nil, fmt.Errorf("unsupported deployment strategy for Fly.io: %s (only %s is supported)", strategy, deployment.StrategyFlyio)
	}
	return NewFlyioQueuedDeployment(fda.client, spec, fda.writer), nil
}

// GenerateArtifactsWithSource generates deployment artifacts with source path
func (fda *FlyioDeploymentAdapter) GenerateArtifactsWithSource(spec *deployment.DeploymentSpec, strategy deployment.DeploymentStrategy, sourcePath string) (deployment.Deployable, error) {
	// Only one strategy supported now
	if strategy != deployment.StrategyFlyio {
		return nil, fmt.Errorf("unsupported deployment strategy for Fly.io: %s (only %s is supported)", strategy, deployment.StrategyFlyio)
	}

	// Pass the source path through metadata so it can be used during deployment
	if spec.Metadata == nil {
		spec.Metadata = make(map[string]any)
	}
	spec.Metadata["buildContext"] = sourcePath

	return NewFlyioQueuedDeployment(fda.client, spec, fda.writer), nil
}

// EstimateCost estimates the cost of deployment on Fly.io
func (fda *FlyioDeploymentAdapter) EstimateCost(spec *deployment.DeploymentSpec, strategy deployment.DeploymentStrategy) (deployment.CostEstimate, error) {
	log.Printf("Estimating costs for spec: %+v with strategy: %s\n", spec, strategy)

	// Build cost request from deployment spec
	cr := deployment.CostRequest{Services: make([]deployment.CostService, 0)}

	// Add services from spec
	for _, service := range spec.Services {
		cs := deployment.CostService{}
		switch service.Provider {
		case "postgresql":
			cs.Service = service
			cs.Plan = "db-shared-1" // Default plan
			cs.Storage = 10         // Default 10GB storage
		case "redis":
			cs.Service = service
			cs.Plan = "redis-shared" // Default plan
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

	ce, err := estimateFlyioCost(cr)
	return ce, err
}

func estimateFlyioCost(cr deployment.CostRequest) (deployment.CostEstimate, error) {
	log.Printf("Estimating Fly.io costs for request: %+v\n", cr)

	// Get current pricing from Fly.io via LLM
	pricing, err := fetchFlyioPricingViaLLM(cr.Services)
	if err != nil {
		log.Printf("Failed to fetch pricing via LLM, using fallback: %v\n", err)
		return estimateFlyioCostFallback(cr)
	}

	ce := deployment.CostEstimate{Services: make([]deployment.CostService, 0, len(cr.Services))}
	ce.Total = 0.0

	for i, service := range cr.Services {
		if i < len(pricing) {
			service.Cost = pricing[i]
		} else {
			service.Cost = 0.0
		}
		ce.Total += service.Cost
		ce.Services = append(ce.Services, service)
	}

	return ce, nil
}

func fetchFlyioPricingViaLLM(services []deployment.CostService) ([]float64, error) {
	// Build service descriptions for LLM
	var serviceDescriptions []string
	for _, service := range services {
		desc := fmt.Sprintf("Service: %s, Type: %s, Plan: %s", service.Service.Name, service.Service.Provider, service.Plan)
		if service.Storage > 0 {
			desc += fmt.Sprintf(", Storage: %dGB", service.Storage)
		}
		serviceDescriptions = append(serviceDescriptions, desc)
	}

	servicesText := strings.Join(serviceDescriptions, "\n")
	log.Printf("Fetching Fly.io pricing via LLM for services:\n%s", servicesText)

	ctx := context.Background()
	response, err := baml_client.FetchFlyioPricing(ctx, servicesText)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch pricing via LLM: %v", err)
	}

	// Extract costs in order of input services
	costs := make([]float64, len(services))
	for i, service := range services {
		// Find matching service in response
		found := false
		for _, pricedService := range response.Services {
			if pricedService.Service_name == service.Service.Name || pricedService.Service_type == service.Service.Provider {
				costs[i] = pricedService.Monthly_cost
				found = true
				break
			}
		}
		if !found {
			costs[i] = 0.0
		}
	}

	log.Printf("LLM returned Fly.io pricing: %v (total: $%.2f)", costs, response.Total_cost)
	return costs, nil
}

func estimateFlyioCostFallback(cr deployment.CostRequest) (deployment.CostEstimate, error) {
	log.Printf("Using Fly.io fallback pricing")

	ce := deployment.CostEstimate{Services: make([]deployment.CostService, 0, len(cr.Services))}
	ce.Total = 0.0

	for _, service := range cr.Services {
		cost := getFlyioFallbackServiceCost(service.Provider, service.Plan, service.Storage)
		service.Cost = cost
		ce.Total += service.Cost
		ce.Services = append(ce.Services, service)
	}
	return ce, nil
}

func getFlyioFallbackServiceCost(provider, plan string, storage int) float64 {
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
		return pricing.Databases["db-shared-1"] + (float64(storage) * pricing.Storage) // Default
	case "redis":
		if cost, ok := pricing.Redis[plan]; ok {
			return cost
		}
		return pricing.Redis["redis-shared"] // Default
	default:
		return 0.0
	}
}

// Removed unused helper methods - these are now in blueprint.go and queued.go
// where they're actually used
