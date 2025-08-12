package flyio

import (
	"context"
	"time"
)

// FlyioClient interface for all Fly.io API operations
type FlyioClient interface {
	// Apps
	CreateApp(ctx context.Context, req CreateAppRequest) (*FlyioApp, error)
	GetApp(ctx context.Context, appID string) (*FlyioApp, error)
	DeployApp(ctx context.Context, appID string, config *FlyioConfig) error
	DestroyApp(ctx context.Context, appID string) error

	// Databases
	CreatePostgres(ctx context.Context, req CreatePostgresRequest) (*FlyioPostgres, error)
	CreateRedis(ctx context.Context, req CreateRedisRequest) (*FlyioRedis, error)
	GetPostgresConnectionInfo(ctx context.Context, appID string) (*PostgresConnectionInfo, error)
	GetRedisConnectionInfo(ctx context.Context, appID string) (*RedisConnectionInfo, error)
	AttachPostgres(ctx context.Context, req AttachPostgresRequest) error
	AttachRedis(ctx context.Context, req AttachRedisRequest) error

	// Volumes
	CreateVolume(ctx context.Context, req CreateVolumeRequest) (*FlyioVolume, error)

	// Monitoring
	GetAppLogs(ctx context.Context, appID string) ([]LogEntry, error)
	GetAppMetrics(ctx context.Context, appID string) (*AppMetrics, error)
}

// FlyioApp represents a Fly.io application
type FlyioApp struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Region    string `json:"region"`
	CreatedAt int64  `json:"created_at"` // Unix timestamp in milliseconds
	UpdatedAt int64  `json:"updated_at"` // Unix timestamp in milliseconds
}

// CreateAppRequest for creating a new Fly.io app
type CreateAppRequest struct {
	Name    string `json:"name"`
	OrgID   string `json:"org_id"`
	OrgSlug string `json:"org_slug"`
	Region  string `json:"region"`
}

// FlyioConfig represents the configuration for a Fly.io app
type FlyioConfig struct {
	AppName     string            `json:"app_name"`
	SourcePath  string            `json:"source_path,omitempty"` // Path to source code directory
	BuildConfig *BuildConfig      `json:"build_config,omitempty"`
	EnvVars     map[string]string `json:"env_vars,omitempty"`
	Services    []ServiceConfig   `json:"services,omitempty"`
	Volumes     []VolumeConfig    `json:"volumes,omitempty"`
}

// BuildConfig for Fly.io app build configuration
type BuildConfig struct {
	Builder    string `json:"builder,omitempty"`
	BuildCmd   string `json:"build_cmd,omitempty"`
	StartCmd   string `json:"start_cmd,omitempty"`
	Dockerfile string `json:"dockerfile,omitempty"`
}

// ServiceConfig for Fly.io service configuration
type ServiceConfig struct {
	Protocol     string `json:"protocol"`
	InternalPort int    `json:"internal_port"`
	Ports        []Port `json:"ports,omitempty"`
}

// Port configuration for Fly.io services
type Port struct {
	Port     int      `json:"port"`
	Handlers []string `json:"handlers"`
}

// VolumeConfig for Fly.io volume configuration
type VolumeConfig struct {
	Name   string `json:"name"`
	Size   int    `json:"size"`
	Region string `json:"region"`
}

// FlyioPostgres represents a Fly.io PostgreSQL database
type FlyioPostgres struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	AppID  string `json:"app_id"`
}

// CreatePostgresRequest for creating a PostgreSQL database
type CreatePostgresRequest struct {
	AppID  string `json:"app_id"`
	Name   string `json:"name"`
	Region string `json:"region"`
	Size   int    `json:"size"`
}

// FlyioRedis represents a Fly.io Redis database
type FlyioRedis struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	AppID  string `json:"app_id"`
}

// CreateRedisRequest for creating a Redis database
type CreateRedisRequest struct {
	AppID  string `json:"app_id"`
	Name   string `json:"name"`
	Region string `json:"region"`
}

// FlyioVolume represents a Fly.io volume
type FlyioVolume struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Size   int    `json:"size"`
	Region string `json:"region"`
	AppID  string `json:"app_id"`
}

// CreateVolumeRequest for creating a volume
type CreateVolumeRequest struct {
	AppID  string `json:"app_id"`
	Name   string `json:"name"`
	Size   int    `json:"size"`
	Region string `json:"region"`
}

// PostgresConnectionInfo for PostgreSQL connection details
type PostgresConnectionInfo struct {
	InternalConnectionString string `json:"internal_connection_string"`
	ExternalConnectionString string `json:"external_connection_string"`
}

// AttachPostgresRequest for attaching PostgreSQL to an app
type AttachPostgresRequest struct {
	AppName      string `json:"app_name"`
	PostgresName string `json:"postgres_name"`
	DatabaseName string `json:"database_name"`
	VariableName string `json:"variable_name"`
}

// AttachRedisRequest for attaching Redis to an app
type AttachRedisRequest struct {
	AppName      string `json:"app_name"`
	RedisName    string `json:"redis_name"`
	VariableName string `json:"variable_name"`
}

// RedisConnectionInfo for Redis connection details
type RedisConnectionInfo struct {
	InternalConnectionString string `json:"internal_connection_string"`
	ExternalConnectionString string `json:"external_connection_string"`
}

// LogEntry represents a log entry from Fly.io
type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Message   string    `json:"message"`
	Level     string    `json:"level"`
}

// AppMetrics represents application metrics from Fly.io
type AppMetrics struct {
	CPU     float64        `json:"cpu"`
	Memory  float64        `json:"memory"`
	Network NetworkMetrics `json:"network"`
}

// NetworkMetrics for network statistics
type NetworkMetrics struct {
	BytesIn  int64 `json:"bytes_in"`
	BytesOut int64 `json:"bytes_out"`
}

// FlyioAPIStep interface for all deployment steps
type FlyioAPIStep interface {
	Execute(ctx context.Context, client FlyioClient, stepResults map[string]interface{}) (interface{}, error)
	Rollback(ctx context.Context, client FlyioClient, stepResults map[string]interface{}) error
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
