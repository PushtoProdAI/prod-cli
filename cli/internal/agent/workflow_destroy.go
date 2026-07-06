package agent

import (
	"fmt"

	"github.com/cschleiden/go-workflows/workflow"
	"github.com/pushtoprodai/prod-cli/internal/deployment"
	prod_error "github.com/pushtoprodai/prod-cli/internal/error"
)

// destroyDeployment tears down an existing deployment: detect where it lives (for
// platforms that need the deployed id, e.g. Heroku), then call the platform's
// Destroy. Structurally a slimmer sibling of rollbackDeployment — no "previous
// revision" step, since destroy removes the service outright.
func (w *Workflows) destroyDeployment(ctx workflow.Context, plan DeployPlan) (deployResult, error) {
	workflow.Logger(ctx).Info("starting destroy workflow", "platform", plan.Platform, "project", plan.Spec.Name)

	operationId, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentLogDeploymentStart, plan.Platform.String(), plan.Spec, plan.Source, plan.Action).Get(ctx)
	if err != nil {
		workflow.Logger(ctx).Warn("failed to log destroy start", "error", err)
	}

	// Resolve the deployed id where a platform needs it (Heroku); platforms that
	// derive the target from the spec (Cloud Run/Fly/Azure/App Runner) don't require
	// detection to succeed.
	existingProject := plan.ExistingProjectInfo
	if !existingProject.Exists || existingProject.ProjectID == "" || existingProject.Platform != plan.Platform {
		detected, derr := workflow.ExecuteActivity[ExistingProjectInfo](ctx, ActivityOpts, AgentCheckExistingProject, plan.Platform, plan.Spec.Name, plan.Source).Get(ctx)
		if derr == nil && detected.Exists {
			existingProject = detected
		}
	}

	db := deployment.NewDeploymentBuilder(&plan.Spec, plan.CollectedEnvVars)
	spec, err := db.Build()
	if err != nil {
		failDestroy(ctx, operationId, plan.Platform.String(), "spec_build", err)
		return deployResult{Error: deployError{Summary: fmt.Sprintf("Failed to build deployment spec: %v", err)}}, nil
	}
	spec.ExistingProjectID = existingProject.ProjectID

	if _, err := workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentDestroyDeployment, *spec, plan.Platform).Get(ctx); err != nil {
		workflow.Logger(ctx).Error("failed to destroy deployment", "error", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     DestroyDeploymentWorkflowName,
			"activity":     AgentDestroyDeployment,
			"component":    "workflow",
			"platform":     plan.Platform.String(),
			"project_name": spec.Name,
		})
		failDestroy(ctx, operationId, plan.Platform.String(), "destroy_execution", err)
		return deployResult{Error: deployError{Summary: fmt.Sprintf("Failed to destroy deployment: %v", err)}}, nil
	}

	if operationId != "" {
		workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "success", map[string]any{
			"platform": plan.Platform.String(),
			"stage":    "destroy",
		}).Get(ctx)
	}
	workflow.Logger(ctx).Info("destroy completed successfully")
	return deployResult{}, nil
}

// failDestroy records a destroy failure against the operation log (best-effort).
func failDestroy(ctx workflow.Context, operationId, platform, stage string, err error) {
	if operationId == "" {
		return
	}
	workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
		"error":    err.Error(),
		"platform": platform,
		"stage":    stage,
	}).Get(ctx)
}
