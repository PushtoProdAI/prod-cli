package deployment

import (
	"context"
	"fmt"

	"github.com/meroxa/prod/cli/internal/analyzer"
)

type DeploymentStrategy string

const (
	// StrategyTerraform  DeploymentStrategy = "terraform"
	// StrategyHelm       DeploymentStrategy = "helm"

	// Platform-specific strategies
	StrategyRenderBlueprint DeploymentStrategy = "render_blueprint"
	StrategyRenderQueued    DeploymentStrategy = "render_queued"
)

type Deployable interface {
	Deploy(ctx context.Context) error
}

type DeploymentAdapter interface {
	SupportedStrategies() []DeploymentStrategy
	GenerateArtifacts(spec *DeploymentSpec, strategy DeploymentStrategy) (Deployable, error)
	EstimateCost(spec *DeploymentSpec, strategy DeploymentStrategy) (CostEstimate, error)
}

type Service struct {
	Type     string
	Name     string
	Config   map[string]any
	Provider string
}

type DeploymentSpec struct {
	Name         string
	Language     string
	Services     []Service
	Metadata     map[string]any
	BuildCommand string
	StartCommand string
}

type CostService struct {
	Service
	Plan    string
	Storage int
	Cost    float64
}

type CostEstimate struct {
	Total    float64
	Services []CostService
}

type CostRequest struct {
	BasePlan string
	Platform string
	Services []CostService
}

func (ds *DeploymentSpec) ServiceCounts() map[string]int {
	counts := make(map[string]int)
	for _, service := range ds.Services {
		counts[service.Provider]++
	}
	return counts
}

type DeploymentBuilder struct {
	projectSpec *analyzer.ProjectSpec
}

func NewDeploymentBuilder(projectSpec *analyzer.ProjectSpec) *DeploymentBuilder {
	return &DeploymentBuilder{
		projectSpec: projectSpec,
	}
}

func (db *DeploymentBuilder) Build() (*DeploymentSpec, error) {
	if db.projectSpec == nil {
		return nil, fmt.Errorf("project spec is required")
	}

	services := make([]Service, 0, len(db.projectSpec.ServiceRequirements))
	serviceCount := make(map[string]int)

	for _, req := range db.projectSpec.ServiceRequirements {
		serviceCount[req.Provider]++

		service := Service{
			Type:     req.Provider,
			Name:     fmt.Sprintf("%s-%d", req.Provider, serviceCount[req.Provider]),
			Config:   make(map[string]any),
			Provider: req.Provider,
		}

		services = append(services, service)
	}

	return &DeploymentSpec{
		Name:     db.projectSpec.Name,
		Language: db.projectSpec.Language,
		Services: services,
		Metadata: map[string]any{
			"source": "project-analysis",
		},
		BuildCommand: db.projectSpec.BuildCommand,
		StartCommand: db.projectSpec.StartCommand,
	}, nil
}
