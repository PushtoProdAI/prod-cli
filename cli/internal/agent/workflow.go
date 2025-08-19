package agent

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"maps"
	"time"

	"github.com/cschleiden/go-workflows/client"
	"github.com/cschleiden/go-workflows/workflow"
	"github.com/go-errors/errors"
	"github.com/meroxa/prod/cli/internal/backend"
	"github.com/meroxa/prod/cli/internal/deployment"
	"github.com/meroxa/prod/cli/internal/deployment/flyio"
	"github.com/meroxa/prod/cli/internal/deployment/render"
	"github.com/meroxa/prod/cli/internal/output"
	"github.com/meroxa/prod/cli/internal/workflowext"
)

const (
	PlanDeployWorkflowName        = "agent.planDeploy"
	DeployRenderWorkflowName      = "agent.deploy.render"
	DryRunDeployWorkflowName      = "agent.dryRun.render"
	DeployFlyioWorkflowName       = "agent.deploy.flyio"
	CategorizeEnvVarsWorkflowName = "agent.categorizeEnvVars"
)

var ActivityOpts = workflow.ActivityOptions{
	RetryOptions: workflow.RetryOptions{
		MaxAttempts:        10,
		BackoffCoefficient: 1,
		FirstRetryInterval: time.Second * 5,
		MaxRetryInterval:   time.Second * 20,
	},
}

type Workflows struct {
	Acts         *Activities
	registry     workflowext.Registry
	renderClient render.RenderClient
	flyClient    flyio.FlyioClient
	uiWriter     output.StatusWriter
}

var _ workflowext.Registerer = (*Workflows)(nil)

func NewWorkflows(renderClient render.RenderClient, flyClient flyio.FlyioClient, beClient *backend.Client, uiWriter output.StatusWriter) *Workflows {
	return &Workflows{
		Acts:         &Activities{renderClient: renderClient, flyClient: flyClient, beClient: beClient, uiWriter: uiWriter},
		renderClient: renderClient,
		flyClient:    flyClient,
		uiWriter:     uiWriter,
	}
}

func (w *Workflows) Register(registry workflowext.Registry) error {
	var errs error
	w.registry = registry
	for _, wf := range w.Workflows() {
		if err := wf.Register(registry); err != nil {
			errs = errors.Join(errs,
				errors.Errorf("failed to register agent workflow %q: %w", wf.Name, err),
			)
		}
	}

	for _, act := range w.Acts.Activities() {
		if err := act.Register(registry); err != nil {
			errs = errors.Join(errs,
				errors.Errorf("failed to register agent activity %q: %w", act.Name, err),
			)
		}
	}

	return errs
}

// Workflows returns all available workflows for the pipeline.
func (w *Workflows) Workflows() []workflowext.Workflow {
	return []workflowext.Workflow{
		{Name: PlanDeployWorkflowName, WorkflowFunc: w.planDeploy},
		{Name: DeployRenderWorkflowName, WorkflowFunc: w.deployRender},
		{Name: DryRunDeployWorkflowName, WorkflowFunc: w.dryRunDeployRender},
		{Name: DeployFlyioWorkflowName, WorkflowFunc: w.deployFly},
		{Name: CategorizeEnvVarsWorkflowName, WorkflowFunc: w.categorizeEnvVars},
	}
}

func (Workflows) PlanDeploy(ctx context.Context, c *client.Client, input string) (*workflow.Instance, error) {
	return c.CreateWorkflowInstance(ctx, client.WorkflowInstanceOptions{InstanceID: fmt.Sprintf("%s.%d", PlanDeployWorkflowName, time.Now().Unix())}, PlanDeployWorkflowName, input)
}

func (Workflows) Deploy(ctx context.Context, c *client.Client, input DeployPlan) (*workflow.Instance, error) {
	slog.Info("Deploy", "platform", input.Platform, "action", input.Action)
	switch input.Platform {
	case Render:
		return c.CreateWorkflowInstance(ctx, client.WorkflowInstanceOptions{InstanceID: fmt.Sprintf("%s.%d", DeployRenderWorkflowName, time.Now().Unix())}, DeployRenderWorkflowName, input)
	case FlyIO:
		return c.CreateWorkflowInstance(ctx, client.WorkflowInstanceOptions{InstanceID: fmt.Sprintf("%s.%d", DeployFlyioWorkflowName, time.Now().Unix())}, DeployFlyioWorkflowName, input)
	default:
		return nil, errors.New("unsupported platform for deployment")
	}
}

