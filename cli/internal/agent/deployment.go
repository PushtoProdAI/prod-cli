package agent

import (
	"context"
	"fmt"

	"github.com/cschleiden/go-workflows/workflow"
	"github.com/go-errors/errors"
	"github.com/meroxa/prod/cli/baml_client"
	"github.com/meroxa/prod/cli/baml_client/types"
	"github.com/meroxa/prod/cli/internal/analyzer"
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

func (a *Activities) estimateFlyioCosts(_ context.Context, spec deployment.DeploymentSpec, strategy deployment.DeploymentStrategy) (deployment.CostEstimate, error) {
	fa := flyio.NewFlyioDeploymentAdapter(a.flyClient, a.uiWriter)
	costs, err := fa.EstimateCost(&spec, strategy)
	if err != nil {
		return deployment.CostEstimate{}, errors.Errorf("failed to estimate costs: %w", err)
	}
	return costs, nil
}

func (a *Activities) categorizeEnvVarsForDeployment(ctx context.Context, spec analyzer.ProjectSpec) ([]deployment.EnvVar, error) {
	a.uiWriter.SendStatus("summarizing", "Categorizing environment variables...")
	categorizedEnvVars := make([]deployment.EnvVar, 0)
	dbList := make([]string, len(spec.ServiceRequirements))
	for i, service := range spec.ServiceRequirements {
		dbList[i] = service.Provider
	}
	// could consider pulling this up into the workflow, where this is the activitiy itself
	// doing a prompt call per environment variable gives us better control over the context and results from the LLM
	for _, envVar := range spec.EnvVars {
		ev := types.EnvVarCandidate{
			VarName: envVar.VarName,
			Line:    int64(envVar.Line),
			Context: envVar.Context,
			File:    envVar.File,
		}
		cat, err := baml_client.DetermineEnvVarRoles(ctx, ev, dbList)
		if err != nil {
			return categorizedEnvVars, errors.Errorf("failed to determine env var roles: %w", err)
		}
		categorizedEnvVars = append(categorizedEnvVars, deployment.EnvVar{Name: envVar.VarName, Role: cat.Role, Service: cat.DbType})
	}
	a.uiWriter.SendStatusComplete("summarizing", "✅ Categorized environment variables")
	return categorizedEnvVars, nil
}

func (a *Activities) getEnvVarsFromEnvFiles(_ context.Context, path string) ([]deployment.EnvVar, error) {
	a.uiWriter.SendStatus("analyzing", "Analyzing .env files for environment variables...")
	envVars := make([]deployment.EnvVar, 0)
	for _, file := range []string{".env", ".env.local", ".env.development", ".env.production", ".env.example"} {
		fileEnvVars, err := analyzer.ParseEnvFile(path, file)
		if err != nil {
			return envVars, errors.Errorf("failed to parse env file %s: %w", file, err)
		}
		for k, v := range fileEnvVars {
			envVars = append(envVars, deployment.EnvVar{Name: k, Value: v})
		}
	}
	a.uiWriter.SendStatusComplete("analyzing", "✅ Analyzed .env files")
	return envVars, nil
}

func (a *Activities) createDockerRepo(ctx context.Context, projectName string) error {
	session := CtxSession(ctx)
	err := a.beClient.CreateDockerRepository(ctx, session.AccessToken, projectName)
	if err != nil {
		return errors.Errorf("failed to create docker repository: %w", err)
	}
	return nil
}
