package deployment

import (
	"context"
	"fmt"

	"github.com/go-errors/errors"
	"github.com/meroxa/prod/cli/internal/analyzer"
	"github.com/meroxa/prod/cli/internal/cache"
)

type DeploymentStrategy string

const (
	// StrategyTerraform  DeploymentStrategy = "terraform"
	// StrategyHelm       DeploymentStrategy = "helm"

	// Platform-specific strategies
	StrategyRenderBlueprint DeploymentStrategy = "render_blueprint"
	StrategyRenderQueued    DeploymentStrategy = "render_queued"

	StrategyFlyio DeploymentStrategy = "flyio"

	StrategyNetlify DeploymentStrategy = "netlify"

	StrategyVercel DeploymentStrategy = "vercel"

	StrategyHeroku DeploymentStrategy = "heroku"
)

type CreatedResource struct {
	ID       string
	Type     string
	Name     string
	Metadata map[string]interface{} // Additional metadata about the resource
}

type Deployable interface {
	Deploy(ctx context.Context) ([]CreatedResource, error)
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
	Name             string
	Language         string
	Services         []Service
	Metadata         map[string]any
	BuildCommand     string
	StartCommand     string
	MigrationCommand string
	EnvVars          []EnvVar
	OutputDir        string
	IsStatic         bool
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

type EnvVar struct {
	Name    string
	Value   string
	Role    string
	Service string
}

const (
	EnvRoleFullURI       = "full_uri"        // complete database connection URL
	EnvRoleHostname      = "hostname"        // host or server address for the database
	EnvRolePort          = "port"            // database port number
	EnvRoleUsername      = "username"        // database username
	EnvRolePassword      = "password"        // database password or auth token
	EnvRoleDatabaseName  = "database_name"   // logical DB name
	EnvRoleOtherDBConfig = "other_db_config" // database-related but not fitting above categories
	EnvRoleNotDBRelated  = "not_db_related"  // unrelated to databases
)

// IsDBRelated returns true if the environment variable role is database-related
func (e EnvVar) IsDBRelated() bool {
	return e.Role != EnvRoleNotDBRelated
}

// IsNotDBRelated returns true if the environment variable role is not database-related
func (e EnvVar) IsNotDBRelated() bool {
	return e.Role == EnvRoleNotDBRelated
}

func (ds *DeploymentSpec) ServiceCounts() map[string]int {
	counts := make(map[string]int)
	for _, service := range ds.Services {
		counts[service.Provider]++
	}
	return counts
}

type DeploymentBuilder struct {
	projectSpec    *analyzer.ProjectSpec
	serviceEnvVars []EnvVar
}

func NewDeploymentBuilder(projectSpec *analyzer.ProjectSpec, serviceEnvVars []EnvVar) *DeploymentBuilder {
	return &DeploymentBuilder{
		projectSpec:    projectSpec,
		serviceEnvVars: serviceEnvVars,
	}
}

func (db *DeploymentBuilder) Build() (*DeploymentSpec, error) {
	if db.projectSpec == nil {
		return nil, errors.Errorf("project spec is required")
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

	// Determine if it's a static app (has build command, no start command, and has output directory)
	isStatic := db.projectSpec.BuildCommand != "" && db.projectSpec.StartCommand == "" && db.projectSpec.BuildOutput.Path != ""

	return &DeploymentSpec{
		Name:     db.projectSpec.Name,
		Language: db.projectSpec.Language,
		Services: services,
		Metadata: map[string]any{
			"source": "project-analysis",
		},
		BuildCommand:     db.projectSpec.BuildCommand,
		StartCommand:     db.projectSpec.StartCommand,
		MigrationCommand: db.projectSpec.MigrationCommand,
		EnvVars:          db.serviceEnvVars,
		OutputDir:        db.projectSpec.BuildOutput.Path,
		IsStatic:         isStatic,
	}, nil
}

// FetchURLAsMarkdown fetches a URL and converts it to markdown with caching
func FetchURLAsMarkdown(url string) (string, error) {
	return cache.FetchURLAsMarkdown(url)
}

// FetchURLAsMarkdownWithCleaning fetches a URL and converts it to markdown with HTML cleaning
func FetchURLAsMarkdownWithCleaning(url string) (string, error) {
	return cache.FetchURLAsMarkdownWithOptions(url, true)
}
