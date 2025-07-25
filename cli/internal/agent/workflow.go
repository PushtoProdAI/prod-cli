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

var DeployActivityOpts = workflow.ActivityOptions{
	RetryOptions: workflow.RetryOptions{
		MaxAttempts:        1,
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
		Acts:         &Activities{renderClient: renderClient},
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
	w.statusSender("planning", "Understanding your request...")
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
	w.statusSender("summarizing", "Summarizing your request...")
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

func (w *Workflows) deployRender(ctx workflow.Context, input deployPlan) error {
	if w.registry == nil {
		return errors.New("workflow registry is not set")
	}
	dockerGen := deployment.NewDockerGenerator()
	db := deployment.NewDeploymentBuilder(&input.Spec)
	spec, err := db.Build()
	if err != nil {
		return errors.Errorf("failed to build deployment spec: %w", err)
	}
	workspaceID, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentGetRenderWorkspace).Get(ctx)
	if err != nil {
		summary, _ := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentSummarizeError, err.Error()).Get(ctx)
		w.statusSender("error", summary)
		return errors.Errorf("failed to get Render workspace: %w", err)
	}
	spec.Metadata["buildContext"] = input.Source
	spec.Metadata["tenantID"] = "test" // TODO: this shouldn't be hardcoded, we need to get the tenant ID from the context or config

	d := render.NewQueuedDeployment(w.renderClient, spec, dockerGen, true)
	steps := d.GenerateAPISteps(workspaceID)
	log.Printf("Generated %d steps for deployment", len(steps))

	// collect the descriptions to generate a summary
	descriptions := make([]string, len(steps))
	for i, step := range steps {
		descriptions[i] = step.GetDescription()
	}
	summary, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentSummarizeDeploySteps, descriptions).Get(ctx)
	if err != nil {
		log.Printf("Failed to summarize deployment steps: %v", err)
	}
	w.statusSender("deploying", fmt.Sprintf("%s\n-----", summary))
	log.Printf("Generated %d steps for deployment", len(steps))

	stepExecutor := render.NewStepExecutor(w.renderClient)
	for _, step := range steps {
		// this is a bit of a hack. Generating and registering the activities deynamically
		// this is so we can get an activity per step so that we can track the status of each step and handle retries, etc..
		// not sure of the impact of having one off activities, running in a CLI this is probably fine
		// as they'll disappear when closing the app, but we will need to look at what to do if we move to a server
		af := func(ctx context.Context) error {
			w.statusSender("deploying", step.GetDescription())
			err := stepExecutor.ExecuteStep(ctx, step)
			if err != nil {
				w.statusSender("error", fmt.Sprintf("⚠️ Uh oh! Encountered an error trying to %s", step.GetDescription()))
				log.Printf("Error executing step %s: %s: %v", step.GetID(), step.GetDescription(), err)
				return errors.Errorf("failed to execute step %s: %s: %w", step.GetID(), step.GetDescription(), err)
			}
			return nil
		}
		name := fmt.Sprintf("agent.deploy.render.%s.%d", step.GetID(), time.Now().Unix())
		a := workflowext.Activity{Name: name, ActFunc: af}
		a.Register(w.registry)
		_, err := workflow.ExecuteActivity[any](ctx, DeployActivityOpts, name).Get(ctx)
		if err != nil {
			summary, _ := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentSummarizeError, err.Error()).Get(ctx)
			w.statusSender("error", summary)
			return errors.Errorf("failed to execute step %s: %w", step.GetID(), err)
		}
	}

	return nil
}
