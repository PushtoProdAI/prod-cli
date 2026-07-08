package agent

import (
	"fmt"
	"log/slog"

	"github.com/cschleiden/go-workflows/workflow"
	"github.com/pushtoprodai/prod-cli/internal/deployment"
	prod_error "github.com/pushtoprodai/prod-cli/internal/error"
)

// deployModal deploys a Modal app via the `modal` CLI, driven through the generic
// AgentDeploySteps activity → the Modal Deployable. Modal has no rollback, so a failed
// liveness check is reported rather than auto-reverted. EXPERIMENTAL / unvalidated.
func (w *Workflows) deployModal(ctx workflow.Context, input DeployPlan) (deployResult, error) {
	operationId, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentLogDeploymentStart, "modal", input.Spec, input.Source, input.Action).Get(ctx)
	if err != nil {
		workflow.Logger(ctx).Warn("failed to log modal deploy start", "error", err)
	}

	db := deployment.NewDeploymentBuilder(&input.Spec, input.CollectedEnvVars, input.Shape)
	spec, err := db.Build()
	if err != nil {
		return deployResult{Error: deployError{Summary: fmt.Sprintf("Failed to build deployment spec: %v", err)}}, nil
	}
	spec.Metadata["buildContext"] = input.Source
	spec.Metadata["platform"] = "modal"

	createdResources, err := workflow.ExecuteActivity[[]deployment.CreatedResource](ctx, ActivityOpts, AgentDeploySteps, *spec, input.Platform).Get(ctx)
	if err != nil {
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow": DeployModalWorkflowName, "activity": AgentDeploySteps,
			"component": "deployment", "platform": "modal", "project_name": input.Spec.Name,
		})
		if operationId != "" {
			workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
				"error": err.Error(), "platform": "modal", "stage": "deployment_steps",
			}).Get(ctx)
		}
		summary, e1 := workflow.ExecuteActivity[deployError](ctx, ActivityOpts, AgentSummarizeError, err.Error(), input).Get(ctx)
		if e1 != nil {
			return deployResult{Error: deployError{Summary: err.Error()}}, nil
		}
		return deployResult{Error: summary}, nil
	}

	var deploymentURL string
	for _, r := range createdResources {
		if u, ok := r.Metadata["url"].(string); ok && u != "" {
			deploymentURL = u
			break
		}
	}

	// A Modal app with no web endpoint (a function/cron/worker) has no URL — a valid
	// success for a non-web shape; there's nothing to HTTP-probe.
	if deploymentURL == "" {
		if operationId != "" {
			workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "success", map[string]any{
				"platform": "modal", "resources_created": createdResources,
			}).Get(ctx)
		}
		slog.Info("Modal deployment completed (no web endpoint)")
		return deployResult{}, nil
	}

	// Shape-aware liveness: a web endpoint gets an HTTP probe; worker/cron shapes skip it.
	if _, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentVerifyLiveness, input.Shape, deploymentURL).Get(ctx); err != nil {
		// Modal has no rollback, so surface the failure instead of auto-reverting.
		if operationId != "" {
			workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
				"error": err.Error(), "platform": "modal", "stage": "url_verification", "url": deploymentURL,
			}).Get(ctx)
		}
		return deployResult{Error: deployError{
			Summary: fmt.Sprintf("Deployed to Modal, but %s didn't pass the health check. Modal has no rollback — check your app and redeploy.", deploymentURL),
		}}, nil
	}

	if operationId != "" {
		workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "success", map[string]any{
			"url": deploymentURL, "platform": "modal", "resources_created": createdResources,
		}).Get(ctx)
	}
	slog.Info("Modal deployment completed", "url", deploymentURL)
	return deployResult{Url: deploymentURL}, nil
}
