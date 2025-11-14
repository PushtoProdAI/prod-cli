package agent

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"github.com/cschleiden/go-workflows/workflow"
	"github.com/go-errors/errors"
	"github.com/meroxa/prod/cli/internal/deployment"
	"github.com/meroxa/prod/cli/internal/deployment/flyio"
	prod_error "github.com/meroxa/prod/cli/internal/error"
)

// extractFlyioStepConfig extracts configuration from a Fly.io deployment step
func extractFlyioStepConfig(step flyio.FlyioAPIStep) map[string]any {
	config := make(map[string]any)

	// Since the fields are unexported, we'll just use the step description
	// and type information that's available through the interface
	config["step_id"] = step.GetID()
	config["description"] = step.GetDescription()

	return config
}

// performFlyioConflictChecks checks for resource conflicts in a Fly.io deployment
func performFlyioConflictChecks(spec *deployment.DeploymentSpec, client flyio.FlyioClient) []ConflictCheck {
	var conflicts []ConflictCheck

	// Check for app name conflicts by attempting to get the app
	// Use a timeout to prevent hanging
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := client.GetApp(ctx, spec.Name)
	if err == nil {
		// App exists, this is a conflict
		conflicts = append(conflicts, ConflictCheck{
			Resource: "app",
			Status:   "conflict",
			Message:  fmt.Sprintf("App name '%s' already exists", spec.Name),
		})
	}

	return conflicts
}

// getFlyioStepType returns the type of a Fly.io deployment step
func getFlyioStepType(step flyio.FlyioAPIStep) string {
	switch step.(type) {
	case *flyio.CreateFlyioAppStep:
		return "app"
	case *flyio.CreateFlyioServiceStep:
		return "service"
	case *flyio.DeployFlyioConfigStep:
		return "config"
	case *flyio.AttachPostgresStep:
		return "attach"
	default:
		return "unknown"
	}
}

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
	db := deployment.NewDeploymentBuilder(&input.Spec, envVars)
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

	_, err = workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentIsURLLive, fullUrl).Get(ctx)
	if err != nil {
		// Send the original error before summarizing
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     DeployFlyioWorkflowName,
			"activity":     AgentIsURLLive,
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

func (w *Workflows) dryRunDeployFly(ctx workflow.Context, input DeployPlan) (DryRunResult, error) {
	if w.registry == nil {
		return DryRunResult{}, errors.New("workflow registry is not set")
	}

	credentialStatus := make(map[string]bool)
	// Check Fly.io credentials by attempting to get an app (this will fail if not authenticated)
	// Use a timeout to prevent hanging
	checkCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := w.flyClient.GetApp(checkCtx, "test-app")
	if err != nil {
		credentialStatus["Fly.io API"] = false
	} else {
		credentialStatus["Fly.io API"] = true
	}

	envVars := input.CollectedEnvVars

	// Build deployment spec
	db := deployment.NewDeploymentBuilder(&input.Spec, envVars)
	spec, err := db.Build()
	if err != nil {
		return DryRunResult{}, errors.Errorf("failed to build deployment spec: %w", err)
	}

	spec.Metadata["buildContext"] = input.Source

	// Generate deployment steps
	dockerGen := deployment.NewDockerGenerator(w.uiWriter, spec.EnvVars)
	d := flyio.NewFlyioQueuedDeployment(w.flyClient, spec, dockerGen, w.uiWriter)
	steps := d.GenerateAPISteps()

	dryRunSteps := make([]DryRunStep, len(steps))
	for i, step := range steps {
		dryRunSteps[i] = DryRunStep{
			ID:          step.GetID(),
			Description: step.GetDescription(),
			Type:        getFlyioStepType(step),
			Config:      extractFlyioStepConfig(step),
			DependsOn:   step.GetDependencies(),
		}
	}

	// Estimate costs
	estimatedCosts, err := workflow.ExecuteActivity[deployment.CostEstimate](ctx, ActivityOpts, AgentEstimateFlyioCosts, *spec, deployment.StrategyFlyio).Get(ctx)
	if err != nil {
		slog.Info("Failed to estimate costs", "error", err)
		estimatedCosts = deployment.CostEstimate{}
	}

	// Perform conflict checks and validation
	conflictChecks := performFlyioConflictChecks(spec, w.flyClient)
	validationErrors := validateDeploymentSpec(spec)

	return DryRunResult{
		Steps:            dryRunSteps,
		EstimatedCosts:   estimatedCosts,
		CredentialStatus: credentialStatus,
		ConflictChecks:   conflictChecks,
		ValidationErrors: validationErrors,
	}, nil
}
