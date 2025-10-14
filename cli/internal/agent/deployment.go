package agent

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/cschleiden/go-workflows/workflow"
	"github.com/go-errors/errors"
	"github.com/meroxa/prod/cli/baml_client/types"
	"github.com/meroxa/prod/cli/internal/analyzer"
	"github.com/meroxa/prod/cli/internal/deployment"
	"github.com/meroxa/prod/cli/internal/deployment/flyio"
	"github.com/meroxa/prod/cli/internal/deployment/heroku"
	"github.com/meroxa/prod/cli/internal/deployment/netlify"
	"github.com/meroxa/prod/cli/internal/deployment/render"
	"github.com/meroxa/prod/cli/internal/deployment/vercel"
	"github.com/meroxa/prod/cli/internal/output"
)

func (a *Activities) deploySteps(ctx context.Context, spec deployment.DeploymentSpec, platform Platform) ([]deployment.CreatedResource, error) {
	// Create platform-specific Deployable implementation
	var deployable deployment.Deployable
	switch platform {
	case Render:
		dockerGen := deployment.NewDockerGenerator(a.uiWriter, spec.EnvVars)
		deployable = render.NewQueuedDeployment(a.renderClient, &spec, dockerGen, true, a.uiWriter)
	case FlyIO:
		dockerGen := deployment.NewDockerGenerator(a.uiWriter, spec.EnvVars)
		deployable = flyio.NewFlyioQueuedDeployment(a.flyClient, &spec, dockerGen, a.uiWriter)
	case Netlify:
		// Use the Netlify deployment adapter
		netlifyAdapter := netlify.NewDefaultNetlifyDeploymentAdapter(a.uiWriter, a.llmClient)
		var err error
		deployable, err = netlifyAdapter.GenerateArtifacts(&spec, deployment.StrategyNetlify)
		if err != nil {
			return nil, errors.Errorf("failed to create Netlify deployment: %w", err)
		}
	case Vercel:
		vercelAdapter := vercel.NewDefaultVercelDeploymentAdapter(a.uiWriter, a.llmClient)
		var err error
		deployable, err = vercelAdapter.GenerateArtifacts(&spec, deployment.StrategyVercel)
		if err != nil {
			return nil, errors.Errorf("failed to create Vercel deployment: %w", err)
		}
	case Heroku:
		herokuAdapter := heroku.NewDefaultHerokuDeploymentAdapter(a.uiWriter)
		var err error
		deployable, err = herokuAdapter.GenerateArtifacts(&spec, deployment.StrategyHeroku)
		if err != nil {
			return nil, errors.Errorf("failed to create Heroku deployment: %w", err)
		}
	default:
		return nil, errors.Errorf("unsupported platform: %s", platform)
	}

	createdResources, err := deployable.Deploy(ctx)
	if err != nil {
		// Check for Render HTTP errors
		var renderHTTPErr *render.HTTPError
		if errors.As(err, &renderHTTPErr) {
			if renderHTTPErr.IsClientError() {
				return []deployment.CreatedResource{}, workflow.NewPermanentError(errors.Errorf("failed to execute %s deployment. client error (%d): %s", platform, renderHTTPErr.StatusCode, renderHTTPErr.Message))
			}
		}

		// Check for Heroku HTTP errors
		var herokuHTTPErr *heroku.HTTPError
		if errors.As(err, &herokuHTTPErr) {
			if herokuHTTPErr.IsClientError() {
				return []deployment.CreatedResource{}, workflow.NewPermanentError(errors.Errorf("failed to execute %s deployment. client error (%d): %s", platform, herokuHTTPErr.StatusCode, herokuHTTPErr.Message))
			}
		}

		return []deployment.CreatedResource{}, errors.Errorf("failed to execute %s deployment: %w", platform, err)
	}

	return createdResources, nil
}

func (a *Activities) summarizeDeploySteps(ctx context.Context, steps []string) error {
	a.uiWriter.SendStatus("summarizing", "Summarizing deployment steps")

	var summaryText string
	summary, err := a.llmClient.SummarizeSteps(ctx, steps)
	if err != nil {
		slog.Warn("Failed to get LLM summary, using fallback", "error", err)
		summaryText = "We will execute the following deployment steps:\n\n"
		for i, step := range steps {
			summaryText += fmt.Sprintf("%d. %s\n", i+1, step)
		}
	} else {
		summaryText = summary.Summary
	}
	a.uiWriter.SendStatusComplete("summarizing", "")

	type infoBoxSender interface {
		SendInfoBox(title string, content string, icon string)
	}

	if tuiWriter, ok := a.uiWriter.(infoBoxSender); ok {
		slog.Info("Sending info box for deployment steps", "hasContent", summaryText != "")
		tuiWriter.SendInfoBox("Deployment Steps", summaryText, "📋")
	} else {
		slog.Info("Not a TUI writer, using plain text", "writerType", fmt.Sprintf("%T", a.uiWriter))
		fmt.Fprintf(a.uiWriter, "%s\n", summaryText)
	}
	return nil
}

