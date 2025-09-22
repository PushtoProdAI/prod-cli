package heroku

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"

	heroku "github.com/heroku/heroku-go/v6"
	"github.com/meroxa/prod/cli/internal/output"
)

// HTTPError represents an HTTP error with status code information
type HTTPError struct {
	StatusCode int
	Message    string
}

func (e *HTTPError) Error() string {
	return e.Message
}

// IsClientError returns true if the status code is in the 4xx range
func (e *HTTPError) IsClientError() bool {
	return e.StatusCode >= 400 && e.StatusCode < 500
}

// IsServerError returns true if the status code is in the 5xx range
func (e *HTTPError) IsServerError() bool {
	return e.StatusCode >= 500 && e.StatusCode < 600
}

// HerokuClient wraps the official Heroku Go client
type HerokuClient struct {
	client *heroku.Service
	writer io.Writer
}

// GetWriter returns the writer for output
func (hc *HerokuClient) GetWriter() io.Writer {
	return hc.writer
}

// NewHerokuClient creates a new Heroku client using the official SDK
func NewHerokuClient(apiKey string, writer io.Writer) *HerokuClient {
	if writer == nil {
		writer = output.NewNoOpWriter()
	}

	// If apiKey is empty, try to get from environment
	if apiKey == "" {
		apiKey = os.Getenv("HEROKU_API_KEY")
		if apiKey == "" {
			apiKey = os.Getenv("HEROKU_AUTH_TOKEN")
		}
	}

	// For v6, use the DefaultTransport but create a copy to avoid global mutation
	transport := &heroku.Transport{
		BearerToken: apiKey,
	}

	// Create service with the custom transport
	client := heroku.NewService(&http.Client{
		Transport: transport,
	})

	return &HerokuClient{
		client: client,
		writer: writer,
	}
}

// IsAuthenticated checks if the client has valid authentication
func (c *HerokuClient) IsAuthenticated(ctx context.Context) bool {
	// Try a simple API call to verify auth
	_, err := c.ListApps(ctx)
	return err == nil
}

// CreateApp creates a new Heroku application
func (c *HerokuClient) CreateApp(ctx context.Context, name string, region string) (*heroku.App, error) {
	opts := heroku.AppCreateOpts{}
	if name != "" {
		opts.Name = &name
	}
	if region != "" {
		opts.Region = &region
	}

	app, err := c.client.AppCreate(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create app: %w", err)
	}
	return app, nil
}

// GetApp retrieves information about a specific Heroku app
func (c *HerokuClient) GetApp(ctx context.Context, appID string) (*heroku.App, error) {
	app, err := c.client.AppInfo(ctx, appID)
	if err != nil {
		return nil, fmt.Errorf("failed to get app info: %w", err)
	}
	return app, nil
}

// DeleteApp deletes a Heroku app
func (c *HerokuClient) DeleteApp(ctx context.Context, appID string) (*heroku.App, error) {
	app, err := c.client.AppDelete(ctx, appID)
	if err != nil {
		return nil, fmt.Errorf("failed to delete app: %w", err)
	}
	return app, nil
}

// ListApps lists all Heroku apps accessible by the authenticated user
func (c *HerokuClient) ListApps(ctx context.Context) (heroku.AppListResult, error) {
	apps, err := c.client.AppList(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list apps: %w", err)
	}
	return apps, nil
}

// GetConfigVars retrieves the config vars (environment variables) for an app
func (c *HerokuClient) GetConfigVars(ctx context.Context, appID string) (map[string]*string, error) {
	vars, err := c.client.ConfigVarInfoForApp(ctx, appID)
	if err != nil {
		return nil, fmt.Errorf("failed to get config vars: %w", err)
	}
	return vars, nil
}

// UpdateConfigVars updates the config vars (environment variables) for an app
func (c *HerokuClient) UpdateConfigVars(ctx context.Context, appID string, vars map[string]*string) (map[string]*string, error) {
	updatedVars, err := c.client.ConfigVarUpdate(ctx, appID, vars)
	if err != nil {
		return nil, fmt.Errorf("failed to update config vars: %w", err)
	}
	return updatedVars, nil
}

