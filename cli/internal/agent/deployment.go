package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cschleiden/go-workflows/workflow"
	"github.com/go-errors/errors"
	"github.com/pushtoprodai/prod-cli/baml_client/types"
	"github.com/pushtoprodai/prod-cli/internal/analyzer"
	"github.com/pushtoprodai/prod-cli/internal/deployment"
	"github.com/pushtoprodai/prod-cli/internal/deployment/flyio"
	"github.com/pushtoprodai/prod-cli/internal/deployment/heroku"
	"github.com/pushtoprodai/prod-cli/internal/deployment/netlify"
	"github.com/pushtoprodai/prod-cli/internal/deployment/render"
	"github.com/pushtoprodai/prod-cli/internal/deployment/vercel"
	"github.com/pushtoprodai/prod-cli/internal/history"
	"github.com/pushtoprodai/prod-cli/internal/output"
)

func (a *Activities) createDeployable(spec *deployment.DeploymentSpec, platform Platform) (deployment.Deployable, error) {
	p, ok := LookupPlatform(platform)
	if !ok {
		return nil, errors.Errorf("unsupported platform: %s", platform)
	}
	deployable, err := p.NewDeployable(a, spec)
	if err != nil {
		return nil, errors.Errorf("failed to create %s deployment: %w", p.Name, err)
	}
	return deployable, nil
}

