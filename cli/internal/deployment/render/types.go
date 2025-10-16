package render

import (
	"context"
)

// RenderAPIStep interface for all deployment steps
type RenderAPIStep interface {
	Execute(ctx context.Context, client RenderClient, stepResults map[string]any) (any, error)
	Rollback(ctx context.Context, client RenderClient, stepResults map[string]any) error
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

type CreateWebServiceRequest struct {
	Name           string                `json:"name"`
	Type           string                `json:"type"` // "web_service"
	OwnerID        string                `json:"ownerId"`
	Repo           string                `json:"repo,omitempty"`
	Branch         string                `json:"branch,omitempty"`
	BuildCommand   string                `json:"buildCommand,omitempty"`
	StartCommand   string                `json:"startCommand,omitempty"`
	Image          *ImageDetails         `json:"image,omitempty"` // For Docker image deployments
	EnvVars        []CreateServiceEnvVar `json:"envVars,omitempty"`
	ServiceDetails *WebServiceDetails    `json:"serviceDetails,omitempty"`
}

type CreateServiceEnvVar struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type ImageDetails struct {
	OwnerID              string `json:"ownerId"`
	RegistryCredentialID string `json:"registryCredentialId"`
	ImagePath            string `json:"imagePath"`
}

type CreateWebServiceResponse struct {
	DeploymentID string        `json:"deployId"`
	Service      RenderService `json:"service"`
}

// For creating registry credentials
type CreateRegistryCredentialRequest struct {
	Name      string `json:"name"`
	Username  string `json:"username"`
	AuthToken string `json:"authToken"`
	Registry  string `json:"registry"`
	OwnerID   string `json:"ownerId"`
}

type RegistryCredential struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	RegistryURL string `json:"registryUrl"`
	UpdatedAt   string `json:"updatedAt"`
}

type UpdateRegistryCredentialRequest struct {
	Username  string `json:"username"`
	AuthToken string `json:"authToken"`
}

type UpdateServiceImageRequest struct {
	ImagePath string `json:"imagePath"`
}

type WebServiceDetails struct {
	Runtime                    string                        `json:"runtime"`
	Plan                       string                        `json:"plan"`
	EnvSpecificDetails         *WebServiceEnvSpecificDetails `json:"envSpecificDetails,omitempty"`
	BuildFilter                *BuildFilter                  `json:"buildFilter,omitempty"`
	PublishPath                string                        `json:"publishPath,omitempty"` // For static sites
	PullRequestPreviewsEnabled *bool                         `json:"pullRequestPreviewsEnabled,omitempty"`
	Region                     string                        `json:"region,omitempty"` // Optional, if not provided, defaults to oregon
	PreDeployCommand           string                        `json:"preDeployCommand,omitempty"`
}

type WebServiceEnvSpecificDetails struct {
	RegistryCredentialID string `json:"registryCredentialId"`
}

type BuildFilter struct {
	Paths        []string `json:"paths,omitempty"`
	IgnoredPaths []string `json:"ignoredPaths,omitempty"`
}

type CreatePostgresRequest struct {
	Name                   string `json:"name"`
	OwnerID                string `json:"ownerId"`
	Plan                   string `json:"plan"`
	Version                string `json:"version"`
	DiskSizeGB             int    `json:"diskSizeGB"`
	Region                 string `json:"region"`
	EnableHighAvailability bool   `json:"enableHighAvailability"`
}

type CreateRedisRequest struct {
	Name    string `json:"name"`
	OwnerID string `json:"ownerId"`
	Plan    string `json:"plan"`
}

type PostgresConnectionInfo struct {
	InternalConnectionString string `json:"internalConnectionString"`
	ExternalConnectionString string `json:"externalConnectionString"`
	Password                 string `json:"password"`
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

type RenderWebService struct {
	RenderService
	ServiceDetails ServiceDetails `json:"serviceDetails"`
}

type RenderDeploy struct {
	ID        string `json:"id"`
	Commit    Commit `json:"commit"`
	Status    string `json:"status"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

type Commit struct {
	ID        string `json:"id"`
	Message   string `json:"message"`
	CreatedAt string `json:"createdAt"`
}

type RenderPostgres struct {
	RenderService
	Status       string `json:"status"`
	DatabaseName string `json:"databaseName"`
	Plan         string `json:"plan"`
	Region       string `json:"region"`
	Version      string `json:"version"`
	DiskSizeGB   int    `json:"diskSizeGB"`
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
}

type ServiceDetails struct {
	URL string `json:"url"`
}

type RenderBlueprint struct {
	Services []BlueprintService `json:"services"`
}

type BlueprintService struct {
	Name             string            `json:"name"`
	Type             string            `json:"type"`
	Env              string            `json:"env,omitempty"`
	Repo             string            `json:"repo,omitempty"`
	Branch           string            `json:"branch,omitempty"`
	BuildCommand     string            `json:"buildCommand,omitempty"`
	StartCommand     string            `json:"startCommand,omitempty"`
	PreDeployCommand string            `json:"preDeployCommand,omitempty"`
	Dockerfile       string            `json:"dockerfile,omitempty"`
	EnvVars          map[string]string `json:"envVars,omitempty"`
	DatabaseName     string            `json:"databaseName,omitempty"` // For postgres services
}

type RenderWorkspace struct {
	Cursor string         `json:"cursor"`
	Owner  WorkspaceOwner `json:"owner"`
}

type WorkspaceOwner struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Type  string `json:"type"`
}

type RenderClient interface {
	// Workspaces
	ListWorkspaces(ctx context.Context) ([]RenderWorkspace, error)

	// Services
	CreateWebService(ctx context.Context, req CreateWebServiceRequest) (*RenderService, error)
	UpdateServiceImage(ctx context.Context, serviceID string, req UpdateServiceImageRequest) error
	CreatePostgres(ctx context.Context, req CreatePostgresRequest) (*RenderService, error)
	CreateRedis(ctx context.Context, req CreateRedisRequest) (*RenderService, error)
	GetWebService(ctx context.Context, serviceID string) (*RenderWebService, error)
	GetPostgres(ctx context.Context, serviceID string) (*RenderPostgres, error)
	ListServices(ctx context.Context, name string) ([]RenderService, error)
	ListPostgres(ctx context.Context) ([]RenderPostgres, error)
	ListRedis(ctx context.Context) ([]RenderService, error)

	// Connection Info
	GetPostgresConnectionInfo(ctx context.Context, serviceID string) (*PostgresConnectionInfo, error)
	GetRedisConnectionInfo(ctx context.Context, serviceID string) (*RedisConnectionInfo, error)

	// Blueprint
	DeployBlueprint(ctx context.Context, blueprint *RenderBlueprint) error

	// Registry Credentials
	ListRegistryCredentials(ctx context.Context, ownerID string) ([]*RegistryCredential, error)
	CreateRegistryCredential(ctx context.Context, req CreateRegistryCredentialRequest) (*RegistryCredential, error)
	UpdateRegistryCredential(ctx context.Context, credID string, req UpdateRegistryCredentialRequest) (*RegistryCredential, error)
	DeleteRegistryCredential(ctx context.Context, credID string) error

	// Deploys
	TriggerDeploy(ctx context.Context, serviceID string) (*RenderDeploy, error)
	GetDeploy(ctx context.Context, serviceID, deployID string) (*RenderDeploy, error)
	ListDeploys(ctx context.Context, serviceID string) ([]*RenderDeploy, error)
}
