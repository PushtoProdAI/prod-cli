package agent

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/cschleiden/go-workflows/workflow"
	"github.com/go-errors/errors"
	"github.com/meroxa/prod/cli/baml_client"
	"github.com/meroxa/prod/cli/baml_client/types"
	"github.com/meroxa/prod/cli/internal/analyzer"
	"github.com/meroxa/prod/cli/internal/backend"
	"github.com/meroxa/prod/cli/internal/deployment"
	"github.com/meroxa/prod/cli/internal/deployment/flyio"
	"github.com/meroxa/prod/cli/internal/deployment/render"
	"github.com/meroxa/prod/cli/internal/output"
	"github.com/meroxa/prod/cli/internal/workflowext"
)

const (
	AgentDetermineIntent      = "agent.determineIntent"
	AgentAnalyzeProject       = "agent.analyzeProject"
	AgentSummarizeIntent      = "agent.summarize"
	AgentGetRenderWorkspace   = "agent.getRenderWorkspace"
	AgentDeployRenderSteps    = "agent.deployRenderSteps"
	AgentDeployFlyIOSteps     = "agent.deployFlyIOSteps"
	AgentSummarizeDeploySteps = "agent.summarizeDeploySteps"
	AgentSummarizeError       = "agent.summarizeError"
	AgentEstimateRenderCosts  = "agent.estimateRenderCosts"
	AgentGetRenderServiceURL  = "agent.getRenderServiceURL"
	AgentIsURLLive            = "agent.isURLLive"
	AgentSendProjectStats     = "agent.sendProjectStats"
	AgentGetFlyIOAppURL       = "agent.getFlyIOAppURL"
)

type Activities struct {
	renderClient render.RenderClient
	flyClient    flyio.FlyioClient
	beClient     *backend.Client
	uiWriter     output.StatusWriter
}

func (a *Activities) Activities() []workflowext.Activity {
	return []workflowext.Activity{
		{Name: AgentDetermineIntent, ActFunc: a.determineIntent},
		{Name: AgentAnalyzeProject, ActFunc: a.analyze},
		{Name: AgentSummarizeIntent, ActFunc: a.summarize},
		{Name: AgentGetRenderWorkspace, ActFunc: a.getRenderWorkspace},
		{Name: AgentSummarizeDeploySteps, ActFunc: a.summarizeDeploySteps},
		{Name: AgentDeployRenderSteps, ActFunc: a.deployRenderSteps},
		{Name: AgentDeployFlyIOSteps, ActFunc: a.deployFlyIOSteps},
		{Name: AgentSummarizeError, ActFunc: a.summarizeError},
		{Name: AgentEstimateRenderCosts, ActFunc: a.estimateRenderCosts},
		{Name: AgentGetRenderServiceURL, ActFunc: a.getRenderServiceURL},
		{Name: AgentIsURLLive, ActFunc: a.isURLLive},
		{Name: AgentSendProjectStats, ActFunc: a.sendProjectStats},
		{Name: AgentGetFlyIOAppURL, ActFunc: a.getFlyIOAppURL},
	}
}

func (a *Activities) determineIntent(ctx context.Context, prompt string) (types.Intent, error) {
	a.uiWriter.SendStatus("planning", "Understanding your request...")
	intent, err := baml_client.ExtractIntent(ctx, prompt)
	if err != nil {
		a.uiWriter.SendStatusComplete("planning", "❌ Failed to understand request")
		return types.Intent{}, errors.Errorf("failed to extract intent: %w", err)
	}
	if intent.Source == "pwd" {
		path, err := os.Getwd()
		if err != nil {
			intent.Source = ""
			log.Printf("failed to get current working directory: %v", err)
			a.uiWriter.SendStatusComplete("planning", "✅ Request understood")
			return intent, nil
		}
		intent.Source = path
	}
	a.uiWriter.SendStatusComplete("planning", "✅ Request understood")
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
	a.uiWriter.SendStatus("summarizing", "Summarizing your request...")
	summary, err := baml_client.SummarizeIntent(ctx, intent, name, language)
	if err != nil {
		a.uiWriter.SendStatusComplete("summarizing", "❌ Failed to summarize request")
		return "", errors.Errorf("failed to summarize intent: %w", err)
	}
	a.uiWriter.SendStatusComplete("summarizing", "✅ Request summarized")
	return summary.Summary, nil
}

