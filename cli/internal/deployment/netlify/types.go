package netlify

import (
	"time"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
)

// NetlifyClient defines the interface for interacting with Netlify
type NetlifyClient interface {
	// Site management
	CreateSite(req CreateSiteRequest) (*NetlifySite, error)
	GetSite(siteID string) (*NetlifySite, error)
	UpdateSite(siteID string, req UpdateSiteRequest) (*NetlifySite, error)
	DeleteSite(siteID string) error
	LinkSite(siteID string) error

	// Deployment
	DeploySite(siteID string, path string, functionsPath string) (*NetlifyDeploy, error)
	GetDeploy(siteID, deployID string) (*NetlifyDeploy, error)

	// Environment variables
	SetEnvironmentVariables(siteID string, vars []deployment.EnvVar) error

	// Build settings
	UpdateBuildSettings(siteID string, settings BuildSettings) error
}

// CreateSiteRequest represents a request to create a new Netlify site
type CreateSiteRequest struct {
	Name          string            `json:"name"`
	CustomDomain  string            `json:"custom_domain,omitempty"`
	BuildSettings *BuildSettings    `json:"build_settings,omitempty"`
	EnvVars       map[string]string `json:"env,omitempty"`
}

// UpdateSiteRequest represents a request to update a Netlify site
type UpdateSiteRequest struct {
	Name          string         `json:"name,omitempty"`
	CustomDomain  string         `json:"custom_domain,omitempty"`
	BuildSettings *BuildSettings `json:"build_settings,omitempty"`
}

// BuildSettings represents build configuration for a Netlify site
type BuildSettings struct {
	Command      string            `json:"cmd"`
	PublishDir   string            `json:"dir"`
	FunctionsDir string            `json:"functions_dir,omitempty"`
	Environment  map[string]string `json:"env,omitempty"`
}

// NetlifySite represents a Netlify site
type NetlifySite struct {
	ID              string        `json:"id"`
	Name            string        `json:"name"`
	URL             string        `json:"url"`
	AdminURL        string        `json:"admin_url"`
	ScreenshotURL   string        `json:"screenshot_url"`
	CreatedAt       time.Time     `json:"created_at"`
	UpdatedAt       time.Time     `json:"updated_at"`
	CustomDomain    string        `json:"custom_domain"`
	DefaultDomain   string        `json:"default_domain"`
	BuildSettings   BuildSettings `json:"build_settings"`
	ProcessingState string        `json:"processing_state"`
}

// NetlifyDeploy represents a deployment on Netlify
type NetlifyDeploy struct {
	ID           string    `json:"id"`
	SiteID       string    `json:"site_id"`
	State        string    `json:"state"`
	Name         string    `json:"name"`
	URL          string    `json:"url"`
	AdminURL     string    `json:"admin_url"`
	DeployURL    string    `json:"deploy_url"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	ErrorMessage string    `json:"error_message,omitempty"`
	Branch       string    `json:"branch"`
	CommitRef    string    `json:"commit_ref"`
	CommitURL    string    `json:"commit_url"`
	ReviewID     string    `json:"review_id,omitempty"`
	Title        string    `json:"title"`
	Context      string    `json:"context"`
	DeployTime   int       `json:"deploy_time"`
}

// NetlifyConfig represents the configuration for deploying to Netlify
type NetlifyConfig struct {
	SiteName     string
	BuildCommand string
	PublishDir   string
	FunctionsDir string
	EnvVars      map[string]string
	SourcePath   string
}

// NetlifyPricing contains pricing information for Netlify services
type NetlifyPricing struct {
	Plans       map[string]PlanPricing `json:"plans"`
	Addons      map[string]float64     `json:"addons"`
	LastFetched time.Time              `json:"last_fetched"`
}

// PlanPricing represents pricing for a specific Netlify plan
type PlanPricing struct {
	MonthlyPrice      float64 `json:"monthly_price"`
	IncludedBandwidth int     `json:"included_bandwidth_gb"`
	IncludedBuildMins int     `json:"included_build_minutes"`
	IncludedFunctions int     `json:"included_function_invocations"`
}