func (a *Activities) estimateRenderCosts(ctx context.Context, spec deployment.DeploymentSpec, strategy deployment.DeploymentStrategy) (deployment.CostEstimate, error) {
	ra := render.NewRenderDeploymentAdapter(a.renderClient, a.uiWriter, a.llmClient)
	costs, err := ra.EstimateCost(ctx, &spec, strategy)
	if err != nil {
		return deployment.CostEstimate{}, errors.Errorf("failed to estimate costs: %w", err)
	}
	return costs, nil
}

func (a *Activities) estimateFlyioCosts(ctx context.Context, spec deployment.DeploymentSpec, strategy deployment.DeploymentStrategy) (deployment.CostEstimate, error) {
	fa := flyio.NewFlyioDeploymentAdapter(a.flyClient, a.uiWriter, a.llmClient)
	costs, err := fa.EstimateCost(ctx, &spec, strategy)
	if err != nil {
		return deployment.CostEstimate{}, errors.Errorf("failed to estimate costs: %w", err)
	}
	return costs, nil
}

func (a *Activities) estimateNetlifyCosts(ctx context.Context, spec deployment.DeploymentSpec, strategy deployment.DeploymentStrategy) (deployment.CostEstimate, error) {
	na := netlify.NewNetlifyDeploymentAdapter(netlify.NewCLINetlifyClient(), a.uiWriter, a.llmClient)
	costs, err := na.EstimateCost(ctx, &spec, strategy)
	if err != nil {
		return deployment.CostEstimate{}, errors.Errorf("failed to estimate costs: %w", err)
	}
	return costs, nil
}

func (a *Activities) estimateVercelCosts(ctx context.Context, spec deployment.DeploymentSpec, strategy deployment.DeploymentStrategy) (deployment.CostEstimate, error) {
	va := vercel.NewVercelDeploymentAdapter(vercel.NewCLIVercelClient(), a.uiWriter, a.llmClient)
	costs, err := va.EstimateCost(ctx, &spec, strategy)
	if err != nil {
		return deployment.CostEstimate{}, errors.Errorf("failed to estimate costs: %w", err)
	}
	return costs, nil
}

func (a *Activities) categorizeEnvVarsForDeployment(ctx context.Context, dbList []string, envVar analyzer.EnvVarCandidate) (deployment.EnvVar, error) {
	slog.Info("CategorizeEnvVarsForDeployment input", "envVar", envVar)
	slog.Info("CategorizeEnvVarsForDeployment dbList", "dbList", dbList)
	slog.Info("CategorizeEnvVarsForDeployment workflow name", "workflowName", CategorizeEnvVarsWorkflowName)
	ev := types.EnvVarCandidate{
		VarName: envVar.VarName,
		Line:    int64(envVar.Line),
		Context: envVar.Context,
		File:    envVar.File,
	}
	cat, err := a.llmClient.DetermineEnvVarRoles(ctx, ev, dbList)
	if err != nil {
		return deployment.EnvVar{}, errors.Errorf("failed to determine env var roles: %w", err)
	}
	// Send individual completion message (no spinner start/stop to avoid conflicts)
	a.uiWriter.SendStatus("info", fmt.Sprintf("✅ Environment variable: %s categorized", envVar.VarName))
	return deployment.EnvVar{Name: envVar.VarName, Role: cat.Role, Service: cat.DbType}, nil
}

func (a *Activities) getEnvVarsFromEnvFiles(_ context.Context, path string) ([]deployment.EnvVar, error) {
	a.uiWriter.SendStatus("analyzing", "Analyzing .env files for environment variables...")
	envVars := make([]deployment.EnvVar, 0)
	for _, file := range []string{".env", ".env.local", ".env.development", ".env.production", ".env.example"} {
		fileEnvVars, err := analyzer.ParseEnvFile(path, file)
		if err != nil {
			return envVars, errors.Errorf("failed to parse env file %s: %w", file, err)
		}
		for k, v := range fileEnvVars {
			envVars = append(envVars, deployment.EnvVar{Name: k, Value: v})
		}
	}
	a.uiWriter.SendStatusComplete("analyzing", "✅ Analyzed .env files")
	return envVars, nil
}

