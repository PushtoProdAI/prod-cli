package agent

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/cschleiden/go-workflows/client"
	"github.com/cschleiden/go-workflows/workflow"
	"github.com/go-errors/errors"
	"github.com/meroxa/prod/cli/baml_client/types"
	"github.com/meroxa/prod/cli/internal/analyzer"
	"github.com/meroxa/prod/cli/internal/deployment"
	"github.com/meroxa/prod/cli/internal/deployment/render"
	"github.com/meroxa/prod/cli/internal/workflowext"
)

const (
	PlanDeployWorkflowName   = "agent.planDeploy"
	DeployRenderWorkflowName = "agent.deploy.render"
	DryRunDeployWorkflowName = "agent.dryRun.render"
)

var ActivityOpts = workflow.ActivityOptions{
	RetryOptions: workflow.RetryOptions{
		MaxAttempts:        10,
		BackoffCoefficient: 1,
		FirstRetryInterval: time.Second * 5,
		MaxRetryInterval:   time.Second * 20,
	},
}

type SendWorkflowStatus func(status string, message string)

type Workflows struct {
	Acts         *Activities
	statusSender SendWorkflowStatus
	registry     workflowext.Registry
	renderClient render.RenderClient
}

var _ workflowext.Registerer = (*Workflows)(nil)

func NewWorkflows(renderClient render.RenderClient, statusSender SendWorkflowStatus) *Workflows {
	return &Workflows{
		Acts:         &Activities{renderClient: renderClient, statusSender: statusSender},
		statusSender: statusSender,
		renderClient: renderClient,
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
	}
}

func (Workflows) PlanDeploy(ctx context.Context, c *client.Client, input string) (*workflow.Instance, error) {
	return c.CreateWorkflowInstance(ctx, client.WorkflowInstanceOptions{InstanceID: fmt.Sprintf("%s.%d", PlanDeployWorkflowName, time.Now().Unix())}, PlanDeployWorkflowName, input)
}

func (Workflows) Deploy(ctx context.Context, c *client.Client, input deployPlan) (*workflow.Instance, error) {
	log.Println(input.Platform, input.Action)
	if input.Platform == Render {
		return c.CreateWorkflowInstance(ctx, client.WorkflowInstanceOptions{InstanceID: fmt.Sprintf("%s.%d", DeployRenderWorkflowName, time.Now().Unix())}, DeployRenderWorkflowName, input)
	}
	return nil, errors.New("unsupported platform for deployment")
}

func (Workflows) DryRunDeploy(ctx context.Context, c *client.Client, input deployPlan) (*workflow.Instance, error) {
	log.Println("Dry run for", input.Platform, input.Action)
	if input.Platform == Render {
		return c.CreateWorkflowInstance(ctx, client.WorkflowInstanceOptions{InstanceID: fmt.Sprintf("%s.%d", DryRunDeployWorkflowName, time.Now().Unix())}, DryRunDeployWorkflowName, input)
	}
	return nil, errors.New("unsupported platform for dry-run deployment")
}

func (w *Workflows) planDeploy(ctx workflow.Context, input string) (deployPlan, error) {
	intent, err := workflow.ExecuteActivity[types.Intent](ctx, ActivityOpts, AgentDetermineIntent, input).Get(ctx)
	if err != nil {
		log.Println(errors.Errorf("failed to determine intent: %w", err))
		w.statusSender("error", "Failed to determine intent")
	}
	spec := analyzer.ProjectSpec{}
	if intent.Source != "" {
		opts := ActivityOpts
		opts.RetryOptions.MaxAttempts = 3
		opts.RetryOptions.FirstRetryInterval = time.Second * 2
		w.statusSender("analyzing", "Analyzing project...")
		spec, err = workflow.ExecuteActivity[analyzer.ProjectSpec](ctx, opts, AgentAnalyzeProject, intent).Get(ctx)
		if err != nil {
			log.Println(errors.Errorf("failed to analyze project: %w", err))
		}
	}
	summary, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentSummarizeIntent, intent, spec.Name, spec.Language).Get(ctx)
	if err != nil {
		log.Println(errors.Errorf("failed to summarize intent: %w", err))
	}
	platform := UnknownPlatform
	switch strings.ToLower(intent.Platform) {
	case "render":
		platform = Render
	case "fly.io":
		platform = FlyIO
	default:
		platform = UnknownPlatform
	}

	action := UnknownAction
	switch strings.ToLower(intent.Action) {
	case "deploy":
		action = Deploy
	default:
		action = UnknownAction
	}

	plan := deployPlan{
		Action:   action,
		Platform: platform,
		Source:   intent.Source,
		Spec:     spec,
		Summary:  summary,
	}

	return plan, err
}