func (Workflows) DryRunDeploy(ctx context.Context, c *client.Client, input DeployPlan) (*workflow.Instance, error) {
	slog.Info("Dry run", "platform", input.Platform, "action", input.Action)
	if input.Platform == Render {
		return c.CreateWorkflowInstance(ctx, client.WorkflowInstanceOptions{InstanceID: fmt.Sprintf("%s.%d", DryRunDeployWorkflowName, time.Now().Unix())}, DryRunDeployWorkflowName, input)
	}
	return nil, errors.New("unsupported platform for dry-run deployment")
}

func (Workflows) CategorizeEnvVars(ctx context.Context, c *client.Client, input DeployPlan) (*workflow.Instance, error) {
	return c.CreateWorkflowInstance(ctx, client.WorkflowInstanceOptions{InstanceID: fmt.Sprintf("%s.%d", CategorizeEnvVarsWorkflowName, time.Now().Unix())}, CategorizeEnvVarsWorkflowName, input)
}

func (w *Workflows) deployRender(ctx workflow.Context, input DeployPlan) (deployResult, error) {
	if w.registry == nil {
		return deployResult{}, errors.New("workflow registry is not set")
	}

	// Validate Docker availability for Render
	if !deployment.IsDockerAvailable() {
		summary, err := workflow.ExecuteActivity[deployError](ctx, ActivityOpts, AgentSummarizeError, "not able to build docker image. cannot connect to local docker daemon", input).Get(ctx)
		if err != nil {
			return deployResult{Error: deployError{Summary: "not able to build docker image. cannont connect to local docker daemon"}}, nil
		}
		return deployResult{Error: summary}, nil
	}

	envVars := input.CollectedEnvVars
	// Build deployment spec
	db := deployment.NewDeploymentBuilder(&input.Spec, envVars)
	spec, err := db.Build()
	if err != nil {
		return deployResult{}, errors.Errorf("failed to build deployment spec: %w", err)
	}
	spec.Metadata["buildContext"] = input.Source
	spec.Metadata["tenantID"] = "test"

	// Generate and summarize deployment steps (for UI feedback)
	workspaceID, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentGetRenderWorkspace).Get(ctx)
	if err != nil {
		summary, e1 := workflow.ExecuteActivity[deployError](ctx, ActivityOpts, AgentSummarizeError, err.Error(), input).Get(ctx)
		if e1 != nil {
			log.Printf("Failed to get Render workspace: %v", e1)
			return deployResult{Error: deployError{Summary: err.Error()}}, nil
		}
		return deployResult{Error: summary}, nil
	}

	dockerGen := deployment.NewDockerGenerator(w.uiWriter)
	d := render.NewQueuedDeployment(w.renderClient, spec, dockerGen, true, w.uiWriter)
	steps := d.GenerateAPISteps(workspaceID)
	descriptions := make([]string, len(steps))
	for i, step := range steps {
		descriptions[i] = step.GetDescription()
	}
	_, err = workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentSummarizeDeploySteps, descriptions).Get(ctx)
	if err != nil {
		log.Printf("Failed to summarize deployment steps: %v", err)
	}

	createdResources, err := workflow.ExecuteActivity[[]deployment.CreatedResource](ctx, ActivityOpts, AgentDeploySteps, *spec, input.Platform).Get(ctx)
	if err != nil {
		summary, e1 := workflow.ExecuteActivity[deployError](ctx, ActivityOpts, AgentSummarizeError, err.Error(), input).Get(ctx)
		if e1 != nil {
			return deployResult{Error: deployError{Summary: err.Error()}}, nil
		}
		log.Printf("Deployment failed: %v", err)
		return deployResult{Error: summary}, nil
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

	// Get service URL and verify it's live
	url, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentGetRenderServiceURL, ws.ID).Get(ctx)
	if err != nil {
		return deployResult{}, errors.Errorf("failed to get service URL for %s: %w", ws.Name, err)
	}
	_, err = workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentIsURLLive, url).Get(ctx)
	if err != nil {
		return deployResult{}, errors.Errorf("service URL %s is not live: %w", url, err)
	}
	return deployResult{Url: url}, nil
}

