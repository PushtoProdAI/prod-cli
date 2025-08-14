package agent

import (
	"context"
	"fmt"

	"github.com/cschleiden/go-workflows/workflow"
	"github.com/go-errors/errors"
	"github.com/meroxa/prod/cli/baml_client"
	"github.com/meroxa/prod/cli/internal/deployment"
	"github.com/meroxa/prod/cli/internal/deployment/flyio"
	"github.com/meroxa/prod/cli/internal/deployment/render"
)

func (a *Activities) deploySteps(ctx context.Context, spec deployment.DeploymentSpec, platform Platform) ([]deployment.CreatedResource, error) {
	// Create platform-specific Deployable implementation
	var deployable deployment.Deployable
	switch platform {
	case Render:
		dockerGen := deployment.NewDockerGenerator(a.uiWriter)
		deployable = render.NewQueuedDeployment(a.renderClient, &spec, dockerGen, true, a.uiWriter)
	case FlyIO:
		deployable = flyio.NewFlyioQueuedDeployment(a.flyClient, &spec, a.uiWriter)
	default:
		return nil, fmt.Errorf("unsupported platform: %s", platform)
	}

	createdResources, err := deployable.Deploy(ctx)
	if err != nil {
		var httpErr *render.HTTPError
		if errors.As(err, &httpErr) {
			if httpErr.IsClientError() {
				return []deployment.CreatedResource{}, workflow.NewPermanentError(errors.Errorf("failed to execute %s deployment. client error (%d): %s", platform, httpErr.StatusCode, httpErr.Message))
			}
		}
		return []deployment.CreatedResource{}, errors.Errorf("failed to execute %s deployment: %w", platform, err)
	}

	return createdResources, nil
}

func (a *Activities) summarizeDeploySteps(ctx context.Context, steps []string) error {
	a.uiWriter.SendStatus("summarizing", "Summarizing deployment steps")

	summary, err := baml_client.SummarizeSteps(ctx, steps)
	if err != nil {
		return errors.Errorf("failed to summarize deploy steps: %w", err)
	}
	a.uiWriter.SendStatusComplete("summarizing", "✅ Steps summarized")
	a.uiWriter.SendStatus("summary", fmt.Sprintf("%s\n-----", summary.Summary))
	return nil
}

func (a *Activities) estimateRenderCosts(_ context.Context, spec deployment.DeploymentSpec, strategy deployment.DeploymentStrategy) (deployment.CostEstimate, error) {
	ra := render.NewRenderDeploymentAdapter(a.renderClient, a.uiWriter)
	costs, err := ra.EstimateCost(&spec, strategy)
	if err != nil {
		return deployment.CostEstimate{}, errors.Errorf("failed to estimate costs: %w", err)
	}
	return costs, nil
}
