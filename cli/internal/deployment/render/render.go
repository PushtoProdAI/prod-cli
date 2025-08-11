package render

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/meroxa/prod/cli/baml_client"
	"github.com/meroxa/prod/cli/internal/deployment"
	"github.com/meroxa/prod/cli/internal/output"
)

const (
	postgresPlan     = "basic_256mb"
	postgresVersion  = "16"
	postgresDiskSize = 15
	postgresRegion   = "virginia"
	redisPlan        = "standard"
	webServicePlan   = "standard"
	webServiceRegion = "virginia"
)

type RenderPricing struct {
	WebServices map[string]float64 `json:"web_services"`
	Databases   map[string]float64 `json:"databases"`
	Redis       map[string]float64 `json:"redis"`
	Storage     float64            `json:"storage_per_gb"`
	LastFetched time.Time          `json:"last_fetched"`
}

var fallbackPricing = RenderPricing{
	WebServices: map[string]float64{
		"free":      0.0,
		"starter":   7.0,
		"standard":  25.0,
		"pro":       85.0,
		"pro_plus":  175.0,
		"pro_max":   225.0,
		"pro_ultra": 450.0,
	},
	Databases: map[string]float64{
		"basic_256mb":  7.0,
		"basic_1gb":    20.0,
		"standard_2gb": 50.0,
		"pro_4gb":      120.0,
		"pro_8gb":      200.0,
		"pro_16gb":     350.0,
	},
	Redis: map[string]float64{
		"standard": 25.0,
		"pro":      85.0,
		"pro_plus": 175.0,
	},
	Storage:     0.25,
	LastFetched: time.Date(2025, 1, 30, 0, 0, 0, 0, time.UTC),
}

type RenderDeploymentAdapter struct {
	client          RenderClient
	dockerGenerator *deployment.DockerGenerator
	writer          output.Writer
}

func NewRenderDeploymentAdapter(client RenderClient, writer output.Writer) *RenderDeploymentAdapter {
	if writer == nil {
		writer = output.NewNoOpWriter()
	}
	return &RenderDeploymentAdapter{
		client:          client,
		dockerGenerator: deployment.NewDockerGenerator(writer),
		writer:          writer,
	}
}

func (rda *RenderDeploymentAdapter) SupportedStrategies() []deployment.DeploymentStrategy {
	return []deployment.DeploymentStrategy{
		deployment.StrategyRenderBlueprint,
		deployment.StrategyRenderQueued,
	}
}

func (rda *RenderDeploymentAdapter) GenerateArtifacts(spec *deployment.DeploymentSpec, strategy deployment.DeploymentStrategy) (deployment.Deployable, error) {
	// Determine whether to use Docker based on language support and strategy
	useDockerfile := rda.shouldUseDockerfile(spec, strategy)

	switch strategy {
	case deployment.StrategyRenderBlueprint:
		deployment := NewBlueprintDeployment(rda.client, spec, rda.dockerGenerator, useDockerfile, rda.writer)
		return deployment, nil
	case deployment.StrategyRenderQueued:
		deployment := NewQueuedDeployment(rda.client, spec, rda.dockerGenerator, useDockerfile, rda.writer)
		return deployment, nil
	default:
		return nil, fmt.Errorf("unsupported strategy: %s", strategy)
	}
}

// shouldUseDockerfile determines whether to use Docker based on various factors
func (rda *RenderDeploymentAdapter) shouldUseDockerfile(spec *deployment.DeploymentSpec, _ deployment.DeploymentStrategy) bool {
	// For now, use a simple heuristic:
	// - Use Docker if the language has good native Render support
	// - Or if there are complex service dependencies
	// - Or if custom build/start commands suggest complex setup

	// Languages with good native Render support
	nativeLanguages := map[string]bool{
		"node":       true,
		"nodejs":     true,
		"javascript": true,
		"python":     true,
		"go":         true,
		"golang":     true,
	}

	hasNativeSupport := nativeLanguages[spec.Language]
	hasComplexServices := len(spec.Services) > 1
	hasCustomCommands := spec.BuildCommand != "" || spec.StartCommand != ""

	// Use Docker if:
	// - Language doesn't have native support, OR
	// - Has complex service dependencies, OR
	// - Has custom build commands that might be complex
	return !hasNativeSupport || hasComplexServices || hasCustomCommands
}

func (rda *RenderDeploymentAdapter) EstimateCost(spec *deployment.DeploymentSpec, strategy deployment.DeploymentStrategy) (deployment.CostEstimate, error) {
	log.Printf("Estimating costs for spec: %+v with strategy: %s\n", spec, strategy)
	cr := deployment.CostRequest{Services: make([]deployment.CostService, 0)}
	for _, service := range spec.Services {
		cs := deployment.CostService{}
		switch service.Provider {
		case "postgresql":
			cs.Service = service
			cs.Plan = postgresPlan
			cs.Storage = postgresDiskSize
		case "redis":
			cs.Service = service
			cs.Plan = redisPlan
		default:
			continue
		}
		cr.Services = append(cr.Services, cs)
	}
	// add a service representing the web service
	cs := deployment.CostService{
		Service: deployment.Service{
			Name:     "web",
			Provider: "web",
		},
		Plan: webServicePlan,
	}
	cr.Services = append(cr.Services, cs)
	ce, _ := estimateCost(cr)
	return ce, nil
}

func estimateCost(cr deployment.CostRequest) (deployment.CostEstimate, error) {
	log.Printf("Estimating costs for request: %+v\n", cr)

	// Get current pricing from Render via LLM
	pricing, err := fetchPricingViaLLM(cr.Services)
	if err != nil {
		log.Printf("Failed to fetch pricing via LLM, using fallback: %v\n", err)
		return estimateCostFallback(cr)
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

func fetchPricingViaLLM(services []deployment.CostService) ([]float64, error) {
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
	log.Printf("Fetching pricing via LLM for services:\n%s", servicesText)

	ctx := context.Background()
	response, err := baml_client.FetchRenderPricing(ctx, servicesText)
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

	log.Printf("LLM returned pricing: %v (total: $%.2f)", costs, response.Total_cost)
	return costs, nil
}

func estimateCostFallback(cr deployment.CostRequest) (deployment.CostEstimate, error) {
	log.Printf("Using fallback pricing (last updated: %s)", fallbackPricing.LastFetched.Format("2006-01-02"))

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
		baseCost := fallbackPricing.Databases[plan]
		if baseCost == 0 {
			baseCost = fallbackPricing.Databases["basic_256mb"] // default
		}
		storageCost := float64(storage) * fallbackPricing.Storage
		return baseCost + storageCost
	case "redis":
		cost := fallbackPricing.Redis[plan]
		if cost == 0 {
			cost = fallbackPricing.Redis["standard"] // default
		}
		return cost
	case "web":
		cost := fallbackPricing.WebServices[plan]
		if cost == 0 {
			cost = fallbackPricing.WebServices["standard"] // default
		}
		return cost
	default:
		return 0.0
	}
}
