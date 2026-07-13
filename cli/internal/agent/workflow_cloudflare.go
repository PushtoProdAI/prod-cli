package agent

import (
	"fmt"
	"log/slog"

	"github.com/cschleiden/go-workflows/workflow"
	"github.com/pushtoprodai/prod-cli/internal/deployment"
	prod_error "github.com/pushtoprodai/prod-cli/internal/error"
)

// deployCloudflare drives a Cloudflare Pages static deploy: build the spec, run the adapter
// (which builds the site and direct-uploads it), and record the result. Static deploys are
// fire-and-forget — there's no health-check auto-rollback (Cloudflare Pages rollback is a
// follow-up; SupportsRollback is false), so this is a lean success/failure path.
func (w *Workflows) deployCloudflare(ctx workflow.Context, input DeployPlan) (deployResult, error) {
	slog.Info("deployCloudflare workflow started", "platform", input.Platform, "project", input.Spec.Name)

	operationId, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentLogDeploymentStart, "cloudflarepages", input.Spec, input.Source, input.Action).Get(ctx)
	if err != nil {
		slog.Warn("failed to log Cloudflare deploy start", "error", err)
	}

	db := deployment.NewDeploymentBuilder(&input.Spec, input.CollectedEnvVars, input.Shape)
	spec, err := db.Build()
	if err != nil {
		failCloudflare(ctx, operationId, "spec_build", err)
		return deployResult{Error: deployError{Summary: fmt.Sprintf("Failed to build deployment spec: %v", err)}}, nil
	}
	spec.Metadata["buildContext"] = input.Source
	spec.Metadata["platform"] = "cloudflarepages"
	if input.ExistingProjectInfo.Exists {
		spec.IsUpdate = true
		spec.ExistingProjectID = input.ExistingProjectInfo.ProjectID
	}

	createdResources, err := workflow.ExecuteActivity[[]deployment.CreatedResource](ctx, ActivityOpts, AgentDeploySteps, *spec, input.Platform).Get(ctx)
	if err != nil {
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow": DeployCloudflareWorkflowName, "activity": AgentDeploySteps,
			"component": "deployment", "platform": "cloudflarepages", "project_name": input.Spec.Name,
		})
		failCloudflare(ctx, operationId, "deployment", err)
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
		}
	}

	if operationId != "" {
		_, _ = workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "success", map[string]any{
			"url":               deploymentURL,
			"platform":          "cloudflarepages",
			"resources_created": createdResources,
		}).Get(ctx)
	}

	slog.Info("Cloudflare Pages deploy completed", "url", deploymentURL)
	return deployResult{Url: deploymentURL}, nil
}

// failCloudflare records a Cloudflare deploy failure against the operation log (best-effort).
func failCloudflare(ctx workflow.Context, operationId, stage string, err error) {
	if operationId == "" {
		return
	}
	_, _ = workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
		"error": err.Error(), "platform": "cloudflarepages", "stage": stage,
	}).Get(ctx)
}