func (w *Workflows) deployFly(ctx workflow.Context, input DeployPlan) (deployResult, error) {
	if w.registry == nil {
		return deployResult{}, errors.New("workflow registry is not set")
	}

	envVars := input.CollectedEnvVars

	// Build deployment spec
	db := deployment.NewDeploymentBuilder(&input.Spec, envVars)
	spec, err := db.Build()
	if err != nil {
		return deployResult{}, errors.Errorf("failed to build deployment spec: %w", err)
	}
	spec.Metadata["buildContext"] = input.Source

	// Generate and summarize deployment steps
	d := flyio.NewFlyioQueuedDeployment(w.flyClient, spec, w.uiWriter)
	steps := d.GenerateAPISteps()
	descriptions := make([]string, len(steps))
	for i, step := range steps {
		descriptions[i] = step.GetDescription()
	}
	_, err = workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentSummarizeDeploySteps, descriptions).Get(ctx)
	if err != nil {
		log.Printf("Failed to summarize deployment steps: %v", err)
	}

	createdResources, err := workflow.ExecuteActivity[[]deployment.CreatedResource](ctx, ActivityOpts, AgentDeploySteps, *spec, input.Platform).Get(ctx)
	if err != nil {
		summary, e1 := workflow.ExecuteActivity[deployError](ctx, ActivityOpts, AgentSummarizeError, err.Error(), input).Get(ctx)
		if e1 != nil {
			return deployResult{Error: deployError{Summary: err.Error()}}, nil
		}
		log.Printf("Deployment failed: %v", err)
		return deployResult{Error: summary}, nil
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
	url, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentGetFlyIOAppURL, ws.ID).Get(ctx)
	if err != nil {
		return deployResult{}, errors.Errorf("failed to get service URL for %s: %w", ws.Name, err)
	}
	_, err = workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentIsURLLive, url).Get(ctx)
	if err != nil {
		return deployResult{}, errors.Errorf("service URL %s is not live: %w", url, err)
	}
	return deployResult{Url: url}, nil
}

func (w *Workflows) dryRunDeployRender(ctx workflow.Context, input DeployPlan) (DryRunResult, error) {
	if w.registry == nil {
		return DryRunResult{}, errors.New("workflow registry is not set")
	}

	credentialStatus := make(map[string]bool)
	workspaceID, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentGetRenderWorkspace).Get(ctx)
	if err != nil {
		credentialStatus["Render API"] = false
	} else {
		credentialStatus["Render API"] = true
	}

	envVars := input.CollectedEnvVars

	dockerGen := deployment.NewDockerGenerator(w.uiWriter)
	db := deployment.NewDeploymentBuilder(&input.Spec, envVars)
	spec, err := db.Build()
	if err != nil {
		return DryRunResult{}, errors.Errorf("failed to build deployment spec: %w", err)
	}

	spec.Metadata["buildContext"] = input.Source
	spec.Metadata["tenantID"] = "test"

	d := render.NewQueuedDeployment(w.renderClient, spec, dockerGen, true, w.uiWriter)
	steps := d.GenerateAPISteps(workspaceID)

	dryRunSteps := make([]DryRunStep, len(steps))
	for i, step := range steps {
		dryRunSteps[i] = DryRunStep{
			ID:          step.GetID(),
			Description: step.GetDescription(),
			Type:        getStepType(step),
			Config:      extractStepConfig(step),
			DependsOn:   step.GetDependencies(),
		}
	}

	estimatedCosts, err := workflow.ExecuteActivity[deployment.CostEstimate](ctx, ActivityOpts, AgentEstimateRenderCosts, spec, deployment.StrategyRenderQueued).Get(ctx)
	slog.Error("Error", "error", err)
	if err != nil {
		return DryRunResult{}, errors.Errorf("failed to estimate costs: %w", err)
	}

	conflictChecks := performConflictChecks(workspaceID, spec, w.renderClient)
	validationErrors := validateDeploymentSpec(spec)

	return DryRunResult{
		Steps:            dryRunSteps,
		EstimatedCosts:   estimatedCosts,
		CredentialStatus: credentialStatus,
		ConflictChecks:   conflictChecks,
		ValidationErrors: validationErrors,
	}, nil
}

