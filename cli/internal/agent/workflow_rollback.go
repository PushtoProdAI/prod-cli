package agent

import (
	"fmt"
	"log/slog"

	"github.com/cschleiden/go-workflows/workflow"
	"github.com/pushtoprodai/prod-cli/internal/deployment"
	prod_error "github.com/pushtoprodai/prod-cli/internal/error"
)

func (w *Workflows) rollbackDeployment(ctx workflow.Context, plan DeployPlan) (deployResult, error) {
	workflow.Logger(ctx).Info("starting rollback workflow", "platform", plan.Platform, "project", plan.Spec.Name)

	// Log deployment start
	operationId, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentLogDeploymentStart, plan.Platform.String(), plan.Spec, plan.Source, plan.Action).Get(ctx)
	if err != nil {
		slog.Error("Failed to log deployment start", "error", err)
		// Continue with rollback even if logging fails
	}

	// For platforms that need existing project info, we need to detect it first if not already available
	// or if the existing info is from a different platform (multi-platform case)
	existingProject := plan.ExistingProjectInfo
	if !existingProject.Exists || existingProject.ProjectID == "" || existingProject.Platform != plan.Platform {
		workflow.Logger(ctx).Info("detecting existing project for rollback", "platform", plan.Platform)
		detectedProject, err := workflow.ExecuteActivity[ExistingProjectInfo](ctx, ActivityOpts, AgentCheckExistingProject, plan.Platform, plan.Spec.Name, plan.Source).Get(ctx)
		if err != nil {
			workflow.Logger(ctx).Error("Failed to detect existing project", "error", err)
			prod_error.CaptureErrorWithContext(err, map[string]any{
				"workflow":     RollbackDeploymentWorkflowName,
				"activity":     AgentCheckExistingProject,
				"component":    "workflow",
				"platform":     plan.Platform.String(),
				"project_name": plan.Spec.Name,
			})
			if operationId != "" {
				workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
					"error":    err.Error(),
					"platform": plan.Platform.String(),
					"stage":    "check_existing",
				}).Get(ctx)
			}
			return deployResult{
				Error: deployError{
					Summary: "Could not find existing deployment to rollback. Please make sure the application is deployed.",
				},
			}, nil
		}

		if !detectedProject.Exists {
			workflow.Logger(ctx).Warn("No existing deployment found for rollback")
			if operationId != "" {
				workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
					"error":    "No existing deployment found",
					"platform": plan.Platform.String(),
					"stage":    "check_existing",
				}).Get(ctx)
			}
			return deployResult{
				Error: deployError{
					Summary: "No existing deployment found to rollback. Please make sure the application is deployed.",
				},
			}, nil
		}

		existingProject = detectedProject
	}

	// Build deployment spec from the plan
	workflow.Logger(ctx).Info("Building deployment spec for rollback")
	db := deployment.NewDeploymentBuilder(&plan.Spec, plan.CollectedEnvVars, plan.Shape)
	spec, err := db.Build()
	if err != nil {
		workflow.Logger(ctx).Error("Failed to build deployment spec", "error", err)
		if operationId != "" {
			workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
				"error":    err.Error(),
				"platform": plan.Platform.String(),
				"stage":    "spec_build",
			}).Get(ctx)
		}
		return deployResult{Error: deployError{Summary: fmt.Sprintf("Failed to build deployment spec: %v", err)}}, nil
	}

	// Get auth token from session for platforms that need it (e.g., AWS)
	token := ""
	session := CtxWorkflowSession(ctx)
	if session != nil {
		token = session.AccessToken
	}
	if token != "" {
		spec.Metadata["authToken"] = token
	}

	// Set existing project info and rollback flag
	spec.IsUpdate = true
	spec.IsRollback = true
	spec.ExistingProjectID = existingProject.ProjectID
	spec.ExistingDatabases = existingProject.ExistingDatabases

	// Get the previous deployment to rollback to
	previousDeploy, err := workflow.ExecuteActivity[*deployment.DeploymentInfo](ctx, ActivityOpts, AgentGetPreviousDeployment, *spec, plan.Platform).Get(ctx)
	if err != nil {
		workflow.Logger(ctx).Error("Failed to get previous deployment", "error", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     RollbackDeploymentWorkflowName,
			"activity":     AgentGetPreviousDeployment,
			"component":    "workflow",
			"platform":     plan.Platform.String(),
			"project_name": spec.Name,
		})
		if operationId != "" {
			workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
				"error":    err.Error(),
				"platform": plan.Platform.String(),
				"stage":    "get_previous_deployment",
			}).Get(ctx)
		}
		return deployResult{
			Error: deployError{
				Summary: "No previous deployment found to rollback to. This might be your first deployment.",
			},
		}, nil
	}

	if previousDeploy == nil {
		workflow.Logger(ctx).Warn("No previous deployment available for rollback")
		if operationId != "" {
			workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
				"error":    "No previous deployment available",
				"platform": plan.Platform.String(),
				"stage":    "get_previous_deployment",
			}).Get(ctx)
		}
		return deployResult{
			Error: deployError{
				Summary: "No previous deployment found to rollback to. This might be your first deployment.",
			},
		}, nil
	}

	workflow.Logger(ctx).Info("Found previous deployment", "deployment_id", previousDeploy.ID)

	// Execute the rollback
	_, err = workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentRollbackDeployment, *spec, plan.Platform, previousDeploy.ID).Get(ctx)
	if err != nil {
		workflow.Logger(ctx).Error("Failed to rollback deployment", "error", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":          RollbackDeploymentWorkflowName,
			"activity":          AgentRollbackDeployment,
			"component":         "workflow",
			"platform":          plan.Platform.String(),
			"project_name":      spec.Name,
			"target_deployment": previousDeploy.ID,
		})
		if operationId != "" {
			workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
				"error":             err.Error(),
				"platform":          plan.Platform.String(),
				"stage":             "rollback_execution",
				"target_deployment": previousDeploy.ID,
			}).Get(ctx)
		}
		return deployResult{
			Error: deployError{
				Summary: fmt.Sprintf("Failed to rollback deployment: %v", err),
			},
		}, nil
	}

	workflow.Logger(ctx).Info("Rollback initiated successfully")

	workflow.Logger(ctx).Info("Rollback completed successfully")

	// Log rollback success
	if operationId != "" {
		workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "success", map[string]any{
			"url":               previousDeploy.URL,
			"platform":          plan.Platform.String(),
			"target_deployment": previousDeploy.ID,
		}).Get(ctx)
	}

	return deployResult{
		Url: previousDeploy.URL,
	}, nil
}