func (a *Activities) createDockerRepo(ctx context.Context, projectName string) error {
	session := CtxSession(ctx)
	err := a.beClient.CreateDockerRepository(ctx, session.AccessToken, projectName)
	if err != nil {
		return errors.Errorf("failed to create docker repository: %w", err)
	}
	return nil
}

func (a *Activities) determineBuildOutput(ctx context.Context, candidate analyzer.BuildOutputCandidate) (string, error) {
	bo := types.BuildOutputCandidate{
		Framework: candidate.Framework,
		Context:   candidate.ConfigContents,
		Default:   candidate.Path,
		Source:    candidate.Source,
	}
	output, err := a.llmClient.DetermineBuildOutput(ctx, bo)
	if err != nil {
		return "", errors.Errorf("failed to determine build output: %w", err)
	}
	return output.Path, nil
}

type ExistingProjectInfo struct {
	Exists            bool
	Platform          Platform
	ProjectID         string
	Name              string
	DeployURL         string
	IsUpdate          bool
	ExistingDatabases []string
}

type ProjectDetector interface {
	DetectExistingProject(ctx context.Context, projectName string, sourcePath string) (ExistingProjectInfo, error)
}

func (a *Activities) getProjectDetector(platform Platform) (ProjectDetector, error) {
	switch platform {
	case Render:
		return NewRenderProjectDetector(a.renderClient, a.uiWriter), nil
	case FlyIO:
		return NewFlyIOProjectDetector(a.flyClient, a.uiWriter), nil
	case Netlify:
		return NewNetlifyProjectDetector(a.uiWriter), nil
	case Vercel:
		return NewVercelProjectDetector(a.uiWriter), nil
	case Heroku:
		return NewHerokuProjectDetector(a.uiWriter), nil
	default:
		return nil, errors.Errorf("unsupported platform: %s", platform)
	}
}

func (a *Activities) checkExistingProject(ctx context.Context, platform Platform, projectName string, sourcePath string) (ExistingProjectInfo, error) {
	detector, err := a.getProjectDetector(platform)
	if err != nil {
		return ExistingProjectInfo{Exists: false, Platform: platform}, err
	}
	return detector.DetectExistingProject(ctx, projectName, sourcePath)
}

type RenderProjectDetector struct {
	client   render.RenderClient
	uiWriter output.StatusWriter
}

func NewRenderProjectDetector(client render.RenderClient, uiWriter output.StatusWriter) *RenderProjectDetector {
	return &RenderProjectDetector{
		client:   client,
		uiWriter: uiWriter,
	}
}

func (d *RenderProjectDetector) DetectExistingProject(ctx context.Context, projectName string, sourcePath string) (ExistingProjectInfo, error) {
	result := ExistingProjectInfo{
		Exists:            false,
		Platform:          Render,
		ExistingDatabases: []string{},
	}

	existing, err := render.DetectExistingProject(ctx, d.client, projectName)
	if err != nil {
		return result, errors.Errorf("failed to check for existing Render project: %w", err)
	}
	if existing != nil {
		d.uiWriter.SendStatus("info", fmt.Sprintf("✅ Found existing Render service: %s", existing.Name))
		result.Exists = true
		result.ProjectID = existing.ServiceID
		result.Name = existing.Name
		result.IsUpdate = true

		allServices, err := d.client.ListServices(ctx, "")
		if err != nil {
			return result, errors.Errorf("failed to list services for database detection: %w", err)
		}

		for _, service := range allServices {
			if service.Type == "pgsql" && service.Name == fmt.Sprintf("%s-postgres", projectName) {
				result.ExistingDatabases = append(result.ExistingDatabases, "postgresql")
				d.uiWriter.SendStatus("info", fmt.Sprintf("✅ Found existing PostgreSQL database: %s", service.Name))
			}
			if service.Type == "redis" && service.Name == fmt.Sprintf("%s-redis", projectName) {
				result.ExistingDatabases = append(result.ExistingDatabases, "redis")
				d.uiWriter.SendStatus("info", fmt.Sprintf("✅ Found existing Redis database: %s", service.Name))
			}
		}
	} else {
		d.uiWriter.SendStatus("info", "No existing Render service found - will create new deployment")
	}

	return result, nil
}

