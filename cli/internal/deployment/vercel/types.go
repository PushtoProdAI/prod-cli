package vercel

import "time"

// VercelClient defines the interface for interacting with Vercel
type VercelClient interface {
	// Project management
	CreateProject(req CreateProjectRequest) (*VercelProject, error)
	GetProject(projectID string) (*VercelProject, error)
	DeleteProject(projectID string) error
	LinkProject(projectID string) error

	// Project configuration
	PullProject() error

	// Deployment
	DeployProject(projectID string, production bool) (*VercelDeployment, error)
	GetDeployment(deploymentID string) (*VercelDeployment, error)
	PromoteDeployment(deploymentURL, projectName string) error

	// Environment variables
	SetEnvironmentVariables(projectID string, vars map[string]string) error

	// Build settings
	BuildProject(envVars []EnvVar, production bool) error
}

// CreateProjectRequest represents a request to create a new Vercel project
type CreateProjectRequest struct {
	Name      string            `json:"name"`
	Framework string            `json:"framework,omitempty"`
	EnvVars   map[string]string `json:"env,omitempty"`
}

// VercelProject represents a Vercel project
type VercelProject struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	AccountID string    `json:"accountId"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Link      *struct {
		Type   string `json:"type"`
		Repo   string `json:"repo"`
		Branch string `json:"branch,omitempty"`
	} `json:"link,omitempty"`
}

// VercelDeployment represents a deployment on Vercel
type VercelDeployment struct {
	ID            string    `json:"id"`
	URL           string    `json:"url"`           // Production alias URL (for liveness checks)
	DeploymentURL string    `json:"deploymentUrl"` // Deployment-specific URL with hash (for promotion)
	ProjectID     string    `json:"projectId"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
	Ready         bool      `json:"ready"`
	Source        string    `json:"source,omitempty"`
	Target        string    `json:"target,omitempty"`
}

// VercelConfig represents the configuration for deploying to Vercel
type VercelConfig struct {
	ProjectName  string
	BuildCommand string
	OutputDir    string
	EnvVars      map[string]string
	SourcePath   string
	Framework    string
}

// EnvVar represents an environment variable
type EnvVar struct {
	Name  string
	Value string
}

// VercelPricing contains pricing information for Vercel services
type VercelPricing struct {
	Plans       map[string]PlanPricing `json:"plans"`
	Addons      map[string]float64     `json:"addons"`
	LastFetched time.Time              `json:"last_fetched"`
}

// PlanPricing represents pricing for a specific Vercel plan
type PlanPricing struct {
	MonthlyPrice       float64 `json:"monthly_price"`
	IncludedBandwidth  int     `json:"included_bandwidth_gb"`
	IncludedExecutions int     `json:"included_function_executions"`
	IncludedBuilds     int     `json:"included_builds"`
}

// Deployment timeouts
const (
	deployTimeout = 10 * time.Minute
	buildTimeout  = 15 * time.Minute
	envVarTimeout = 2 * time.Minute
	linkTimeout   = 1 * time.Minute
	pullTimeout   = 2 * time.Minute
)
