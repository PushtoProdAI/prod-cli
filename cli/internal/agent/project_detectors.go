package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/go-errors/errors"
	"github.com/pushtoprodai/prod-cli/internal/deployment/flyio"
	"github.com/pushtoprodai/prod-cli/internal/deployment/heroku"
	"github.com/pushtoprodai/prod-cli/internal/deployment/netlify"
	"github.com/pushtoprodai/prod-cli/internal/deployment/render"
	"github.com/pushtoprodai/prod-cli/internal/deployment/vercel"
	"github.com/pushtoprodai/prod-cli/internal/output"
)

// ExistingProjectInfo contains information about an existing deployment
type ExistingProjectInfo struct {
	Exists            bool
	Platform          Platform
	ProjectID         string
	Name              string
	DeployURL         string
	IsUpdate          bool
	ExistingDatabases []string
	DetectedPlatforms []Platform
}

// ProjectDetector defines the interface for detecting existing projects on a platform
type ProjectDetector interface {
	DetectExistingProject(ctx context.Context, projectName string, sourcePath string) (ExistingProjectInfo, error)
}

// getProjectDetector returns the appropriate detector for the given platform. A
// platform with no registered detector (idempotent deploy) gets the no-op.
func (a *Activities) getProjectDetector(platform Platform) (ProjectDetector, error) {
	p, ok := LookupPlatform(platform)
	if !ok {
		return nil, errors.Errorf("unsupported platform: %s", platform)
	}
	if p.NewDetector == nil {
		return noopProjectDetector{}, nil
	}
	return p.NewDetector(a), nil
}

// noopProjectDetector reports no existing project — used for platforms whose
// deploy is idempotent and doesn't need pre-detection.
type noopProjectDetector struct{}

func (noopProjectDetector) DetectExistingProject(_ context.Context, _, _ string) (ExistingProjectInfo, error) {
	return ExistingProjectInfo{Exists: false}, nil
}

// checkExistingProject checks if a project already exists on the given platform
func (a *Activities) checkExistingProject(ctx context.Context, platform Platform, projectName string, sourcePath string) (ExistingProjectInfo, error) {
	detector, err := a.getProjectDetector(platform)
	if err != nil {
		return ExistingProjectInfo{Exists: false, Platform: platform}, err
	}
	return detector.DetectExistingProject(ctx, projectName, sourcePath)
}

// RenderProjectDetector detects existing projects on Render
type RenderProjectDetector struct {
	client   render.RenderClient
	uiWriter output.StatusWriter
}

// NewRenderProjectDetector creates a new Render project detector
func NewRenderProjectDetector(client render.RenderClient, uiWriter output.StatusWriter) *RenderProjectDetector {
	return &RenderProjectDetector{
		client:   client,
		uiWriter: uiWriter,
	}
}

// DetectExistingProject checks for existing Render projects
func (d *RenderProjectDetector) DetectExistingProject(ctx context.Context, projectName string, sourcePath string) (ExistingProjectInfo, error) {
	result := ExistingProjectInfo{
		Exists:            false,
		Platform:          Render,
		ExistingDatabases: []string{},
	}

	existing, err := render.DetectExistingProject(ctx, d.client, projectName, sourcePath)
	if err != nil {
		return result, errors.Errorf("failed to check for existing Render project: %w", err)
	}
	if existing != nil {
		result.Exists = true
		result.ProjectID = existing.ServiceID
		result.Name = existing.Name
		result.IsUpdate = true

		slog.Info("Detecting existing Render databases", "projectName", projectName)
		normalizedProjectName := strings.ReplaceAll(projectName, "-", "_")
		expectedPGServiceNamePrefix := fmt.Sprintf("%s-postgres", projectName)
		expectedPGServiceNameUnderscores := fmt.Sprintf("%s_db", normalizedProjectName)
		expectedPGDatabaseName := fmt.Sprintf("%s_db", projectName)
		expectedRedisNamePrefix := fmt.Sprintf("%s-redis", projectName)
		slog.Info("Looking for databases", "expectedPGServiceNamePrefix", expectedPGServiceNamePrefix, "expectedPGServiceNameUnderscores", expectedPGServiceNameUnderscores, "expectedPGDatabaseName", expectedPGDatabaseName, "expectedRedisNamePrefix", expectedRedisNamePrefix)

		pgList, err := d.client.ListPostgres(ctx)
		if err != nil {
			slog.Error("Failed to list Render postgres", "error", err)
		} else {
			slog.Info("Listed Render postgres databases", "count", len(pgList))
			for _, pg := range pgList {
				slog.Info("Checking Render postgres", "serviceName", pg.Name, "databaseName", pg.DatabaseName, "id", pg.ID)
				if strings.HasPrefix(pg.Name, expectedPGServiceNamePrefix) ||
					pg.Name == expectedPGServiceNameUnderscores ||
					pg.DatabaseName == expectedPGDatabaseName ||
					strings.HasPrefix(pg.DatabaseName, normalizedProjectName+"_") {
					result.ExistingDatabases = append(result.ExistingDatabases, "postgresql")
					slog.Info("Matched existing PostgreSQL database", "serviceName", pg.Name, "databaseName", pg.DatabaseName)
				}
			}
		}

		redisList, err := d.client.ListRedis(ctx)
		if err != nil {
			slog.Error("Failed to list Render redis", "error", err)
		} else {
			slog.Info("Listed Render redis databases", "count", len(redisList))
			for _, red := range redisList {
				slog.Info("Checking Render redis", "name", red.Name, "type", red.Type, "id", red.ID)
				// Match redis with pattern: {projectName}-redis-{number}
				if strings.HasPrefix(red.Name, expectedRedisNamePrefix) {
					result.ExistingDatabases = append(result.ExistingDatabases, "redis")
					slog.Info("Matched existing Redis database", "name", red.Name)
				}
			}
		}

		if len(result.ExistingDatabases) == 0 {
			slog.Info("No matching Render databases found")
		}
	}

	return result, nil
}