// CreateAddon creates a new addon for an app
func (c *HerokuClient) CreateAddon(ctx context.Context, appID string, plan string, config map[string]string) (*heroku.AddOn, error) {
	opts := heroku.AddOnCreateOpts{
		Plan: plan,
	}

	if config != nil {
		opts.Config = config
	}

	addon, err := c.client.AddOnCreate(ctx, appID, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create addon: %w", err)
	}
	return addon, nil
}

// GetAddon retrieves information about a specific addon
func (c *HerokuClient) GetAddon(ctx context.Context, addonID string) (*heroku.AddOn, error) {
	addon, err := c.client.AddOnInfo(ctx, addonID)
	if err != nil {
		return nil, fmt.Errorf("failed to get addon info: %w", err)
	}
	return addon, nil
}

// ListAddons lists all addons for an app
func (c *HerokuClient) ListAddons(ctx context.Context, appID string) (heroku.AddOnListByAppResult, error) {
	addons, err := c.client.AddOnListByApp(ctx, appID, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list addons: %w", err)
	}
	return addons, nil
}

// DeleteAddon deletes an addon
func (c *HerokuClient) DeleteAddon(ctx context.Context, appID string, addonID string) (*heroku.AddOn, error) {
	addon, err := c.client.AddOnDelete(ctx, appID, addonID)
	if err != nil {
		return nil, fmt.Errorf("failed to delete addon: %w", err)
	}
	return addon, nil
}

// GetFormation retrieves a specific process formation for an app
func (c *HerokuClient) GetFormation(ctx context.Context, appID string, processType string) (*heroku.Formation, error) {
	formation, err := c.client.FormationInfo(ctx, appID, processType)
	if err != nil {
		return nil, fmt.Errorf("failed to get formation: %w", err)
	}
	return formation, nil
}

// UpdateFormation updates a process formation for an app
func (c *HerokuClient) UpdateFormation(ctx context.Context, appID string, processType string, quantity *int, size *string) (*heroku.Formation, error) {
	opts := heroku.FormationUpdateOpts{
		Quantity: quantity,
	}

	// In v6, Size changed to DynoSize struct
	if size != nil {
		opts.DynoSize = &struct {
			ID   *string `json:"id,omitempty" url:"id,omitempty,key"`
			Name *string `json:"name,omitempty" url:"name,omitempty,key"`
		}{
			ID: size,
		}
	}

	formation, err := c.client.FormationUpdate(ctx, appID, processType, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to update formation: %w", err)
	}
	return formation, nil
}

// ListFormations lists all process formations for an app
func (c *HerokuClient) ListFormations(ctx context.Context, appID string) (heroku.FormationListResult, error) {
	formations, err := c.client.FormationList(ctx, appID, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list formations: %w", err)
	}
	return formations, nil
}

// CreateBuild creates a new build for an app from a source tarball URL
func (c *HerokuClient) CreateBuild(ctx context.Context, appID string, sourceURL string, buildpacks []string) (*heroku.Build, error) {
	// Create build opts with source URL
	opts := heroku.BuildCreateOpts{}
	opts.SourceBlob.URL = &sourceURL

	// Convert buildpacks to the expected format if provided
	if len(buildpacks) > 0 {
		var buildpackList []*struct {
			Name *string `json:"name,omitempty" url:"name,omitempty,key"`
			URL  *string `json:"url,omitempty" url:"url,omitempty,key"`
		}
		for _, bp := range buildpacks {
			bpCopy := bp // Create a copy to avoid pointer issues
			buildpackList = append(buildpackList, &struct {
				Name *string `json:"name,omitempty" url:"name,omitempty,key"`
				URL  *string `json:"url,omitempty" url:"url,omitempty,key"`
			}{
				URL: &bpCopy,
			})
		}
		opts.Buildpacks = buildpackList
	}

	build, err := c.client.BuildCreate(ctx, appID, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create build: %w", err)
	}
	return build, nil
}

