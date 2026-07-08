package deployment

import (
	"context"
	"fmt"

	"github.com/go-errors/errors"
	"github.com/pushtoprodai/prod-cli/internal/analyzer"
	"github.com/pushtoprodai/prod-cli/internal/cache"
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

	StrategyAWS DeploymentStrategy = "aws"
)

type CreatedResource struct {
	ID       string
	Type     string
	Name     string
	Primary  bool           // the service that carries the deploy's public URL (Metadata["url"])
	Metadata map[string]any // Additional metadata about the resource
}

type Deployable interface {
	Deploy(ctx context.Context) ([]CreatedResource, error)
	GetPreviousDeployment(ctx context.Context) (*DeploymentInfo, error)
	Rollback(ctx context.Context, targetDeploymentID string) error
}

// Destroyer is an optional capability: a Deployable that can tear down its
// deployment (the service and its provisioned resources). Not every platform
// supports it yet; the destroy path checks for this interface and reports clearly
// when a platform doesn't implement it.
type Destroyer interface {
	Destroy(ctx context.Context) error
}

type DeploymentInfo struct {
	ID        string
	Status    string
	CreatedAt string
	URL       string
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
	Name              string
	Language          string
	Services          []Service
	Metadata          map[string]any
	BuildCommand      string
	StartCommand      string
	MigrationCommand  string
	EnvVars           []EnvVar
	OutputDir         string
	IsStatic          bool
	IsUpdate          bool
	IsRollback        bool
	ExistingProjectID string
	ExistingDatabases []string
	// Shape selects the artifact an adapter generates (a web service vs a portless
	// worker vs a scheduled job) — not just the liveness strategy. Empty means ShapeWeb
	// (via HTTPShaped), so existing web deploys are unchanged.
	Shape DeployShape
	// Schedule is a 5-field cron expression, set only for ShapeCron on platforms that
	// support scheduled jobs (e.g. a Render cron_job). Empty otherwise.
	Schedule string
	// ExplicitName is true when the user pinned the app name (--name) — for CI/per-PR
	// deploys. It must be honored exactly: a name collision fails loudly rather than being
	// silently auto-renamed (which would fork an unmanaged, orphaned app in CI).
	ExplicitName bool
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
	Name              string
	Value             string
	Role              string
	Service           string
	Sensitive         bool
	SensitivityReason string
}

const (
	EnvRoleFullURI       = "full_uri"        // complete database connection URL
	EnvRoleHostname      = "hostname"        // host or server address for the database
	EnvRolePort          = "port"            // database port number
	EnvRoleUsername      = "username"        // database username
	EnvRolePassword      = "password"        // database password or auth token
	EnvRoleDatabaseName  = "database_name"   // logical DB name
	EnvRoleOtherDBConfig = "other_db_config" // database-related but not fitting above categories

	// Redis/Cache-specific roles
	EnvRoleRedisURI         = "redis_uri"          // complete Redis connection URL
	EnvRoleRedisHost        = "redis_host"         // Redis server hostname
	EnvRoleRedisPort        = "redis_port"         // Redis server port
	EnvRoleRedisPassword    = "redis_password"     // Redis authentication password
	EnvRoleOtherRedisConfig = "other_redis_config" // Redis-related but not fitting above categories

	EnvRoleNotDBRelated = "not_db_related" // unrelated to databases or backing services
)

// IsDBRelated returns true if the environment variable role is database-related
func (e EnvVar) IsDBRelated() bool {
	return e.Role != EnvRoleNotDBRelated && !e.IsRedisRelated()
}

// IsRedisRelated returns true if the environment variable role is Redis-related
func (e EnvVar) IsRedisRelated() bool {
	return e.Role == EnvRoleRedisURI ||
		e.Role == EnvRoleRedisHost ||
		e.Role == EnvRoleRedisPort ||
		e.Role == EnvRoleRedisPassword ||
		e.Role == EnvRoleOtherRedisConfig
}

// IsBackingServiceRelated returns true if the variable is related to any backing service (DB or Redis)
func (e EnvVar) IsBackingServiceRelated() bool {
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
	shape          DeployShape
}

// NewDeploymentBuilder builds a DeploymentSpec from an analyzed project. shape is the
// resolved deploy shape (from DeployPlan.Shape — the LLM's classification with the
// analyzer's code-signal override already applied); pass ShapeWeb (or "") for the
// default web behavior.
func NewDeploymentBuilder(projectSpec *analyzer.ProjectSpec, serviceEnvVars []EnvVar, shape DeployShape) *DeploymentBuilder {
	return &DeploymentBuilder{
		projectSpec:    projectSpec,
		serviceEnvVars: serviceEnvVars,
		shape:          shape,
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
			Type:     req.Type,
			Name:     fmt.Sprintf("%s-%d", req.Provider, serviceCount[req.Provider]),
			Config:   make(map[string]any),
			Provider: req.Provider,
		}
		services = append(services, service)
	}

	// Determine if it's a static app (has build command, no start command, and has output directory)
	isStatic := db.projectSpec.BuildCommand != "" && db.projectSpec.StartCommand == "" && db.projectSpec.BuildOutput.Path != ""

	// Default an unset shape to web so an adapter's `!spec.Shape.HTTPShaped()` worker
	// branch never misfires on a zero value and drops the HTTP service on a web app.
	shape := db.shape
	if shape == "" {
		shape = ShapeWeb
	}

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
		Shape:            shape,
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