// FlyIOProjectDetector detects existing projects on Fly.io
type FlyIOProjectDetector struct {
	client   flyio.FlyioClient
	uiWriter output.StatusWriter
}

// NewFlyIOProjectDetector creates a new Fly.io project detector
func NewFlyIOProjectDetector(client flyio.FlyioClient, uiWriter output.StatusWriter) *FlyIOProjectDetector {
	return &FlyIOProjectDetector{
		client:   client,
		uiWriter: uiWriter,
	}
}

// DetectExistingProject checks for existing Fly.io projects
func (d *FlyIOProjectDetector) DetectExistingProject(ctx context.Context, projectName string, sourcePath string) (ExistingProjectInfo, error) {
	result := ExistingProjectInfo{
		Exists:            false,
		Platform:          FlyIO,
		ExistingDatabases: []string{},
	}

	existing, err := flyio.DetectExistingProject(ctx, d.client, projectName)
	if err != nil {
		return result, errors.Errorf("failed to check for existing Fly.io project: %w", err)
	}
	if existing != nil {
		result.Exists = true
		result.ProjectID = existing.AppID
		result.Name = existing.Name
		result.DeployURL = existing.Hostname
		result.IsUpdate = true

		// Use normalized name for database detection to match deployment naming
		normalizedName := flyio.NormalizeFlyAppName(projectName)

		slog.Info("Detecting existing Fly.io databases", "projectName", projectName, "normalizedName", normalizedName)
		pgClusters, err := d.client.ListPostgres(ctx)
		if err != nil {
			slog.Error("Failed to list postgres clusters", "error", err)
		} else {
			slog.Info("Listed postgres clusters", "count", len(pgClusters))
			expectedPGName := fmt.Sprintf("%s-postgres", normalizedName)
			slog.Info("Looking for postgres cluster", "expectedName", expectedPGName)
			for _, cluster := range pgClusters {
				slog.Info("Checking postgres cluster", "name", cluster.Name, "expected", expectedPGName)
				if cluster.Name == expectedPGName {
					result.ExistingDatabases = append(result.ExistingDatabases, "postgresql")
					slog.Info("Matched existing postgres cluster", "name", cluster.Name)
					break
				}
			}
			if len(result.ExistingDatabases) == 0 {
				slog.Info("No matching postgres cluster found")
			}
		}

		// Check for Redis databases (Upstash Redis, not apps)
		redisList, err := d.client.ListRedis(ctx)
		if err != nil {
			slog.Error("Failed to list Redis databases", "error", err)
		} else {
			slog.Info("Listed Redis databases", "count", len(redisList))
			expectedRedisName := fmt.Sprintf("%s-redis", normalizedName)
			slog.Info("Looking for Redis database", "expectedName", expectedRedisName)
			for _, redis := range redisList {
				slog.Info("Checking Redis database", "name", redis.Name, "expected", expectedRedisName)
				if redis.Name == expectedRedisName {
					result.ExistingDatabases = append(result.ExistingDatabases, "redis")
					slog.Info("Matched existing Redis database", "name", redis.Name)
					break
				}
			}
		}
	}

	return result, nil
}

// NetlifyProjectDetector detects existing projects on Netlify
type NetlifyProjectDetector struct {
	uiWriter output.StatusWriter
}

// NewNetlifyProjectDetector creates a new Netlify project detector
func NewNetlifyProjectDetector(uiWriter output.StatusWriter) *NetlifyProjectDetector {
	return &NetlifyProjectDetector{
		uiWriter: uiWriter,
	}
}

// DetectExistingProject checks for existing Netlify projects
func (d *NetlifyProjectDetector) DetectExistingProject(ctx context.Context, projectName string, sourcePath string) (ExistingProjectInfo, error) {
	result := ExistingProjectInfo{
		Exists:            false,
		Platform:          Netlify,
		ExistingDatabases: []string{},
	}

	netlifyClient := netlify.NewCLINetlifyClient()
	existing, err := netlify.DetectExistingProject(netlifyClient, projectName, sourcePath)
	if err != nil {
		return result, errors.Errorf("failed to check for existing Netlify project: %w", err)
	}
	if existing != nil {
		result.Exists = true
		result.ProjectID = existing.SiteID
		result.Name = existing.Name
		result.IsUpdate = true
	}

	return result, nil
}

