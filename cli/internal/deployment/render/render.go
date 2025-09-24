package render

import (
	"context"
	"io"
	"log/slog"
	"time"

	"github.com/go-errors/errors"

	"github.com/meroxa/prod/cli/internal/deployment"
	"github.com/meroxa/prod/cli/internal/deployment/pricing"
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
	writer          io.Writer
}

func NewRenderDeploymentAdapter(client RenderClient, writer io.Writer) *RenderDeploymentAdapter {
	if writer == nil {
		writer = output.NewNoOpWriter()
	}
	return &RenderDeploymentAdapter{
		client:          client,
		dockerGenerator: deployment.NewDockerGenerator(writer, []deployment.EnvVar{}),
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
		return nil, errors.Errorf("unsupported strategy: %s", strategy)
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
	slog.Info("Estimating costs for spec", "spec", spec, "strategy", strategy)
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
	slog.Info("Estimating costs for request", "request", cr)

	ctx := context.Background()
	ce := deployment.CostEstimate{Services: make([]deployment.CostService, 0, len(cr.Services))}
	ce.Total = 0.0

	// Create pricing service with Render pricing provider
	pricingProvider := NewPricingProvider()
	pricingService := pricing.NewPricingService(pricingProvider, pricing.DefaultRetries)

	for _, service := range cr.Services {
		result, err := pricingService.EstimateCost(ctx, service)
		if err != nil {
			slog.Info("Failed to fetch pricing via LLM, using fallback", "service", service.Name, "error", err)
			return estimateCostFallback(cr)
		}

		// Apply usage-based costs for storage (Render specific logic)
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