func (a *Activities) deploySteps(ctx context.Context, spec deployment.DeploymentSpec, platform Platform) ([]deployment.CreatedResource, error) {
	deployable, err := a.createDeployable(&spec, platform)
	if err != nil {
		return nil, err
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
		summaryText = "📋 Deployment Steps\n\n"
		summaryText += "The following steps will be executed:\n\n"
		for i, step := range steps {
			summaryText += fmt.Sprintf("%d. %s\n", i+1, step)
		}
		summaryText += "\nNote: Existing resources will be detected and reused automatically.\n"
	} else {
		summaryText = summary.Summary
	}
	a.uiWriter.SendStatusComplete("summarizing", "")

	if tuiWriter, ok := a.uiWriter.(output.InfoBoxWriter); ok {
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

func (a *Activities) estimateHerokuCosts(ctx context.Context, spec deployment.DeploymentSpec, strategy deployment.DeploymentStrategy) (deployment.CostEstimate, error) {
	ha := heroku.NewDefaultHerokuDeploymentAdapter(a.uiWriter, a.llmClient)
	costs, err := ha.EstimateCost(ctx, &spec, strategy)
	if err != nil {
		return deployment.CostEstimate{}, errors.Errorf("failed to estimate costs: %w", err)
	}
	return costs, nil
}

// estimateContainerCosts returns a rough monthly estimate for the managed-container
// clouds (App Runner, Cloud Run, Azure Container Apps), which share one pricing shape.
// A missing rate yields a zero estimate (no preview), never an error.
func (a *Activities) estimateContainerCosts(_ context.Context, _ deployment.DeploymentSpec, platform Platform) (deployment.CostEstimate, error) {
	key := strings.ToLower(platform.String())
	if ce, ok := deployment.EstimateManagedContainerCost(key); ok {
		return ce, nil
	}
	return deployment.CostEstimate{}, nil
}

func (a *Activities) categorizeEnvVarsForDeployment(ctx context.Context, dbList []string, envVar analyzer.EnvVarCandidate) (deployment.EnvVar, error) {
	slog.Info("CategorizeEnvVarsForDeployment input", "envVar", envVar)
	slog.Info("CategorizeEnvVarsForDeployment dbList", "dbList", dbList)
	slog.Info("CategorizeEnvVarsForDeployment workflow name", "workflowName", CategorizeEnvVarsWorkflowName)

	// Framework-specific vars (Django, Rails, etc.) are categorized generically here,
	// but their actual values are set later in PrepareDeployment (which runs after this).
	// This allows for good UX (user sees all vars) while letting framework handlers control values.

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

	// Log sensitivity detection for visibility
	if cat.IsSensitive {
		slog.Info("Detected sensitive environment variable",
			"name", envVar.VarName,
			"reason", cat.SensitivityReason)
	}

	// Send individual completion message (no spinner start/stop to avoid conflicts)
	a.uiWriter.SendStatus("info", fmt.Sprintf("✅ Environment variable: %s categorized", envVar.VarName))

	return deployment.EnvVar{
		Name:              envVar.VarName,
		Role:              cat.Role,
		Service:           cat.DbType,
		Sensitive:         cat.IsSensitive,
		SensitivityReason: cat.SensitivityReason,
	}, nil
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

func (a *Activities) rollbackDeployment(ctx context.Context, spec deployment.DeploymentSpec, platform Platform, targetDeploymentID string) error {
	a.uiWriter.SendStatus("rolling_back", fmt.Sprintf("Rolling back to deployment %s", targetDeploymentID))

	deployable, err := a.createDeployable(&spec, platform)
	if err != nil {
		return err
	}

	err = deployable.Rollback(ctx, targetDeploymentID)
	if err != nil {
		a.uiWriter.SendStatusComplete("rolling_back", fmt.Sprintf("❌ Rollback failed: %v", err))
		return errors.Errorf("failed to rollback %s deployment: %w", platform, err)
	}

	a.uiWriter.SendStatusComplete("rolling_back", "✅ Successfully rolled back to previous working version")
	return nil
}

func (a *Activities) destroyDeployment(ctx context.Context, spec deployment.DeploymentSpec, platform Platform) error {
	a.uiWriter.SendStatus("destroying", fmt.Sprintf("Tearing down %s on %s", spec.Name, platform.DisplayName()))

	deployable, err := a.createDeployable(&spec, platform)
	if err != nil {
		return err
	}
	destroyer, ok := deployable.(deployment.Destroyer)
	if !ok {
		msg := fmt.Sprintf("Teardown isn't supported for %s yet — remove it from the platform's console.", platform.DisplayName())
		a.uiWriter.SendStatusComplete("destroying", "❌ "+msg)
		return errors.Errorf("%s", msg)
	}
	if err := destroyer.Destroy(ctx); err != nil {
		a.uiWriter.SendStatusComplete("destroying", fmt.Sprintf("❌ Teardown failed: %v", err))
		return errors.Errorf("failed to destroy %s deployment: %w", platform.DisplayName(), err)
	}
	a.uiWriter.SendStatusComplete("destroying", "✅ Deployment destroyed")
	return nil
}

func (a *Activities) getPreviousDeployment(ctx context.Context, spec deployment.DeploymentSpec, platform Platform) (*deployment.DeploymentInfo, error) {
	deployable, err := a.createDeployable(&spec, platform)
	if err != nil {
		return nil, err
	}

	return deployable.GetPreviousDeployment(ctx)
}

// getDeployedResources returns the backing resources prod recorded creating for an app's
// latest successful deploy on a platform (from local history's resources_created). Destroy
// uses this so a --delete-data cascade only ever deletes resources prod created — never a
// user's same-named database found by guessing. Returns nil (not an error) when there's no
// record, so destroy of the main service still proceeds.
func (a *Activities) getDeployedResources(_ context.Context, appName string, platform Platform) ([]deployment.CreatedResource, error) {
	if a.history == nil {
		return nil, nil
	}
	records, err := a.history.List(0)
	if err != nil {
		return nil, nil
	}
	app := strings.ToLower(strings.TrimSpace(appName))
	plat := history.CanonicalPlatform(platform.String())
	for _, r := range records {
		if strings.ToLower(strings.TrimSpace(r.ResourceName)) != app || history.CanonicalPlatform(r.Platform) != plat || r.Status != "success" {
			continue
		}
		raw, ok := r.Metadata["resources_created"]
		if !ok {
			continue
		}
		// resources_created round-trips through JSON in history, so re-marshal it back into
		// typed CreatedResources rather than hand-walking map[string]any.
		b, mErr := json.Marshal(raw)
		if mErr != nil {
			return nil, nil
		}
		var resources []deployment.CreatedResource
		if uErr := json.Unmarshal(b, &resources); uErr != nil {
			return nil, nil
		}
		return resources, nil
	}
	return nil, nil
}
