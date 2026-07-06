package agent

import (
	"log/slog"
	"net/url"

	"github.com/cschleiden/go-workflows/workflow"
	"github.com/go-errors/errors"
	"github.com/pushtoprodai/prod-cli/internal/deployment"
	prod_error "github.com/pushtoprodai/prod-cli/internal/error"
)

// deployGCPRun deploys to Google Cloud Run with the user's own credentials
// (ADC). The image build+push to Artifact Registry, the Cloud Run create/update,
// and the wait for Ready all happen inside the AgentDeploySteps activity (see the
// gcprun Cloud Run deployable); this workflow builds the spec, runs that
// activity, and verifies the resulting URL is live.
func (w *Workflows) deployGCPRun(ctx workflow.Context, input DeployPlan) (deployResult, error) {
	operationId, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentLogDeploymentStart, "gcprun", input.Spec, input.Source, input.Action).Get(ctx)
	if err != nil {
		slog.Error("Failed to log deployment start", "error", err)
		// Continue with deployment even if logging fails.
	}

	// Cloud Run deploys from a locally built container image.
	if !deployment.IsDockerAvailable() {
		if operationId != "" {
			workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
				"error": "Docker not available", "platform": "gcprun", "stage": "docker_validation",
			}).Get(ctx)
		}
		summary, err2 := workflow.ExecuteActivity[deployError](ctx, ActivityOpts, AgentSummarizeError, "not able to build docker image. cannot connect to local docker daemon", input).Get(ctx)
		if err2 != nil {
			return deployResult{Error: deployError{Summary: "not able to build docker image. cannot connect to local docker daemon"}}, nil
		}
		return deployResult{Error: summary}, nil
	}

	// Build the deployment spec.
	db := deployment.NewDeploymentBuilder(&input.Spec, input.CollectedEnvVars)
	spec, err := db.Build()
	if err != nil {
		if operationId != "" {
			workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
				"error": err.Error(), "platform": "gcprun", "stage": "spec_build",
			}).Get(ctx)
		}
		return deployResult{}, errors.Errorf("failed to build deployment spec: %w", err)
	}
	spec.Metadata["buildContext"] = input.Source
	if input.ExistingProjectInfo.Exists {
		spec.IsUpdate = true
	}

	// Build+push to Artifact Registry, create/update the Cloud Run service, wait for Ready.
	createdResources, err := workflow.ExecuteActivity[[]deployment.CreatedResource](ctx, ActivityOpts, AgentDeploySteps, *spec, input.Platform).Get(ctx)
	if err != nil {
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow": DeployGCPRunWorkflowName, "activity": AgentDeploySteps,
			"component": "deployment", "platform": "gcprun",
			"project_name": input.Spec.Name, "language": input.Spec.Language,
		})
		summary, e1 := workflow.ExecuteActivity[deployError](ctx, ActivityOpts, AgentSummarizeError, err.Error(), input).Get(ctx)
		if operationId != "" {
			workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
				"error": err.Error(), "platform": "gcprun", "stage": "deploy_steps",
			}).Get(ctx)
		}
		if e1 != nil {
			return deployResult{Error: deployError{Summary: err.Error()}}, nil
		}
		return deployResult{Error: summary}, nil
	}

	// The Cloud Run deployable returns the service and its URL.
	var svc deployment.CreatedResource
	for _, cr := range createdResources {
		if cr.Type == "cloudrun_service" {
			svc = cr
			break
		}
	}
	if svc.ID == "" {
		return deployResult{}, errors.Errorf("Cloud Run deployment returned no service")
	}
	u, _ := svc.Metadata["url"].(string)
	if u == "" {
		return deployResult{}, errors.Errorf("Cloud Run deployment returned no URL")
	}

	// Verify the URL is live.
	path, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentDetermineRootPath, input.Spec.Routes).Get(ctx)
	if err != nil {
		slog.Info("Failed to determine root path for application", "error", err)
		path = "/"
	}
	fullUrl, err := url.JoinPath(u, path)
	if err != nil {
		slog.Info("Failed to combine paths", "error", err)
		fullUrl = u
	}

	liveCheckOpts := ActivityOpts
	liveCheckOpts.RetryOptions.MaxAttempts = 15
	if _, err := workflow.ExecuteActivity[string](ctx, liveCheckOpts, AgentVerifyLiveness, input.Shape, fullUrl).Get(ctx); err != nil {
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow": DeployGCPRunWorkflowName, "activity": AgentVerifyLiveness,
			"component": "deployment", "platform": "gcprun",
			"project_name": input.Spec.Name, "language": input.Spec.Language,
		})
		summary, e1 := workflow.ExecuteActivity[deployError](ctx, ActivityOpts, AgentSummarizeError, err.Error(), input).Get(ctx)
		if operationId != "" {
			workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
				"error": err.Error(), "platform": "gcprun", "stage": "url_check", "url": fullUrl,
			}).Get(ctx)
		}
		if e1 != nil {
			return deployResult{Error: deployError{Summary: err.Error()}}, nil
		}
		return deployResult{Error: summary}, nil
	}

	if operationId != "" {
		workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "success", map[string]any{
			"url": fullUrl, "platform": "gcprun",
		}).Get(ctx)
	}

	return deployResult{Url: fullUrl}, nil
}