type FlyIOProjectDetector struct {
	client   flyio.FlyioClient
	uiWriter output.StatusWriter
}

func NewFlyIOProjectDetector(client flyio.FlyioClient, uiWriter output.StatusWriter) *FlyIOProjectDetector {
	return &FlyIOProjectDetector{
		client:   client,
		uiWriter: uiWriter,
	}
}

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
		d.uiWriter.SendStatus("info", fmt.Sprintf("✅ Found existing Fly.io app: %s", existing.Name))
		result.Exists = true
		result.ProjectID = existing.AppID
		result.Name = existing.Name
		result.DeployURL = existing.Hostname
		result.IsUpdate = true

		postgresApp, err := flyio.DetectExistingProject(ctx, d.client, fmt.Sprintf("%s-postgres", projectName))
		if err == nil && postgresApp != nil {
			result.ExistingDatabases = append(result.ExistingDatabases, "postgresql")
			d.uiWriter.SendStatus("info", fmt.Sprintf("✅ Found existing PostgreSQL app: %s", postgresApp.Name))
		}

		redisApp, err := flyio.DetectExistingProject(ctx, d.client, fmt.Sprintf("%s-redis", projectName))
		if err == nil && redisApp != nil {
			result.ExistingDatabases = append(result.ExistingDatabases, "redis")
			d.uiWriter.SendStatus("info", fmt.Sprintf("✅ Found existing Redis app: %s", redisApp.Name))
		}
	} else {
		d.uiWriter.SendStatus("info", "No existing Fly.io app found - will create new deployment")
	}

	return result, nil
}

type NetlifyProjectDetector struct {
	uiWriter output.StatusWriter
}

func NewNetlifyProjectDetector(uiWriter output.StatusWriter) *NetlifyProjectDetector {
	return &NetlifyProjectDetector{
		uiWriter: uiWriter,
	}
}

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
		d.uiWriter.SendStatus("info", fmt.Sprintf("✅ Found existing Netlify site: %s", existing.Name))
		result.Exists = true
		result.ProjectID = existing.SiteID
		result.Name = existing.Name
		result.IsUpdate = true
	} else {
		d.uiWriter.SendStatus("info", "No existing Netlify site found - will create new deployment")
	}

	return result, nil
}

type VercelProjectDetector struct {
	uiWriter output.StatusWriter
}

func NewVercelProjectDetector(uiWriter output.StatusWriter) *VercelProjectDetector {
	return &VercelProjectDetector{
		uiWriter: uiWriter,
	}
}

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
		d.uiWriter.SendStatus("info", fmt.Sprintf("✅ Found existing Vercel project: %s", existing.Name))
		result.Exists = true
		result.ProjectID = existing.ProjectID
		result.Name = existing.Name
		result.IsUpdate = true
	} else {
		d.uiWriter.SendStatus("info", "No existing Vercel project found - will create new deployment")
	}

	return result, nil
}

type HerokuProjectDetector struct {
	uiWriter output.StatusWriter
}

func NewHerokuProjectDetector(uiWriter output.StatusWriter) *HerokuProjectDetector {
	return &HerokuProjectDetector{
		uiWriter: uiWriter,
	}
}

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
		d.uiWriter.SendStatus("info", fmt.Sprintf("✅ Found existing Heroku app: %s", existing.Name))
		result.Exists = true
		result.ProjectID = existing.AppID
		result.Name = existing.Name
		result.DeployURL = existing.WebURL
		result.IsUpdate = true

		addons, err := herokuClient.ListAddons(ctx, existing.AppID)
		if err == nil {
			for _, addon := range addons {
				planName := addon.Plan.Name
				if contains(planName, "heroku-postgresql") {
					result.ExistingDatabases = append(result.ExistingDatabases, "postgresql")
					d.uiWriter.SendStatus("info", "✅ Found existing PostgreSQL addon")
				} else if contains(planName, "heroku-redis") {
					result.ExistingDatabases = append(result.ExistingDatabases, "redis")
					d.uiWriter.SendStatus("info", "✅ Found existing Redis addon")
				}
			}
		}
	} else {
		d.uiWriter.SendStatus("info", "No existing Heroku app found - will create new deployment")
	}

	return result, nil
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && s[:len(substr)] == substr
}
