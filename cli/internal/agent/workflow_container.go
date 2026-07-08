package agent

import (
	"log/slog"
	"net/url"
	"strings"

	"github.com/cschleiden/go-workflows/workflow"
	"github.com/go-errors/errors"
	"github.com/pushtoprodai/prod-cli/internal/deployment"
	prod_error "github.com/pushtoprodai/prod-cli/internal/error"
)

// deployContainer is the shared deploy workflow for managed-container clouds (App
// Runner, Cloud Run, Azure Container Apps — any platform with ManagedContainer set).
// The image build+push to the platform's registry, the service create/update, and
// the wait for ready all happen inside the AgentDeploySteps activity (see each
// container Deployable); this workflow builds the spec, runs that activity, finds the
// primary service resource, and verifies its URL is live. It replaces the per-platform
// workflow clones — the only thing that differed between them was three literal
// strings, now derived from input.Platform.
func (w *Workflows) deployContainer(ctx workflow.Context, input DeployPlan) (deployResult, error) {
	plat := strings.ToLower(input.Platform.String())

	operationId, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentLogDeploymentStart, plat, input.Spec, input.Source, input.Action).Get(ctx)
	if err != nil {
		slog.Error("Failed to log deployment start", "error", err)
		// Continue with deployment even if logging fails.
	}

	// Managed-container clouds deploy from a locally built container image.
	if !deployment.IsDockerAvailable() {
		if operationId != "" {
			workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
				"error": "Docker not available", "platform": plat, "stage": "docker_validation",
			}).Get(ctx)
		}
		summary, err2 := workflow.ExecuteActivity[deployError](ctx, ActivityOpts, AgentSummarizeError, "not able to build docker image. cannot connect to local docker daemon", input).Get(ctx)
		if err2 != nil {
			return deployResult{Error: deployError{Summary: "not able to build docker image. cannot connect to local docker daemon"}}, nil
		}
		return deployResult{Error: summary}, nil
	}

	// Build the deployment spec.
	db := deployment.NewDeploymentBuilder(&input.Spec, input.CollectedEnvVars, input.Shape)
	spec, err := db.Build()
	if err != nil {
		if operationId != "" {
			workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
				"error": err.Error(), "platform": plat, "stage": "spec_build",
			}).Get(ctx)
		}
		return deployResult{}, errors.Errorf("failed to build deployment spec: %w", err)
	}
	spec.Metadata["buildContext"] = input.Source
	if input.ExistingProjectInfo.Exists {
		spec.IsUpdate = true
	}

	// Build+push to the platform registry, create/update the service, wait for ready.
	createdResources, err := workflow.ExecuteActivity[[]deployment.CreatedResource](ctx, ActivityOpts, AgentDeploySteps, *spec, input.Platform).Get(ctx)
	if err != nil {
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow": DeployContainerWorkflowName, "activity": AgentDeploySteps,
			"component": "deployment", "platform": plat,
			"project_name": input.Spec.Name, "language": input.Spec.Language,
		})
		summary, e1 := workflow.ExecuteActivity[deployError](ctx, ActivityOpts, AgentSummarizeError, err.Error(), input).Get(ctx)
		if operationId != "" {
			workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
				"error": err.Error(), "platform": plat, "stage": "deploy_steps",
			}).Get(ctx)
		}
		if e1 != nil {
			return deployResult{Error: deployError{Summary: err.Error()}}, nil
		}
		return deployResult{Error: summary}, nil
	}

	// The container Deployable marks its service resource Primary and carries the URL.
	var svc deployment.CreatedResource
	for _, cr := range createdResources {
		if cr.Primary {
			svc = cr
			break
		}
	}
	if svc.ID == "" {
		return deployResult{}, errors.Errorf("%s deployment returned no primary service", input.Platform.String())
	}
	u, _ := svc.Metadata["url"].(string)
	if u == "" {
		return deployResult{}, errors.Errorf("%s deployment returned no URL", input.Platform.String())
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
			"workflow": DeployContainerWorkflowName, "activity": AgentVerifyLiveness,
			"component": "deployment", "platform": plat,
			"project_name": input.Spec.Name, "language": input.Spec.Language,
		})

		// Conditional auto-rollback (ACD.2): a container deploy that fails its health check
		// on a rollback-capable cloud (Cloud Run, Azure — not App Runner) is reverted to the
		// previous working revision. GetPreviousDeployment returns (nil, nil) for a first-ever
		// deploy, so a nil previous means "nothing to roll back to" and we fall through to
		// failed + remediation. (We deliberately gate on SupportsRollback and the presence of
		// a previous revision, not spec.IsUpdate: the container clouds have no existing-project
		// detector, so IsUpdate is never set — the previous-revision lookup is the real signal.)
		if p, ok := LookupPlatform(input.Platform); ok && p.SupportsRollback {
			previous, prevErr := workflow.ExecuteActivity[*deployment.DeploymentInfo](ctx, ActivityOpts, AgentGetPreviousDeployment, *spec, input.Platform).Get(ctx)
			if prevErr == nil && previous != nil {
				if _, rbErr := workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentRollbackDeployment, *spec, input.Platform, previous.ID).Get(ctx); rbErr == nil {
					if operationId != "" {
						workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "rolled_back", map[string]any{
							"error": err.Error(), "platform": plat, "stage": "url_check", "url": fullUrl, "rolled_back_to": previous.ID,
						}).Get(ctx)
					}
					return deployResult{Error: deployError{
						Summary:   "Deployment failed its health check — automatically rolled back to your previous working version.",
						IsWarning: true,
					}}, nil
				} else {
					slog.Error("Auto-rollback failed after health-check failure", "error", rbErr, "target", previous.ID)
				}
			} else {
				slog.Warn("No previous deployment to roll back to", "error", prevErr)
			}
		}

		summary, e1 := workflow.ExecuteActivity[deployError](ctx, ActivityOpts, AgentSummarizeError, err.Error(), input).Get(ctx)
		if operationId != "" {
			workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
				"error": err.Error(), "platform": plat, "stage": "url_check", "url": fullUrl,
			}).Get(ctx)
		}
		if e1 != nil {
			return deployResult{Error: deployError{Summary: err.Error()}}, nil
		}
		return deployResult{Error: summary}, nil
	}

	if operationId != "" {
		// Persist the service identifier + the adapter's per-cloud identifiers (region,
		// project, resourceGroup, …) so the console URL and logs command can be rebuilt
		// later. Cloud Run/App Runner encode project/region/account in resourceId; Azure
		// carries them as identifier keys.
		successMeta := map[string]any{"url": fullUrl, "platform": plat, "resourceId": svc.ID}
		for k, v := range svc.Metadata {
			if k == "url" {
				continue
			}
			successMeta[k] = v
		}
		workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "success", successMeta).Get(ctx)
	}

	return deployResult{Url: fullUrl}, nil
}
