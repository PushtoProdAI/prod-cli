package render

import "context"

// RenderAPIStep interface for all deployment steps
type RenderAPIStep interface {
	Execute(ctx context.Context, client RenderClient, stepResults map[string]interface{}) (interface{}, error)
	Rollback(ctx context.Context, client RenderClient, stepResults map[string]interface{}) error
	GetID() string
	GetDescription() string
	GetDependencies() []string
}

// BaseStep provides common functionality for all steps
type BaseStep struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	DependsOn   []string `json:"dependsOn,omitempty"`
}

func (b *BaseStep) GetID() string {
	return b.ID
}

func (b *BaseStep) GetDescription() string {
	return b.Description
}

func (b *BaseStep) GetDependencies() []string {
	return b.DependsOn
}

// Legacy struct for backward compatibility during transition
type LegacyRenderAPIStep struct {
	ID          string         `json:"id"`
	Description string         `json:"description"`
	Method      string         `json:"method"`
	Endpoint    string         `json:"endpoint"`
	Payload     map[string]any `json:"payload"`
	DependsOn   []string       `json:"dependsOn,omitempty"`
}

type RenderProject struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
	OwnerID     string `json:"ownerId"`
	TeamID      string `json:"teamId,omitempty"`
	Environment string `json:"environment"`
}

type CreateProjectRequest struct {
	Name        string `json:"name"`
	TeamID      string `json:"teamId,omitempty"`
	Environment string `json:"environment,omitempty"`
}

type CreateWebServiceRequest struct {
	Name         string                   `json:"name"`
	Type         string                   `json:"type"` // "web_service"
	OwnerID      string                   `json:"ownerId"`
	Repo         string                   `json:"repo,omitempty"`
	Branch       string                   `json:"branch,omitempty"`
	BuildCommand string                   `json:"buildCommand,omitempty"`
	StartCommand string                   `json:"startCommand,omitempty"`
	EnvVars      []CreateServiceEnvVar    `json:"envVars,omitempty"`
	ServiceDetails *WebServiceDetails     `json:"serviceDetails,omitempty"`
}

type CreateServiceEnvVar struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type WebServiceDetails struct {
	Env              string `json:"env,omitempty"`              // "docker", "node", "python3", etc.
	BuildFilter      *BuildFilter `json:"buildFilter,omitempty"`
	PublishPath      string `json:"publishPath,omitempty"`      // For static sites
	PullRequestPreviewsEnabled *bool `json:"pullRequestPreviewsEnabled,omitempty"`
}

type BuildFilter struct {
	Paths         []string `json:"paths,omitempty"`
	IgnoredPaths  []string `json:"ignoredPaths,omitempty"`
}

type CreatePostgresRequest struct {
	Name         string `json:"name"`
	OwnerID      string `json:"ownerId"`
	DatabaseName string `json:"databaseName,omitempty"`
	User         string `json:"user,omitempty"`
}

type CreateRedisRequest struct {
	Name    string `json:"name"`
	OwnerID string `json:"ownerId"`
}

type PostgresConnectionInfo struct {
	InternalConnectionString string `json:"internalConnectionString"`
	ExternalConnectionString string `json:"externalConnectionString"`
}

type RedisConnectionInfo struct {
	InternalConnectionString string `json:"internalConnectionString"`
	ExternalConnectionString string `json:"externalConnectionString"`
}

type RenderService struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

type RenderBlueprint struct {
	Services []BlueprintService `json:"services"`
}

type BlueprintService struct {
	Name         string            `json:"name"`
	Type         string            `json:"type"`
	Env          string            `json:"env,omitempty"`
	Repo         string            `json:"repo,omitempty"`
	Branch       string            `json:"branch,omitempty"`
	BuildCommand string            `json:"buildCommand,omitempty"`
	StartCommand string            `json:"startCommand,omitempty"`
	Dockerfile   string            `json:"dockerfile,omitempty"`
	EnvVars      map[string]string `json:"envVars,omitempty"`
	DatabaseName string            `json:"databaseName,omitempty"` // For postgres services
}

type RenderWorkspace struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type RenderClient interface {
	// Workspaces
	ListWorkspaces(ctx context.Context) ([]*RenderWorkspace, error)

	// Projects
	CreateProject(ctx context.Context, req CreateProjectRequest) (*RenderProject, error)
	GetProject(ctx context.Context, projectID string) (*RenderProject, error)
	ListProjects(ctx context.Context) ([]*RenderProject, error)
	DeleteProject(ctx context.Context, projectID string) error

	// Services
	CreateWebService(ctx context.Context, req CreateWebServiceRequest) (*RenderService, error)
	CreatePostgres(ctx context.Context, req CreatePostgresRequest) (*RenderService, error)
	CreateRedis(ctx context.Context, req CreateRedisRequest) (*RenderService, error)

	// Connection Info
	GetPostgresConnectionInfo(ctx context.Context, serviceID string) (*PostgresConnectionInfo, error)
	GetRedisConnectionInfo(ctx context.Context, serviceID string) (*RedisConnectionInfo, error)

	// Blueprint
	DeployBlueprint(ctx context.Context, blueprint *RenderBlueprint) error
}