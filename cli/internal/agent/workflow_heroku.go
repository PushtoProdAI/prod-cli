package agent

import (
	"fmt"
	"log/slog"
	"net/url"

	"github.com/cschleiden/go-workflows/workflow"
	"github.com/go-errors/errors"
	"github.com/pushtoprodai/prod-cli/internal/deployment"
	"github.com/pushtoprodai/prod-cli/internal/deployment/heroku"
	prod_error "github.com/pushtoprodai/prod-cli/internal/error"
)

func (w *Workflows) deployHeroku(ctx workflow.Context, input DeployPlan) (deployResult, error) {
	slog.Info("deployHeroku workflow started", "platform", input.Platform)
	slog.Info("DeployPlan details", "action", input.Action, "source", input.Source, "specName", input.Spec.Name, "specLanguage", input.Spec.Language)

	// Use existing project info from DeployPlan
	existingProject := input.ExistingProjectInfo
	if existingProject.Exists {
		slog.Info("Using existing project from detection", "name", existingProject.Name, "id", existingProject.ProjectID, "databases", existingProject.ExistingDatabases)
	}

	// Log deployment start
	operationId, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentLogDeploymentStart, "heroku", input.Spec, input.Source, input.Action).Get(ctx)
	if err != nil {
		slog.Error("Failed to log deployment start", "error", err)
		// Continue with deployment even if logging fails
	}

	// Build deployment spec from the plan
	slog.Info("Building deployment spec")
	db := deployment.NewDeploymentBuilder(&input.Spec, input.CollectedEnvVars)
	spec, err := db.Build()
	if err != nil {
		slog.Info("Failed to build deployment spec", "error", err)
		return deployResult{Error: deployError{Summary: fmt.Sprintf("Failed to build deployment spec: %v", err)}}, nil
	}
	slog.Info("Deployment spec built successfully")

	// Add metadata
	spec.Metadata["buildContext"] = input.Source
	spec.Metadata["platform"] = "heroku"

	// Set update mode if existing project detected
	if existingProject.Exists {
		spec.IsUpdate = true
		spec.ExistingProjectID = existingProject.ProjectID
		spec.ExistingDatabases = existingProject.ExistingDatabases
		slog.Info("Set spec for existing project", "ExistingProjectID", spec.ExistingProjectID, "IsUpdate", spec.IsUpdate, "Name", existingProject.Name)
	}

	// Use default Heroku adapter
	herokuAdapter := heroku.NewDefaultHerokuDeploymentAdapter(w.uiWriter, w.llmClient)
	d, err := herokuAdapter.GenerateArtifactsWithSource(spec, deployment.StrategyHeroku, input.Source)
	if err != nil {
		slog.Error("Failed to generate Heroku deployment", "error", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     DeployHerokuWorkflowName,
			"activity":     "generate_artifacts", // This is not an activity, it's a local operation
			"component":    "deployment",
			"platform":     "heroku",
			"project_name": input.Spec.Name,
			"language":     input.Spec.Language,
		})
		return deployResult{Error: deployError{Summary: fmt.Sprintf("Failed to generate deployment: %v", err)}}, nil
	}

	// Generate steps for summary
	if qd, ok := d.(*heroku.QueuedDeployment); ok {
		steps := qd.GenerateAPISteps()
		descriptions := make([]string, len(steps))
		for i, step := range steps {
			descriptions[i] = step.GetDescription()
		}
		_, err = workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentSummarizeDeploySteps, descriptions).Get(ctx)
		if err != nil {
			slog.Error("Failed to summarize deployment steps", "error", err)
			prod_error.CaptureErrorWithContext(err, map[string]any{
				"workflow":     DeployHerokuWorkflowName,
				"activity":     AgentSummarizeDeploySteps,
				"component":    "deployment",
				"platform":     "heroku",
				"project_name": input.Spec.Name,
				"language":     input.Spec.Language,
			})
		}
	}

	// Estimate costs
	estimatedCosts, err := workflow.ExecuteActivity[deployment.CostEstimate](ctx, ActivityOpts, AgentEstimateHerokuCosts, *spec, deployment.StrategyHeroku).Get(ctx)
	if err != nil {
		slog.Error("Error estimating costs", "error", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     DeployHerokuWorkflowName,
			"activity":     AgentEstimateHerokuCosts,
			"component":    "deployment",
			"platform":     "heroku",
			"project_name": input.Spec.Name,
			"language":     input.Spec.Language,
		})
	} else {
		slog.Info("Estimated monthly costs", "total", estimatedCosts.Total, "services", len(estimatedCosts.Services))
	}

	// Use limited retries for Heroku deployment (it has long-running git operations)
	deployOpts := ActivityOpts
	if input.Platform == Heroku {
		deployOpts.RetryOptions.MaxAttempts = 2 // Only retry once for Heroku
	}

	createdResources, err := workflow.ExecuteActivity[[]deployment.CreatedResource](ctx, deployOpts, AgentDeploySteps, *spec, input.Platform).Get(ctx)
	if err != nil {
		// Send the original error before summarizing
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     DeployHerokuWorkflowName,
			"activity":     AgentDeploySteps,
			"component":    "deployment",
			"platform":     "heroku",
			"project_name": input.Spec.Name,
			"language":     input.Spec.Language,
		})
		summary, e1 := workflow.ExecuteActivity[deployError](ctx, ActivityOpts, AgentSummarizeError, err.Error(), input).Get(ctx)
		if e1 != nil {
			// Send the summarize error
			prod_error.CaptureErrorWithContext(e1, map[string]any{
				"workflow":     DeployHerokuWorkflowName,
				"activity":     AgentSummarizeError,
				"component":    "deployment",
				"platform":     "heroku",
				"project_name": input.Spec.Name,
				"language":     input.Spec.Language,
				"operation":    "summarize_original_error",
			})
			return deployResult{Error: deployError{Summary: err.Error()}}, nil
		}
		slog.Error("Deployment failed", "error", err)
		return deployResult{Error: summary}, nil
	}

	// Extract deployment URL and app name from created resources
	var deploymentURL string
	var appName string
	for _, resource := range createdResources {
		if url, ok := resource.Metadata["url"].(string); ok {
			deploymentURL = url
		}
		if resource.Type == "heroku_app" {
			appName = resource.Name
		}
	}

	if deploymentURL == "" {
		slog.Info("No deployment URL found in created resources")
		deploymentURL = "Deployment completed but URL not available"
	}

	// Store app name in spec for rollback operations (only if not already set from existing project)
	if appName != "" && spec.ExistingProjectID == "" {
		spec.ExistingProjectID = appName
	}

	path, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentDetermineRootPath, input.Spec.Routes).Get(ctx)
	if err != nil {
		// if there is an error, we will just default to /
		slog.Info("Failed to determine root path for application", "error", err)
		path = "/"
	}

	fullUrl, err := url.JoinPath(deploymentURL, path)
	if err != nil {
		slog.Info("Failed to combine paths", "error", err)
		fullUrl = deploymentURL
	}

	_, err = workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentIsURLLive, fullUrl).Get(ctx)
	if err != nil {
		slog.Error("Health check failed, attempting rollback", "error", err, "url", fullUrl)

		previousDeploy, rollbackErr := workflow.ExecuteActivity[*deployment.DeploymentInfo](ctx, ActivityOpts, AgentGetPreviousDeployment, *spec, Heroku).Get(ctx)
		if rollbackErr != nil {
			slog.Warn("No previous deployment available for rollback", "error", rollbackErr)
			if operationId != "" {
				workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
					"error":              err.Error(),
					"platform":           "heroku",
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

		slog.Info("Found previous deployment for rollback", "deployment_id", previousDeploy.ID)

		_, rollbackErr = workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentRollbackDeployment, *spec, Heroku, previousDeploy.ID).Get(ctx)
		if rollbackErr != nil {
			slog.Error("Rollback failed", "error", rollbackErr, "target_deployment", previousDeploy.ID)
			if operationId != "" {
				workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
					"error":           err.Error(),
					"platform":        "heroku",
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
				"platform":       "heroku",
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
			"platform":          "heroku",
			"resources_created": createdResources,
		}).Get(ctx)
	}

	slog.Info("Heroku deployment workflow completed successfully")
	return deployResult{Url: fullUrl}, nil
}