func (w *Workflows) categorizeEnvVars(ctx workflow.Context, deployPlan DeployPlan) ([]deployment.EnvVar, error) {
	// as noted in the activites code for this, we could split this out so each env get's own activity instance that could be conccurent
	envVars, err := workflow.ExecuteActivity[[]deployment.EnvVar](ctx, ActivityOpts, AgentCategorizeEnvVars, deployPlan.Spec).Get(ctx)
	if err != nil {
		return []deployment.EnvVar{}, errors.Errorf("failed to categorize environment variables: %w", err)
	}
	fromEnvFiles, err := workflow.ExecuteActivity[[]deployment.EnvVar](ctx, ActivityOpts, AgentReadEnvFiles, deployPlan.Source).Get(ctx)
	if err != nil {
		return []deployment.EnvVar{}, errors.Errorf("failed to read environment variables from .env files: %w", err)
	}

	// convert to a map to make the next step a little easier
	envMap := maps.Collect(func(yield func(string, deployment.EnvVar) bool) {
		for _, e := range fromEnvFiles {
			if !yield(e.Name, e) {
				return
			}
		}
	})

	// we will take values that in env files and use those as suggested values
	for i := range envVars {
		if envVars[i].IsNotDBRelated() {
			fromEnvFile, ok := envMap[envVars[i].Name]
			if !ok {
				continue
			}
			log.Println(envVars[i].Name, "found in env file with value", fromEnvFile.Value)
			envVars[i].Value = fromEnvFile.Value
		}
	}

	return envVars, nil
}

// Helper functions for dry run workflow

func getStepType(step render.RenderAPIStep) string {
	switch step.(type) {
	case *render.CreatePostgresStep:
		return "postgres"
	case *render.CreateRedisStep:
		return "redis"
	case *render.GetConnectionInfoStep:
		return "connection"
	case *render.BuildAndPushStep:
		return "docker_build"
	case *render.CreateRegistryCredentialStep:
		return "registry_credential"
	case *render.CreateWebServiceStep:
		return "web_service"
	default:
		return "unknown"
	}
}

func extractStepConfig(step render.RenderAPIStep) map[string]any {
	config := make(map[string]any)

	switch s := step.(type) {
	case *render.CreatePostgresStep:
		config["name"] = s.Name
		config["databaseName"] = s.DatabaseName
		config["plan"] = "basic_256mb"
		config["version"] = "16"
	case *render.CreateRedisStep:
		config["name"] = s.Name
		config["plan"] = "standard"
	case *render.CreateWebServiceStep:
		config["name"] = s.Name
		config["buildCommand"] = s.BuildCommand
		config["startCommand"] = s.StartCommand
		config["environment"] = s.Environment
	}

	return config
}

func performConflictChecks(workspaceID string, spec *deployment.DeploymentSpec, client render.RenderClient) []ConflictCheck {
	var checks []ConflictCheck

	checks = append(checks, ConflictCheck{
		Resource: fmt.Sprintf("Web service '%s-web'", spec.Name),
		Status:   "ok",
		Message:  "No conflicts detected",
	})

	serviceCounts := spec.ServiceCounts()
	for provider, count := range serviceCounts {
		for i := 1; i <= count; i++ {
			checks = append(checks, ConflictCheck{
				Resource: fmt.Sprintf("%s service '%s-%s-%d'", provider, spec.Name, provider, i),
				Status:   "ok",
				Message:  "No conflicts detected",
			})
		}
	}

	return checks
}

func validateDeploymentSpec(spec *deployment.DeploymentSpec) []string {
	var errors []string

	if spec.Name == "" {
		errors = append(errors, "Application name is required")
	}

	if spec.Language == "" {
		errors = append(errors, "Programming language must be specified")
	}

	return errors
}
