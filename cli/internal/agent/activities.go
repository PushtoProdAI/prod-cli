package agent

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/go-errors/errors"
	"github.com/meroxa/prod/cli/baml_client"
	"github.com/meroxa/prod/cli/baml_client/types"
	"github.com/meroxa/prod/cli/internal/analyzer"
	"github.com/meroxa/prod/cli/internal/deployment"
	"github.com/meroxa/prod/cli/internal/deployment/render"
	"github.com/meroxa/prod/cli/internal/workflowext"
)

const (
	AgentDetermineIntent      = "agent.determineIntent"
	AgentAnalyzeProject       = "agent.analyzeProject"
	AgentSummarizeIntent      = "agent.summarize"
	AgentGetRenderWorkspace   = "agent.getRenderWorkspace"
	AgentDeployRenderSteps    = "agent.deployRenderSteps"
	AgentSummarizeDeploySteps = "agent.summarizeDeploySteps"
	AgentSummarizeError       = "agent.summarizeError"
	AgentGetServiceURL        = "agent.getServiceURL"
	AgentIsURLLive            = "agent.isURLLive"
)

type Activities struct {
	renderClient render.RenderClient
	statusSender SendWorkflowStatus
}

func (a *Activities) Activities() []workflowext.Activity {
	return []workflowext.Activity{
		{Name: AgentDetermineIntent, ActFunc: a.determineIntent},
		{Name: AgentAnalyzeProject, ActFunc: a.analyze},
		{Name: AgentSummarizeIntent, ActFunc: a.summarize},
		{Name: AgentGetRenderWorkspace, ActFunc: a.getRenderWorkspace},
		{Name: AgentSummarizeDeploySteps, ActFunc: a.summarizeDeploySteps},
		{Name: AgentDeployRenderSteps, ActFunc: a.deployRenderSteps},
		{Name: AgentSummarizeError, ActFunc: a.summarizeError},
		{Name: AgentGetServiceURL, ActFunc: a.getServiceURL},
		{Name: AgentIsURLLive, ActFunc: a.isURLLive},
	}
}

func (a *Activities) determineIntent(ctx context.Context, prompt string) (types.Intent, error) {
	a.statusSender("planning", "Understanding your request...")
	intent, err := baml_client.ExtractIntent(ctx, prompt)
	if err != nil {
		return types.Intent{}, errors.Errorf("failed to extract intent: %w", err)
	}
	if intent.Source == "pwd" {
		path, err := os.Getwd()
		if err != nil {
			intent.Source = ""
			log.Printf("failed to get current working directory: %v", err)
			return intent, nil
		}
		intent.Source = path
	}
	return intent, nil
}

func (a *Activities) analyze(_ context.Context, intent types.Intent) (analyzer.ProjectSpec, error) {
	an, err := analyzer.GetAnalyzer(intent.Source)
	if err != nil {
		return analyzer.ProjectSpec{}, errors.Errorf("failed to get analyzer: %w", err)
	}
	spec, err := an.Analyze()
	return *spec, err
}

func (a *Activities) summarize(ctx context.Context, intent types.Intent, name string, language string) (string, error) {
	a.statusSender("summarizing", "Summarizing your request...")
	summary, err := baml_client.SummarizeIntent(ctx, intent, name, language)
	if err != nil {
		return "", errors.Errorf("failed to summarize intent: %w", err)
	}
	return summary.Summary, nil
}

func (a *Activities) getRenderWorkspace(ctx context.Context) (string, error) {
	workspaces, err := a.renderClient.ListWorkspaces(ctx)
	if err != nil {
		return "", errors.Errorf("failed to list workspaces: %w", err)
	}

	if len(workspaces) == 0 {
		return "", errors.Errorf("no workspaces found")
	}

	ownerID := workspaces[0].Owner.ID
	return ownerID, nil
}

func (a *Activities) summarizeDeploySteps(ctx context.Context, steps []string) error {
	summary, err := baml_client.SummarizeSteps(ctx, steps)
	if err != nil {
		return errors.Errorf("failed to summarize deploy steps: %w", err)
	}
	a.statusSender("deploying", fmt.Sprintf("%s\n-----", summary.Summary))
	return nil
}

func (a *Activities) deployRenderSteps(ctx context.Context, spec deployment.DeploymentSpec, workspaceID string) ([]deployment.CreatedResource, error) {
	dockerGen := deployment.NewDockerGenerator()
	d := render.NewQueuedDeployment(a.renderClient, &spec, dockerGen, true)
	steps := d.GenerateAPISteps(workspaceID)
	stepExecutor := render.NewStepExecutor(a.renderClient)
	createdResources, err := stepExecutor.ExecuteSteps(ctx, steps)
	if err != nil {
		return []deployment.CreatedResource{}, errors.Errorf("failed to execute render steps: %w", err)
	}
	return createdResources, nil
}

func (a *Activities) getServiceURL(ctx context.Context, serviceID string) (string, error) {
	service, err := a.renderClient.GetWebService(ctx, serviceID)
	if err != nil {
		return "", errors.Errorf("failed to get service info: %w", err)
	}
	return service.ServiceDetails.URL, nil
}

func (a *Activities) isURLLive(ctx context.Context, url string) error {
	// we could also use the deploys endpoint and check the status of the latest deploy,
	// but using the URL saves us on the rate limiting and ultimately is what the user cares about
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	resp, err := client.Get(url)
	if err != nil {
		return errors.Errorf("failed to make GET request to %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode > 300 {
		return errors.Errorf("received non-success status code %d from %s", resp.StatusCode, url)
	}

	return nil
}

func (a *Activities) summarizeError(ctx context.Context, error string) (string, error) {
	summary, err := baml_client.SummarizeDeployError(ctx, error)
	if err != nil {
		return "", errors.Errorf("failed to summarize error: %w", err)
	}
	return summary.Summary, nil
}
