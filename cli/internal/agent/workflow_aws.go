package agent

import (
	"log/slog"
	"net/url"

	"github.com/cschleiden/go-workflows/workflow"
	"github.com/go-errors/errors"
	"github.com/pushtoprodai/prod-cli/internal/deployment"
	prod_error "github.com/pushtoprodai/prod-cli/internal/error"
)

// deployAWS deploys to AWS App Runner with the user's own credentials. The
// image build+push to ECR, the App Runner create/redeploy, and the wait for
// RUNNING all happen inside the AgentDeploySteps activity (see the aws
// App Runner deployable); this workflow builds the spec, runs that activity, and
// verifies the resulting URL is live.
func (w *Workflows) deployAWS(ctx workflow.Context, input DeployPlan) (deployResult, error) {
	operationId, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentLogDeploymentStart, "aws", input.Spec, input.Source, input.Action).Get(ctx)
	if err != nil {
		slog.Error("Failed to log deployment start", "error", err)
		// Continue with deployment even if logging fails.
	}

	// App Runner deploys from a locally built container image.
	if !deployment.IsDockerAvailable() {
		if operationId != "" {
			workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
				"error": "Docker not available", "platform": "aws", "stage": "docker_validation",
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
				"error": err.Error(), "platform": "aws", "stage": "spec_build",
			}).Get(ctx)
		}
		return deployResult{}, errors.Errorf("failed to build deployment spec: %w", err)
	}
	spec.Metadata["buildContext"] = input.Source
	if input.ExistingProjectInfo.Exists {
		spec.IsUpdate = true
	}

	// Build+push to ECR, create/redeploy the App Runner service, wait for RUNNING.
	createdResources, err := workflow.ExecuteActivity[[]deployment.CreatedResource](ctx, ActivityOpts, AgentDeploySteps, *spec, input.Platform).Get(ctx)
	if err != nil {
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow": DeployAWSWorkflowName, "activity": AgentDeploySteps,
			"component": "deployment", "platform": "aws",
			"project_name": input.Spec.Name, "language": input.Spec.Language,
		})
		summary, e1 := workflow.ExecuteActivity[deployError](ctx, ActivityOpts, AgentSummarizeError, err.Error(), input).Get(ctx)
		if operationId != "" {
			workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
				"error": err.Error(), "platform": "aws", "stage": "deploy_steps",
			}).Get(ctx)
		}
		if e1 != nil {
			return deployResult{Error: deployError{Summary: err.Error()}}, nil
		}
		return deployResult{Error: summary}, nil
	}

	// The App Runner deployable returns the service and its URL.
	var svc deployment.CreatedResource
	for _, cr := range createdResources {
		if cr.Type == "apprunner_service" {
			svc = cr
			break
		}
	}
	if svc.ID == "" {
		return deployResult{}, errors.Errorf("App Runner deployment returned no service")
	}
	u, _ := svc.Metadata["url"].(string)
	if u == "" {
		return deployResult{}, errors.Errorf("App Runner deployment returned no URL")
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
			"workflow": DeployAWSWorkflowName, "activity": AgentVerifyLiveness,
			"component": "deployment", "platform": "aws",
			"project_name": input.Spec.Name, "language": input.Spec.Language,
		})
		summary, e1 := workflow.ExecuteActivity[deployError](ctx, ActivityOpts, AgentSummarizeError, err.Error(), input).Get(ctx)
		if operationId != "" {
			workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
				"error": err.Error(), "platform": "aws", "stage": "url_check", "url": fullUrl,
			}).Get(ctx)
		}
		if e1 != nil {
			return deployResult{Error: deployError{Summary: err.Error()}}, nil
		}
		return deployResult{Error: summary}, nil
	}

	if operationId != "" {
		workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "success", map[string]any{
			"url": fullUrl, "platform": "aws",
		}).Get(ctx)
	}

	return deployResult{Url: fullUrl}, nil
}