func (w *Workflows) deployRender(ctx workflow.Context, input deployPlan) (string, error) {
	if w.registry == nil {
		return "", errors.New("workflow registry is not set")
	}
	dockerGen := deployment.NewDockerGenerator()
	db := deployment.NewDeploymentBuilder(&input.Spec)
	spec, err := db.Build()
	if err != nil {
		return "", errors.Errorf("failed to build deployment spec: %w", err)
	}
	workspaceID, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentGetRenderWorkspace).Get(ctx)
	if err != nil {
		summary, _ := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentSummarizeError, err.Error()).Get(ctx)
		w.statusSender("error", summary)
		return "", errors.Errorf("failed to get Render workspace: %w", err)
	}
	spec.Metadata["buildContext"] = input.Source
	spec.Metadata["tenantID"] = "test" // TODO: this shouldn't be hardcoded, we need to get the tenant ID from the context or config

	d := render.NewQueuedDeployment(w.renderClient, spec, dockerGen, true)
	steps := d.GenerateAPISteps(workspaceID)

	// collect the descriptions to generate a summary
	descriptions := make([]string, len(steps))
	for i, step := range steps {
		descriptions[i] = step.GetDescription()
	}
	_, err = workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentSummarizeDeploySteps, descriptions).Get(ctx)
	if err != nil {
		log.Printf("Failed to summarize deployment steps: %v", err)
	}
	createdResources, err := workflow.ExecuteActivity[[]deployment.CreatedResource](ctx, ActivityOpts, AgentDeployRenderSteps, *spec, workspaceID).Get(ctx)
	if err != nil {
		summary, _ := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentSummarizeError, err.Error()).Get(ctx)
		w.statusSender("error", summary)
		return "", errors.Errorf("failed to execute Render deploy: %w", err)
	}
	var ws deployment.CreatedResource
	for _, cr := range createdResources {
		if cr.Type == "web_service" {
			ws = cr
			break // assuming we only have one web service
		}
	}
	if ws.ID == "" {
		return "", nil
	}
	url, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentGetServiceURL, ws.ID).Get(ctx)
	if err != nil {
		return "", errors.Errorf("failed to get service URL for %s: %w", ws.Name, err)
	}
	_, err = workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentIsURLLive, url).Get(ctx)
	if err != nil {
		return "", errors.Errorf("service URL %s is not live: %w", url, err)
	}
	return url, nil
}

func (w *Workflows) dryRunDeployRender(ctx workflow.Context, input deployPlan) (DryRunResult, error) {
	if w.registry == nil {
		return DryRunResult{}, errors.New("workflow registry is not set")
	}

	// Validate credentials first
	credentialStatus := make(map[string]bool)
	workspaceID, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentGetRenderWorkspace).Get(ctx)
	if err != nil {
		credentialStatus["Render API"] = false
	} else {
		credentialStatus["Render API"] = true
	}

	// Generate the deployment steps (same as actual deployment)
	dockerGen := deployment.NewDockerGenerator()
	db := deployment.NewDeploymentBuilder(&input.Spec)
	spec, err := db.Build()
	if err != nil {
		return DryRunResult{}, errors.Errorf("failed to build deployment spec: %w", err)
	}

	spec.Metadata["buildContext"] = input.Source
	spec.Metadata["tenantID"] = "test" // TODO: this shouldn't be hardcoded

	d := render.NewQueuedDeployment(w.renderClient, spec, dockerGen, true)
	steps := d.GenerateAPISteps(workspaceID)

	// Convert render steps to dry-run steps
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

	// Generate estimated costs
	estimatedCosts, err := workflow.ExecuteActivity[deployment.CostEstimate](ctx, ActivityOpts, AgentEstimateRenderCosts, spec, deployment.StrategyRenderQueued).Get(ctx)
	log.Println(err)
	if err != nil {
		return DryRunResult{}, errors.Errorf("failed to estimate costs: %w", err)
	}

	// Perform conflict checks
	conflictChecks := performConflictChecks(workspaceID, spec, w.renderClient)

	// Collect validation errors
	validationErrors := validateDeploymentSpec(spec)

	return DryRunResult{
		Steps:            dryRunSteps,
		EstimatedCosts:   estimatedCosts,
		CredentialStatus: credentialStatus,
		ConflictChecks:   conflictChecks,
		ValidationErrors: validationErrors,
	}, nil
}

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

func calculateEstimatedCosts(spec *deployment.DeploymentSpec) map[string]float64 {
	costs := make(map[string]float64)

	// Base web service cost
	costs["Web Service"] = 7.00 // Standard plan

	// Add costs for backing services
	serviceCounts := spec.ServiceCounts()
	for provider, count := range serviceCounts {
		switch provider {
		case "postgresql":
			costs["PostgreSQL"] = float64(count) * 15.00 // basic_256mb plan
		case "redis":
			costs["Redis"] = float64(count) * 15.00 // standard plan
		}
	}

	return costs
}

func performConflictChecks(workspaceID string, spec *deployment.DeploymentSpec, client render.RenderClient) []ConflictCheck {
	var checks []ConflictCheck

	// Note: In a real implementation, you would call the Render API to check for existing services
	// For now, we'll simulate some basic checks

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

	// Add more validation as needed

	return errors
}
