package agent

import (
	"log/slog"
	"net/url"

	"github.com/cschleiden/go-workflows/workflow"
	"github.com/go-errors/errors"
	"github.com/pushtoprodai/prod-cli/internal/deployment"
	"github.com/pushtoprodai/prod-cli/internal/deployment/flyio"
	prod_error "github.com/pushtoprodai/prod-cli/internal/error"
)

func (w *Workflows) deployFly(ctx workflow.Context, input DeployPlan) (deployResult, error) {
	if w.registry == nil {
		return deployResult{}, errors.New("workflow registry is not set")
	}

	// Use existing project info from DeployPlan
	existingProject := input.ExistingProjectInfo
	if existingProject.Exists {
		slog.Info("Using existing project from detection", "name", existingProject.Name, "id", existingProject.ProjectID, "databases", existingProject.ExistingDatabases)
	}

	// Log deployment start
	operationId, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentLogDeploymentStart, "flyio", input.Spec, input.Source, input.Action).Get(ctx)
	if err != nil {
		slog.Error("Failed to log deployment start", "error", err)
		// Continue with deployment even if logging fails
	}

	envVars := input.CollectedEnvVars

	// Build deployment spec
	db := deployment.NewDeploymentBuilder(&input.Spec, envVars, input.Shape)
	spec, err := db.Build()
	if err != nil {
		// Log deployment failure
		if operationId != "" {
			workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
				"error":    err.Error(),
				"platform": "flyio",
				"stage":    "spec_build",
			}).Get(ctx)
		}
		return deployResult{}, errors.Errorf("failed to build deployment spec: %w", err)
	}
	spec.Metadata["buildContext"] = input.Source

	// Set update mode if existing project detected
	if existingProject.Exists {
		spec.IsUpdate = true
		spec.ExistingProjectID = existingProject.ProjectID
		spec.ExistingDatabases = existingProject.ExistingDatabases
	}

	// Generate and summarize deployment steps
	dockerGen := deployment.NewDockerGenerator(w.uiWriter, spec.EnvVars)
	d := flyio.NewFlyioQueuedDeployment(w.flyClient, spec, dockerGen, w.uiWriter)
	steps := d.GenerateAPISteps()
	descriptions := make([]string, len(steps))
	for i, step := range steps {
		descriptions[i] = step.GetDescription()
	}
	_, err = workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentSummarizeDeploySteps, descriptions).Get(ctx)
	if err != nil {
		slog.Info("Failed to summarize deployment steps", "error", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     DeployFlyioWorkflowName,
			"activity":     AgentSummarizeDeploySteps,
			"component":    "deployment",
			"platform":     "flyio",
			"project_name": input.Spec.Name,
			"language":     input.Spec.Language,
		})
	}

	createdResources, err := workflow.ExecuteActivity[[]deployment.CreatedResource](ctx, ActivityOpts, AgentDeploySteps, *spec, input.Platform).Get(ctx)
	if err != nil {
		// Send the original error before summarizing
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     DeployFlyioWorkflowName,
			"activity":     AgentDeploySteps,
			"component":    "deployment",
			"platform":     "flyio",
			"project_name": input.Spec.Name,
			"language":     input.Spec.Language,
		})

		slog.Error("Deployment failed, attempting rollback", "error", err)

		previousDeploy, rollbackErr := workflow.ExecuteActivity[*deployment.DeploymentInfo](ctx, ActivityOpts, AgentGetPreviousDeployment, *spec, FlyIO).Get(ctx)
		if rollbackErr != nil {
			slog.Warn("No previous deployment available for rollback", "error", rollbackErr)
			if operationId != "" {
				workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
					"error":              err.Error(),
					"platform":           "flyio",
					"stage":              "deployment_steps",
					"no_previous_deploy": true,
				}).Get(ctx)
			}
			return deployResult{
				Error: deployError{
					Summary: "Deployment failed. This is your first deployment, so there's no previous version to roll back to",
				},
			}, nil
		}

		slog.Info("Found previous deployment for rollback", "image", previousDeploy.ID)

		_, rollbackErr = workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentRollbackDeployment, *spec, FlyIO, previousDeploy.ID).Get(ctx)
		if rollbackErr != nil {
			slog.Error("Rollback failed", "error", rollbackErr, "target_image", previousDeploy.ID)
			if operationId != "" {
				workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
					"error":           err.Error(),
					"platform":        "flyio",
					"stage":           "deployment_steps",
					"rollback_error":  rollbackErr.Error(),
					"rollback_target": previousDeploy.ID,
				}).Get(ctx)
			}
			summary, e1 := workflow.ExecuteActivity[deployError](ctx, ActivityOpts, AgentSummarizeError, err.Error(), input).Get(ctx)
			if e1 != nil {
				prod_error.CaptureErrorWithContext(e1, map[string]any{
					"workflow":     DeployFlyioWorkflowName,
					"activity":     AgentSummarizeError,
					"component":    "deployment",
					"platform":     "flyio",
					"project_name": input.Spec.Name,
					"language":     input.Spec.Language,
					"operation":    "summarize_original_error",
				})
				return deployResult{Error: deployError{Summary: err.Error()}}, nil
			}
			return deployResult{Error: summary}, nil
		}

		slog.Info("Rollback completed successfully", "rolled_back_to", previousDeploy.ID)

		if operationId != "" {
			workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "rolled_back", map[string]any{
				"error":          err.Error(),
				"platform":       "flyio",
				"stage":          "deployment_steps",
				"rolled_back_to": previousDeploy.ID,
			}).Get(ctx)
		}
		return deployResult{
			Error: deployError{
				Summary:   "Deployment failed. We've automatically rolled back to your previous working version",
				IsWarning: true,
			},
		}, nil
	}

	// Find app resource
	var ws deployment.CreatedResource
	for _, cr := range createdResources {
		if cr.Type == "app" {
			ws = cr
			break
		}
	}
	if ws.ID == "" {
		return deployResult{}, nil
	}

	// Get app URL and verify it's live
	u, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentGetFlyIOAppURL, ws.ID).Get(ctx)
	if err != nil {
		return deployResult{}, errors.Errorf("failed to get service URL for %s: %w", ws.Name, err)
	}
	path, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentDetermineRootPath, input.Spec.Routes).Get(ctx)
	if err != nil {
		// if there is an error, we will just default to /
		slog.Info("Failed to determine root path for application", "error", err)
		path = "/"
	}

	fullUrl, err := url.JoinPath(u, path)
	if err != nil {
		slog.Info("Failed to combine paths", "error", err)
		fullUrl = u
	}

	_, err = workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentVerifyLiveness, input.Shape, fullUrl).Get(ctx)
	if err != nil {
		// Send the original error before summarizing
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     DeployFlyioWorkflowName,
			"activity":     AgentVerifyLiveness,
			"component":    "deployment",
			"platform":     "flyio",
			"project_name": input.Spec.Name,
			"language":     input.Spec.Language,
		})

		slog.Error("Health check failed, attempting rollback", "error", err, "url", fullUrl)

		previousDeploy, rollbackErr := workflow.ExecuteActivity[*deployment.DeploymentInfo](ctx, ActivityOpts, AgentGetPreviousDeployment, *spec, FlyIO).Get(ctx)
		if rollbackErr != nil {
			slog.Warn("No previous deployment available for rollback", "error", rollbackErr)
			if operationId != "" {
				workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
					"error":              err.Error(),
					"platform":           "flyio",
					"stage":              "url_verification",
					"url":                fullUrl,
					"no_previous_deploy": true,
				}).Get(ctx)
			}
			return deployResult{
				Error: deployError{
					Summary: "Deployment failed health check. This is your first deployment, so there's no previous version to roll back to",
				},
			}, nil
		}

		slog.Info("Found previous deployment for rollback", "image", previousDeploy.ID)

		_, rollbackErr = workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentRollbackDeployment, *spec, FlyIO, previousDeploy.ID).Get(ctx)
		if rollbackErr != nil {
			slog.Error("Rollback failed", "error", rollbackErr, "target_image", previousDeploy.ID)
			if operationId != "" {
				workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
					"error":           err.Error(),
					"platform":        "flyio",
					"stage":           "url_verification",
					"url":             fullUrl,
					"rollback_error":  rollbackErr.Error(),
					"rollback_target": previousDeploy.ID,
				}).Get(ctx)
			}
			return deployResult{}, errors.Errorf("service URL %s is not live and rollback failed: %w", fullUrl, err)
		}

		slog.Info("Rollback completed successfully", "rolled_back_to", previousDeploy.ID)

		if operationId != "" {
			workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "rolled_back", map[string]any{
				"error":          err.Error(),
				"platform":       "flyio",
				"stage":          "url_verification",
				"url":            fullUrl,
				"rolled_back_to": previousDeploy.ID,
			}).Get(ctx)
		}
		return deployResult{
			Error: deployError{
				Summary:   "Deployment failed health check. We've automatically rolled back to your previous working version",
				IsWarning: true,
			},
		}, nil
	}

	// Log deployment success
	if operationId != "" {
		workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "success", map[string]any{
			"url":               fullUrl,
			"platform":          "flyio",
			"resources_created": createdResources,
			"app_id":            ws.ID,
		}).Get(ctx)
	}

	return deployResult{Url: fullUrl}, nil
}
