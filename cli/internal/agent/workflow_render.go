package agent

import (
	"log/slog"
	"net/url"

	"github.com/cschleiden/go-workflows/workflow"
	"github.com/go-errors/errors"
	"github.com/pushtoprodai/prod-cli/internal/deployment"
	"github.com/pushtoprodai/prod-cli/internal/deployment/render"
	prod_error "github.com/pushtoprodai/prod-cli/internal/error"
)

func (w *Workflows) deployRender(ctx workflow.Context, input DeployPlan) (deployResult, error) {
	if w.registry == nil {
		return deployResult{}, errors.New("workflow registry is not set")
	}

	// Log deployment start
	operationId, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentLogDeploymentStart, "render", input.Spec, input.Source, input.Action).Get(ctx)
	if err != nil {
		slog.Error("Failed to log deployment start", "error", err)
		// Continue with deployment even if logging fails
	}

	// Use existing project info from DeployPlan
	existingProject := input.ExistingProjectInfo
	if existingProject.Exists {
		slog.Info("Using existing project from detection", "name", existingProject.Name, "id", existingProject.ProjectID, "databases", existingProject.ExistingDatabases)
	}

	// Validate Docker availability for Render
	if !deployment.IsDockerAvailable() {
		if operationId != "" {
			workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
				"error":    "Docker not available",
				"platform": "render",
				"stage":    "docker_validation",
			}).Get(ctx)
		}
		summary, err2 := workflow.ExecuteActivity[deployError](ctx, ActivityOpts, AgentSummarizeError, "not able to build docker image. cannot connect to local docker daemon", input).Get(ctx)
		if err2 != nil {
			return deployResult{Error: deployError{Summary: "not able to build docker image. cannont connect to local docker daemon"}}, nil
		}
		return deployResult{Error: summary}, nil
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
				"platform": "render",
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

	// Generate and summarize deployment steps (for UI feedback)
	workspaceID, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentGetRenderWorkspace).Get(ctx)
	if err != nil {
		// Send the original error before summarizing
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     DeployRenderWorkflowName,
			"activity":     AgentGetRenderWorkspace,
			"component":    "deployment",
			"platform":     "render",
			"project_name": input.Spec.Name,
			"language":     input.Spec.Language,
		})
		summary, e1 := workflow.ExecuteActivity[deployError](ctx, ActivityOpts, AgentSummarizeError, err.Error(), input).Get(ctx)
		if e1 != nil {
			// Send the summarize error
			prod_error.CaptureErrorWithContext(e1, map[string]any{
				"workflow":     DeployRenderWorkflowName,
				"activity":     AgentSummarizeError,
				"component":    "deployment",
				"platform":     "render",
				"project_name": input.Spec.Name,
				"language":     input.Spec.Language,
				"operation":    "summarize_original_error",
			})
			slog.Info("Failed to summarize error", "error", e1)
			if operationId != "" {
				workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
					"error":    err.Error(),
					"platform": "render",
					"stage":    "get_workspace",
				}).Get(ctx)
			}
			return deployResult{Error: deployError{Summary: err.Error()}}, nil
		}
		if operationId != "" {
			workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
				"error":    err.Error(),
				"platform": "render",
				"stage":    "get_workspace",
			}).Get(ctx)
		}
		return deployResult{Error: summary}, nil
	}

	// No separate repository-creation step: repositories auto-create on the first
	// push to the user's own registry (Docker Hub / GHCR / generic).
	dockerGen := deployment.NewDockerGenerator(w.uiWriter, spec.EnvVars)
	d := render.NewQueuedDeployment(w.renderClient, spec, dockerGen, true, w.uiWriter)
	steps := d.GenerateAPISteps(workspaceID)
	descriptions := make([]string, len(steps))
	for i, step := range steps {
		descriptions[i] = step.GetDescription()
	}
	_, err = workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentSummarizeDeploySteps, descriptions).Get(ctx)
	if err != nil {
		slog.Info("Failed to summarize deployment steps", "error", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     DeployRenderWorkflowName,
			"activity":     AgentSummarizeDeploySteps,
			"component":    "deployment",
			"platform":     "render",
			"project_name": input.Spec.Name,
			"language":     input.Spec.Language,
		})
	}

	buildOutputPath, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentDetermineBuildOutput, input.Spec.BuildOutput).Get(ctx)
	if err != nil {
		slog.Info("Failed to determine build output path", "error", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     DeployRenderWorkflowName,
			"activity":     AgentDetermineBuildOutput,
			"component":    "deployment",
			"platform":     "render",
			"project_name": input.Spec.Name,
			"language":     input.Spec.Language,
		})
	} else {
		slog.Info("Using build output path", "path", buildOutputPath)
		// Update the deployment spec's OutputDir with the final resolved build output path
		spec.OutputDir = buildOutputPath
	}

	createdResources, err := workflow.ExecuteActivity[[]deployment.CreatedResource](ctx, ActivityOpts, AgentDeploySteps, *spec, input.Platform).Get(ctx)
	if err != nil {
		// Send the original error before summarizing
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     DeployRenderWorkflowName,
			"activity":     AgentDeploySteps,
			"component":    "deployment",
			"platform":     "render",
			"project_name": input.Spec.Name,
			"language":     input.Spec.Language,
		})
		summary, e1 := workflow.ExecuteActivity[deployError](ctx, ActivityOpts, AgentSummarizeError, err.Error(), input).Get(ctx)
		if e1 != nil {
			// Send the summarize error
			prod_error.CaptureErrorWithContext(e1, map[string]any{
				"workflow":     DeployRenderWorkflowName,
				"activity":     AgentSummarizeError,
				"component":    "deployment",
				"platform":     "render",
				"project_name": input.Spec.Name,
				"language":     input.Spec.Language,
				"operation":    "summarize_original_error",
			})
			if operationId != "" {
				workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
					"error":    err.Error(),
					"platform": "render",
					"stage":    "deploy_steps",
				}).Get(ctx)
			}
			return deployResult{Error: deployError{Summary: err.Error()}}, nil
		}
		slog.Info("Deployment failed", "error", err)
		if operationId != "" {
			workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
				"error":    err.Error(),
				"platform": "render",
				"stage":    "deploy_steps",
			}).Get(ctx)
		}
		return deployResult{Error: summary}, nil
	}

	// A worker/cron shape has no HTTP URL to probe. Skip the whole URL/liveness
	// block below: GetRenderServiceURL returns empty for a portless
	// background_worker, and an HTTP liveness probe would fail a non-listening
	// process (auto-failing an otherwise healthy deploy). Record success with no
	// URL. (Mirror of the Fly worker path.)
	if !input.Shape.HTTPShaped() {
		var svc deployment.CreatedResource
		for _, cr := range createdResources {
			if cr.ID != "" {
				svc = cr
				break
			}
		}
		if operationId != "" {
			workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "success", map[string]any{
				"platform":          "render",
				"resources_created": createdResources,
				"resourceId":        svc.ID,
				// Persist the shape so ls/open/status know this Render record is a URL-less
				// worker/cron by design (mirrors the Fly worker path).
				"shape": input.Shape.String(),
			}).Get(ctx)
		}
		return deployResult{Url: ""}, nil
	}

	// Find web service resource
	var ws deployment.CreatedResource
	for _, cr := range createdResources {
		if cr.Type == "web_service" {
			ws = cr
			break
		}
	}
	if ws.ID == "" {
		return deployResult{}, nil
	}

	// Get service URL
	u, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentGetRenderServiceURL, ws.ID).Get(ctx)
	if err != nil {
		return deployResult{}, errors.Errorf("failed to get service URL for %s: %w", ws.Name, err)
	}

	// First, check deploy status (for both fresh and updates)
	if deployID, ok := ws.Metadata["deployId"].(string); ok && deployID != "" {
		deployCheckOpts := ActivityOpts
		deployCheckOpts.RetryOptions.MaxAttempts = 15
		_, err := workflow.ExecuteActivity[any](ctx, deployCheckOpts, AgentWaitForRenderDeploy, ws.ID, deployID).Get(ctx)
		if err != nil {
			prod_error.CaptureErrorWithContext(err, map[string]any{
				"workflow":     DeployRenderWorkflowName,
				"activity":     AgentWaitForRenderDeploy,
				"component":    "deployment",
				"platform":     "render",
				"project_name": input.Spec.Name,
				"language":     input.Spec.Language,
			})
			summary, e1 := workflow.ExecuteActivity[deployError](ctx, ActivityOpts, AgentSummarizeError, err.Error(), input).Get(ctx)
			if e1 != nil {
				prod_error.CaptureErrorWithContext(e1, map[string]any{
					"workflow":     DeployRenderWorkflowName,
					"activity":     AgentSummarizeError,
					"component":    "deployment",
					"platform":     "render",
					"project_name": input.Spec.Name,
					"language":     input.Spec.Language,
					"operation":    "summarize_original_error",
				})
				if operationId != "" {
					workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
						"error":     err.Error(),
						"platform":  "render",
						"stage":     "wait_for_deploy",
						"deploy_id": deployID,
					}).Get(ctx)
				}
				return deployResult{Error: deployError{Summary: err.Error()}}, nil
			}
			slog.Info("deployment failed", "deployId", deployID, "error", err)
			if operationId != "" {
				workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
					"error":     err.Error(),
					"platform":  "render",
					"stage":     "wait_for_deploy",
					"deploy_id": deployID,
				}).Get(ctx)
			}
			return deployResult{Error: summary}, nil
		}
	}

	// Then verify URL is live
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
	_, err = workflow.ExecuteActivity[string](ctx, liveCheckOpts, AgentVerifyLiveness, input.Shape, fullUrl).Get(ctx)
	if err != nil {
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     DeployRenderWorkflowName,
			"activity":     AgentVerifyLiveness,
			"component":    "deployment",
			"platform":     "render",
			"project_name": input.Spec.Name,
			"language":     input.Spec.Language,
		})
		summary, e1 := workflow.ExecuteActivity[deployError](ctx, ActivityOpts, AgentSummarizeError, err.Error(), input).Get(ctx)
		if e1 != nil {
			prod_error.CaptureErrorWithContext(e1, map[string]any{
				"workflow":     DeployRenderWorkflowName,
				"activity":     AgentSummarizeError,
				"component":    "deployment",
				"platform":     "render",
				"project_name": input.Spec.Name,
				"language":     input.Spec.Language,
				"operation":    "summarize_original_error",
			})
			if operationId != "" {
				workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
					"error":    err.Error(),
					"platform": "render",
					"stage":    "url_check",
					"url":      fullUrl,
				}).Get(ctx)
			}
			return deployResult{Error: deployError{Summary: err.Error()}}, nil
		}
		slog.Info("service URL is not live", "url", fullUrl, "error", err)
		if operationId != "" {
			workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
				"error":    err.Error(),
				"platform": "render",
				"stage":    "url_check",
				"url":      fullUrl,
			}).Get(ctx)
		}
		return deployResult{Error: summary}, nil
	}

	// Log deployment success
	if operationId != "" {
		workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "success", map[string]any{
			"url":        fullUrl,
			"platform":   "render",
			"resourceId": ws.ID, // srv-… — needed to build Render's console URL + logs
		}).Get(ctx)
	}

	return deployResult{Url: fullUrl}, nil
}
