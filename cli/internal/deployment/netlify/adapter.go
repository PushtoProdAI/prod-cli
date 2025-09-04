package netlify

import (
	"context"
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/meroxa/prod/cli/baml_client"
	"github.com/meroxa/prod/cli/internal/deployment"
	"github.com/meroxa/prod/cli/internal/output"
)

// NetlifyDeploymentAdapter implements the DeploymentAdapter interface for Netlify
type NetlifyDeploymentAdapter struct {
	client NetlifyClient
	writer io.Writer
}

// NewNetlifyDeploymentAdapter creates a new Netlify deployment adapter
func NewNetlifyDeploymentAdapter(client NetlifyClient, writer io.Writer) *NetlifyDeploymentAdapter {
	if writer == nil {
		writer = output.NewNoOpWriter()
	}
	return &NetlifyDeploymentAdapter{
		client: client,
		writer: writer,
	}
}

// NewDefaultNetlifyDeploymentAdapter creates a deployment adapter with the default CLI client
func NewDefaultNetlifyDeploymentAdapter(writer io.Writer) *NetlifyDeploymentAdapter {
	return NewNetlifyDeploymentAdapter(NewCLINetlifyClient(), writer)
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
		return nil, fmt.Errorf("unsupported strategy for Netlify: %s", strategy)
	}

	// Use the queued deployment pattern for better visibility and control
	return NewNetlifyQueuedDeployment(n.client, spec, n.writer), nil
}

// EstimateCost estimates the cost of deployment on Netlify
func (n *NetlifyDeploymentAdapter) EstimateCost(spec *deployment.DeploymentSpec, strategy deployment.DeploymentStrategy) (deployment.CostEstimate, error) {
	log.Printf("Estimating costs for Netlify deployment: %s", spec.Name)

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

	ce, err := estimateNetlifyCost(cr)
	return ce, err
}

func estimateNetlifyCost(cr deployment.CostRequest) (deployment.CostEstimate, error) {
	log.Printf("Estimating Netlify costs for %d services", len(cr.Services))

	// Get current pricing from Netlify via LLM
	pricing, err := fetchNetlifyPricingViaLLM(cr.Services)
	if err != nil {
		log.Printf("Failed to fetch pricing via LLM, using fallback: %v", err)
		return estimateNetlifyCostFallback(cr)
	}

	log.Printf("Successfully fetched pricing via LLM: %+v", pricing)

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

	log.Printf("Final cost estimate: Total=%.2f, Services=%+v", ce.Total, ce.Services)

	return ce, nil
}

func fetchNetlifyPricingViaLLM(services []deployment.CostService) ([]float64, error) {
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
	log.Printf("Fetching Netlify pricing via LLM for services:\n%s", servicesText)

	ctx := context.Background()
	response, err := baml_client.FetchNetlifyPricing(ctx, servicesText)
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

	return costs, nil
}

func estimateNetlifyCostFallback(cr deployment.CostRequest) (deployment.CostEstimate, error) {
	log.Printf("Using fallback pricing for Netlify cost estimation")

	// Fallback pricing when LLM is unavailable
	ce := deployment.CostEstimate{Services: make([]deployment.CostService, 0, len(cr.Services))}
	ce.Total = 0.0

	for _, service := range cr.Services {
		// Default to free tier for most services
		service.Cost = 0.0

		// Add some basic pricing for premium features
		if service.Service.Provider == "functions" && service.Plan != "free" {
			service.Cost = 19.0 // Pro plan pricing
		} else if service.Service.Provider == "forms" && service.Plan != "free" {
			service.Cost = 19.0 // Pro plan pricing
		}

		ce.Total += service.Cost
		ce.Services = append(ce.Services, service)
	}

	log.Printf("Fallback cost estimate: Total=%.2f, Services=%+v", ce.Total, ce.Services)
	return ce, nil
}

// validateSpec validates that the deployment spec is suitable for Netlify
func (n *NetlifyDeploymentAdapter) validateSpec(spec *deployment.DeploymentSpec) error {
	// Check for unsupported services
	for _, service := range spec.Services {
		switch service.Provider {
		case "postgresql", "redis", "mysql", "mongodb":
			return fmt.Errorf("Netlify does not support %s hosting. Netlify is for static sites and serverless functions only", service.Provider)
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
			log.Printf("Warning: Netlify is designed for static sites. Start command '%s' suggests a backend service which won't work on Netlify", spec.StartCommand)
		}
	}

	return nil
}