func (a *Activities) getRenderWorkspace(ctx context.Context) (string, error) {
	a.uiWriter.SendStatus("retrieving", "Retrieving Render workspace details...")
	workspaces, err := a.renderClient.ListWorkspaces(ctx)
	if err != nil {
		a.uiWriter.SendStatusComplete("retrieving", "❌ Failed to retrieve workspace details")
		var httpErr *render.HTTPError
		if errors.As(err, &httpErr) {
			// Handle HTTP errors based on status code
			if httpErr.IsClientError() {
				return "", workflow.NewPermanentError(errors.Errorf("failed to list workspaces. client error (%d): %s", httpErr.StatusCode, httpErr.Message))
			}
			if httpErr.IsServerError() {
				return "", errors.Errorf("failed to list workspaces. server error (%d): %s", httpErr.StatusCode, httpErr.Message)
			}
		}
		return "", errors.Errorf("failed to list workspaces: %w", err)
	}

	if len(workspaces) == 0 {
		a.uiWriter.SendStatusComplete("retrieving", "❌ No workspaces found")
		return "", errors.Errorf("no workspaces found")
	}

	ownerID := workspaces[0].Owner.ID
	a.uiWriter.SendStatusComplete("retrieving", "✅ Workplace details retrieved")
	return ownerID, nil
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

func (a *Activities) deployRenderSteps(ctx context.Context, spec deployment.DeploymentSpec, workspaceID string) ([]deployment.CreatedResource, error) {
	dockerGen := deployment.NewDockerGenerator(a.uiWriter)
	d := render.NewQueuedDeployment(a.renderClient, &spec, dockerGen, true, a.uiWriter)
	steps := d.GenerateAPISteps(workspaceID)
	stepExecutor := render.NewStepExecutor(a.renderClient, a.uiWriter)
	createdResources, err := stepExecutor.ExecuteSteps(ctx, steps, a.uiWriter)
	if err != nil {
		var httpErr *render.HTTPError
		if errors.As(err, &httpErr) {
			if httpErr.IsClientError() {
				return []deployment.CreatedResource{}, workflow.NewPermanentError(errors.Errorf("failed to execute render steps. client error (%d): %s", httpErr.StatusCode, httpErr.Message))
			}
		}
		return []deployment.CreatedResource{}, errors.Errorf("failed to execute render steps: %w", err)
	}
	return createdResources, nil
}

func (a *Activities) deployFlyIOSteps(ctx context.Context, spec deployment.DeploymentSpec) ([]deployment.CreatedResource, error) {
	d := flyio.NewFlyioQueuedDeployment(a.flyClient, &spec, a.uiWriter)
	createdResources, err := d.Deploy(ctx)
	if err != nil {
		var httpErr *render.HTTPError
		if errors.As(err, &httpErr) {
			if httpErr.IsClientError() {
				return []deployment.CreatedResource{}, workflow.NewPermanentError(errors.Errorf("failed to execute render steps. client error (%d): %s", httpErr.StatusCode, httpErr.Message))
			}
		}
		return []deployment.CreatedResource{}, errors.Errorf("failed to execute render steps: %w", err)
	}
	return createdResources, nil
}

func (a *Activities) getRenderServiceURL(ctx context.Context, serviceID string) (string, error) {
	service, err := a.renderClient.GetWebService(ctx, serviceID)
	if err != nil {
		return "", errors.Errorf("failed to get service info: %w", err)
	}
	return service.ServiceDetails.URL, nil
}

func (a *Activities) getFlyIOAppURL(ctx context.Context, appID string) (string, error) {
	service, err := a.flyClient.GetApp(ctx, appID)
	if err != nil {
		return "", errors.Errorf("failed to get service info: %w", err)
	}
	return service.Hostname, nil
}

func (a *Activities) isURLLive(ctx context.Context, url string) error {
	// we could also use the deploys endpoint and check the status of the latest deploy,
	// but using the URL saves us on the rate limiting and ultimately is what the user cares about
	client := &http.Client{
		Timeout: 10 * time.Second,
	}
	a.uiWriter.SendStatus("deploying", "Waiting for URL to be live...")
	resp, err := client.Get(url)
	if err != nil {
		return errors.Errorf("failed to make GET request to %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode > 300 {
		return errors.Errorf("received non-success status code %d from %s", resp.StatusCode, url)
	}
	a.uiWriter.SendStatusComplete("deploying", "✅ URL is live")
	return nil
}

func (a *Activities) summarizeError(ctx context.Context, error string, input deployPlan) (deployError, error) {
	intent := types.Intent{
		Action:   input.Action.String(),
		Platform: input.Platform.String(),
		Source:   input.Source,
	}

	spec := types.ProjectSpec{
		BuildCommand: input.Spec.BuildCommand,
		Language:     input.Spec.Language,
		Name:         input.Spec.Name,
		StartCommand: input.Spec.StartCommand,
	}

	a.uiWriter.SendStatus("summarizing", "Creating next steps...")

	var summary types.Error
	var violations []string
	// handling this internally for now, but we could also bubble this up to the workflow
	for {
		s, err := baml_client.SummarizeDeployError(ctx, error, intent, spec, runtime.GOOS, violations)
		if err != nil {
			return deployError{}, errors.Errorf("failed to summarize error: %w", err)
		}

		violations = findErrorViolations(s, error, input.Platform.String())
		if len(violations) == 0 {
			summary = s
			break
		}

		log.Printf("Found %d violations in summary, re-prompting: %v", len(violations), violations)
	}

	deployError := deployError{
		Summary:      summary.Summary,
		Remediations: make([]remediation, len(summary.Remediations)),
	}

	for i, r := range summary.Remediations {
		deployError.Remediations[i] = remediation{
			Description: r.Description,
			CliCommand:  r.CliCommand,
		}
	}

	a.uiWriter.SendStatusComplete("summarizing", "✅ Errors summarized")
	log.Printf("Error summary: %s", deployError.Summary)
	log.Printf("Remediations: %v", deployError.Remediations)

	return deployError, nil
}

func (a *Activities) estimateRenderCosts(_ context.Context, spec deployment.DeploymentSpec, strategy deployment.DeploymentStrategy) (deployment.CostEstimate, error) {
	ra := render.NewRenderDeploymentAdapter(a.renderClient, a.uiWriter)
	costs, err := ra.EstimateCost(&spec, strategy)
	if err != nil {
		return deployment.CostEstimate{}, errors.Errorf("failed to estimate costs: %w", err)
	}
	return costs, nil
}

func (a *Activities) sendProjectStats(ctx context.Context, platform string, spec analyzer.ProjectSpec) error {
	err := a.beClient.RecordRequestedStack(ctx, platform, spec.Language, spec.ServiceRequirements)
	if err != nil {
		return errors.Errorf("failed to record project stats: %w", err)
	}
	return nil
}

func findErrorViolations(summary types.Error, errorMsg string, platform string) []string {
	var errs []string

	lowerOutput := strings.ToLower(summary.Summary)
	lowerError := strings.ToLower(errorMsg)
	lowerPlatform := strings.ToLower(platform)

	containsNotInError := func(text string) bool {
		return strings.Contains(lowerOutput, text) && !strings.Contains(lowerError, text)
	}

	// 1. Wrong platform mentions
	if lowerPlatform == FlyIO.String() {
		if containsNotInError("render") {
			errs = append(errs, "Mentioned Render in Fly.io context")
		}
		if containsNotInError("~/.render") {
			errs = append(errs, "Mentioned Render config path in Fly.io context")
		}
		if containsNotInError("$render_api_key") {
			errs = append(errs, "Mentioned Render env var in Fly.io context")
		}
		if (strings.Contains(lowerOutput, "docker") || strings.Contains(lowerOutput, "ecr")) &&
			!strings.Contains(lowerError, "docker") {
			errs = append(errs, "Mentioned Docker/ECR in Fly.io context without Docker in error message")
		}
	}

	if lowerPlatform == Render.String() {
		if containsNotInError("fly.io") {
			errs = append(errs, "Mentioned Fly.io in Render context")
		}
		if containsNotInError("~/.fly") {
			errs = append(errs, "Mentioned Fly.io config path in Render context")
		}
	}

	// 2. Forbidden commands
	forbiddenCmds := []string{"docker login", "docker push", "prod login"}
	for _, cmd := range forbiddenCmds {
		if strings.Contains(lowerOutput, cmd) {
			errs = append(errs, fmt.Sprintf("Suggested forbidden command: %s", cmd))
		}
	}

	return errs
}
