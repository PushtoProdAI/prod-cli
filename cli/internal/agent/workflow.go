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