// VercelProjectDetector detects existing projects on Vercel
type VercelProjectDetector struct {
	uiWriter output.StatusWriter
}

// NewVercelProjectDetector creates a new Vercel project detector
func NewVercelProjectDetector(uiWriter output.StatusWriter) *VercelProjectDetector {
	return &VercelProjectDetector{
		uiWriter: uiWriter,
	}
}

// DetectExistingProject checks for existing Vercel projects
func (d *VercelProjectDetector) DetectExistingProject(ctx context.Context, projectName string, sourcePath string) (ExistingProjectInfo, error) {
	result := ExistingProjectInfo{
		Exists:            false,
		Platform:          Vercel,
		ExistingDatabases: []string{},
	}

	vercelClient := vercel.NewCLIVercelClient()
	existing, err := vercel.DetectExistingProject(vercelClient, projectName, sourcePath)
	if err != nil {
		return result, errors.Errorf("failed to check for existing Vercel project: %w", err)
	}
	if existing != nil {
		result.Exists = true
		result.ProjectID = existing.ProjectID
		result.Name = existing.Name
		result.IsUpdate = true
	}

	return result, nil
}

// HerokuProjectDetector detects existing projects on Heroku
type HerokuProjectDetector struct {
	uiWriter output.StatusWriter
}

// NewHerokuProjectDetector creates a new Heroku project detector
func NewHerokuProjectDetector(uiWriter output.StatusWriter) *HerokuProjectDetector {
	return &HerokuProjectDetector{
		uiWriter: uiWriter,
	}
}

// DetectExistingProject checks for existing Heroku projects
func (d *HerokuProjectDetector) DetectExistingProject(ctx context.Context, projectName string, sourcePath string) (ExistingProjectInfo, error) {
	result := ExistingProjectInfo{
		Exists:            false,
		Platform:          Heroku,
		ExistingDatabases: []string{},
	}

	herokuClient := heroku.NewHerokuClient("", d.uiWriter)
	existing, err := heroku.DetectExistingProject(ctx, herokuClient, projectName, sourcePath)
	if err != nil {
		return result, errors.Errorf("failed to check for existing Heroku project: %w", err)
	}
	if existing != nil {
		result.Exists = true
		result.ProjectID = existing.Name
		result.Name = existing.Name
		result.DeployURL = existing.WebURL
		result.IsUpdate = true

		addons, err := herokuClient.ListAddons(ctx, existing.AppID)
		if err == nil {
			for _, addon := range addons {
				planName := addon.Plan.Name
				if strings.HasPrefix(planName, "heroku-postgresql") {
					result.ExistingDatabases = append(result.ExistingDatabases, "postgresql")
				} else if strings.HasPrefix(planName, "heroku-redis") {
					result.ExistingDatabases = append(result.ExistingDatabases, "redis")
				}
			}
		}
	}

	return result, nil
}

// AWSProjectDetector detects existing projects on AWS. App Runner deploys are idempotent
// by service name (the deployer finds-or-redeploys), so this holds no backend/central-account
// state — it runs entirely on the user's own AWS credentials.
type AWSProjectDetector struct {
	uiWriter output.StatusWriter
}

// NewAWSProjectDetector creates a new AWS project detector
func NewAWSProjectDetector(uiWriter output.StatusWriter) *AWSProjectDetector {
	return &AWSProjectDetector{
		uiWriter: uiWriter,
	}
}

// DetectExistingProject is a no-op for App Runner (idempotent by service name)
func (d *AWSProjectDetector) DetectExistingProject(_ context.Context, _ string, _ string) (ExistingProjectInfo, error) {
	// App Runner deploys are idempotent by service name — the deployer finds and
	// redeploys an existing service — so there's no separate detection step.
	return ExistingProjectInfo{Platform: AWS, ExistingDatabases: []string{}}, nil
}

// CloudflareProjectDetector detects existing Cloudflare Pages projects. It's a no-op: the
// deploy is idempotent by project name (the adapter finds-or-creates the project) and destroy
// resolves the project by name, so there's no separate detection step.
type CloudflareProjectDetector struct {
	uiWriter output.StatusWriter
}

// NewCloudflareProjectDetector creates a Cloudflare Pages project detector.
func NewCloudflareProjectDetector(uiWriter output.StatusWriter) *CloudflareProjectDetector {
	return &CloudflareProjectDetector{uiWriter: uiWriter}
}

// DetectExistingProject is a no-op for Cloudflare Pages (idempotent by project name).
func (d *CloudflareProjectDetector) DetectExistingProject(_ context.Context, _ string, _ string) (ExistingProjectInfo, error) {
	return ExistingProjectInfo{Platform: CloudflarePages, ExistingDatabases: []string{}}, nil
}