// GetBuild retrieves information about a specific build
func (c *HerokuClient) GetBuild(ctx context.Context, appID string, buildID string) (*heroku.Build, error) {
	build, err := c.client.BuildInfo(ctx, appID, buildID)
	if err != nil {
		return nil, fmt.Errorf("failed to get build info: %w", err)
	}
	return build, nil
}

// GetRelease retrieves information about a specific release
func (c *HerokuClient) GetRelease(ctx context.Context, appID string, releaseID string) (*heroku.Release, error) {
	release, err := c.client.ReleaseInfo(ctx, appID, releaseID)
	if err != nil {
		return nil, fmt.Errorf("failed to get release info: %w", err)
	}
	return release, nil
}

// ListReleases lists all releases for an app
func (c *HerokuClient) ListReleases(ctx context.Context, appID string) (heroku.ReleaseListResult, error) {
	releases, err := c.client.ReleaseList(ctx, appID, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list releases: %w", err)
	}
	return releases, nil
}

// CreateDomain adds a custom domain to an app
func (c *HerokuClient) CreateDomain(ctx context.Context, appID string, hostname string) (*heroku.Domain, error) {
	opts := heroku.DomainCreateOpts{
		Hostname: hostname,
	}

	domain, err := c.client.DomainCreate(ctx, appID, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create domain: %w", err)
	}
	return domain, nil
}

// ListDomains lists all custom domains for an app
func (c *HerokuClient) ListDomains(ctx context.Context, appID string) (heroku.DomainListResult, error) {
	domains, err := c.client.DomainList(ctx, appID, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list domains: %w", err)
	}
	return domains, nil
}

// DeleteDomain removes a custom domain from an app
func (c *HerokuClient) DeleteDomain(ctx context.Context, appID string, domainID string) (*heroku.Domain, error) {
	domain, err := c.client.DomainDelete(ctx, appID, domainID)
	if err != nil {
		return nil, fmt.Errorf("failed to delete domain: %w", err)
	}
	return domain, nil
}

// CreateLogDrain creates a new log drain for an app
func (c *HerokuClient) CreateLogDrain(ctx context.Context, appID string, url string) (*heroku.LogDrain, error) {
	opts := heroku.LogDrainCreateOpts{
		URL: url,
	}

	drain, err := c.client.LogDrainCreate(ctx, appID, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create log drain: %w", err)
	}
	return drain, nil
}

// ListLogDrains lists all log drains for an app
func (c *HerokuClient) ListLogDrains(ctx context.Context, appID string) (heroku.LogDrainListResult, error) {
	drains, err := c.client.LogDrainList(ctx, appID, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list log drains: %w", err)
	}
	return drains, nil
}

// DeleteLogDrain removes a log drain from an app
func (c *HerokuClient) DeleteLogDrain(ctx context.Context, appID string, drainID string) (*heroku.LogDrain, error) {
	drain, err := c.client.LogDrainDelete(ctx, appID, drainID)
	if err != nil {
		return nil, fmt.Errorf("failed to delete log drain: %w", err)
	}
	return drain, nil
}

// SetBuildpacks sets the buildpacks for an app
func (c *HerokuClient) SetBuildpacks(ctx context.Context, appID string, buildpacks []string) (heroku.BuildpackInstallationUpdateResult, error) {
	// Note: In v6, we update all buildpacks at once
	var updates []struct {
		Buildpack string `json:"buildpack" url:"buildpack,key"`
	}

	for _, bp := range buildpacks {
		updates = append(updates, struct {
			Buildpack string `json:"buildpack" url:"buildpack,key"`
		}{Buildpack: bp})
	}

	opts := heroku.BuildpackInstallationUpdateOpts{
		Updates: updates,
	}

	result, err := c.client.BuildpackInstallationUpdate(ctx, appID, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to set buildpacks: %w", err)
	}

	return result, nil
}