package agent

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"net/url"
	"sync"
	"time"

	"github.com/cschleiden/go-workflows/client"
	"github.com/cschleiden/go-workflows/workflow"
	"github.com/go-errors/errors"
	"github.com/meroxa/prod/cli/internal/backend"
	"github.com/meroxa/prod/cli/internal/deployment"
	"github.com/meroxa/prod/cli/internal/deployment/flyio"
	"github.com/meroxa/prod/cli/internal/deployment/heroku"
	"github.com/meroxa/prod/cli/internal/deployment/netlify"
	"github.com/meroxa/prod/cli/internal/deployment/render"
	"github.com/meroxa/prod/cli/internal/deployment/vercel"
	prod_error "github.com/meroxa/prod/cli/internal/error"
	"github.com/meroxa/prod/cli/internal/llm"
	"github.com/meroxa/prod/cli/internal/output"
	"github.com/meroxa/prod/cli/internal/workflowext"
)

const (
	PlanDeployWorkflowName             = "agent.planDeploy"
	DeployRenderWorkflowName           = "agent.deploy.render"
	DryRunDeployWorkflowName           = "agent.dryRun.render"
	DeployFlyioWorkflowName            = "agent.deploy.flyio"
	CategorizeEnvVarsWorkflowName      = "agent.categorizeEnvVars"
	DetectExistingWorkflowName         = "agent.detectExisting"
	DryRunRenderWorkflowName           = "agent.dryrun.render"
	DryRunFlyioWorkflowName            = "agent.dryrun.flyio"
	DeployNetlifyWorkflowName          = "agent.deploy.netlify"
	DryRunNetlifyWorkflowName          = "agent.dryrun.netlify"
	SetupJavaScriptProjectWorkflowName = "agent.setupJavaScriptProject"
	DeployVercelWorkflowName           = "agent.deploy.vercel"
	DeployHerokuWorkflowName           = "agent.deploy.heroku"
	RollbackDeploymentWorkflowName     = "agent.rollbackDeployment"
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

// DiffLine represents a single line in a diff
type DiffLine struct {
	Type    string `json:"type"`    // "context", "added", "removed", "header", "fileheader"
	Content string `json:"content"` // the actual line content
}

// SetupJavaScriptProjectResult contains the results of setting up a JavaScript project
type SetupJavaScriptProjectResult struct {
	PackageLockCreated bool        `json:"packageLockCreated"`
	ConfigUpdated      bool        `json:"configUpdated"`
	ConfigDiff         []DiffLine  `json:"configDiff,omitempty"`
	ConfigPath         string      `json:"configPath,omitempty"`
	PackageJsonUpdated bool        `json:"packageJsonUpdated"`
	PackageJsonDiff    []DiffLine  `json:"packageJsonDiff,omitempty"`
	Error              deployError `json:"error"`
	UpdatedPlan        DeployPlan  `json:"updatedPlan"`
}

var _ workflowext.Registerer = (*Workflows)(nil)

// newAgentLLMClient creates an LLM client configured for agent workflows
func newAgentLLMClient() llm.Client {
	return llm.New(llm.Config{
		SessionExtractor: func(ctx context.Context) llm.SessionProvider {
			session := CtxSession(ctx)
			if session == nil {
				return nil
			}
			return session
		},
	})
}

func NewWorkflows(renderClient render.RenderClient, flyClient flyio.FlyioClient, beClient *backend.Client, uiWriter output.StatusWriter) *Workflows {
	return &Workflows{
		Acts: &Activities{
			renderClient: renderClient,
			flyClient:    flyClient,
			beClient:     beClient,
			uiWriter:     uiWriter,
			llmClient:    newAgentLLMClient(),
		},
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
		{Name: DetectExistingWorkflowName, WorkflowFunc: w.detectExistingWorkflow},
		{Name: DryRunRenderWorkflowName, WorkflowFunc: w.dryRunDeployRender},
		{Name: DryRunFlyioWorkflowName, WorkflowFunc: w.dryRunDeployFly},
		{Name: DeployNetlifyWorkflowName, WorkflowFunc: w.deployNetlify},
		{Name: DryRunNetlifyWorkflowName, WorkflowFunc: w.dryRunNetlify},
		{Name: SetupJavaScriptProjectWorkflowName, WorkflowFunc: w.setupJavaScriptProject},
		{Name: DeployVercelWorkflowName, WorkflowFunc: w.deployVercel},
		{Name: DeployHerokuWorkflowName, WorkflowFunc: w.deployHeroku},
		{Name: RollbackDeploymentWorkflowName, WorkflowFunc: w.rollbackDeployment},
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
	case Netlify:
		return c.CreateWorkflowInstance(ctx, client.WorkflowInstanceOptions{InstanceID: fmt.Sprintf("%s.%d", DeployNetlifyWorkflowName, time.Now().Unix())}, DeployNetlifyWorkflowName, input)
	case Vercel:
		return c.CreateWorkflowInstance(ctx, client.WorkflowInstanceOptions{InstanceID: fmt.Sprintf("%s.%d", DeployVercelWorkflowName, time.Now().Unix())}, DeployVercelWorkflowName, input)
	case Heroku:
		return c.CreateWorkflowInstance(ctx, client.WorkflowInstanceOptions{InstanceID: fmt.Sprintf("%s.%d", DeployHerokuWorkflowName, time.Now().Unix())}, DeployHerokuWorkflowName, input)
	default:
		return nil, errors.New("unsupported platform for deployment")
	}
}

func (Workflows) DryRunDeploy(ctx context.Context, c *client.Client, input DeployPlan) (*workflow.Instance, error) {
	slog.Info("Dry run", "platform", input.Platform, "action", input.Action)
	switch input.Platform {
	case Render:
		return c.CreateWorkflowInstance(ctx, client.WorkflowInstanceOptions{InstanceID: fmt.Sprintf("%s.%d", DryRunDeployWorkflowName, time.Now().Unix())}, DryRunDeployWorkflowName, input)
	case FlyIO:
		return c.CreateWorkflowInstance(ctx, client.WorkflowInstanceOptions{InstanceID: fmt.Sprintf("%s.%d", DryRunFlyioWorkflowName, time.Now().Unix())}, DryRunFlyioWorkflowName, input)
	default:
		return nil, errors.New("unsupported platform for dry-run deployment")
	}
}

func (Workflows) Rollback(ctx context.Context, c *client.Client, input DeployPlan) (*workflow.Instance, error) {
	slog.Info("Rollback", "platform", input.Platform, "project", input.Spec.Name)
	return c.CreateWorkflowInstance(ctx, client.WorkflowInstanceOptions{InstanceID: fmt.Sprintf("%s.%d", RollbackDeploymentWorkflowName, time.Now().Unix())}, RollbackDeploymentWorkflowName, input)
}

func (Workflows) CategorizeEnvVars(ctx context.Context, c *client.Client, input DeployPlan) (*workflow.Instance, error) {
	return c.CreateWorkflowInstance(ctx, client.WorkflowInstanceOptions{InstanceID: fmt.Sprintf("%s.%d", CategorizeEnvVarsWorkflowName, time.Now().Unix())}, CategorizeEnvVarsWorkflowName, input)
}

func (Workflows) DetectExisting(ctx context.Context, c *client.Client, input DeployPlan) (*workflow.Instance, error) {
	return c.CreateWorkflowInstance(ctx, client.WorkflowInstanceOptions{InstanceID: fmt.Sprintf("%s.%d", DetectExistingWorkflowName, time.Now().Unix())}, DetectExistingWorkflowName, input)
}

func (Workflows) SetupJavaScriptProject(ctx context.Context, c *client.Client, input DeployPlan) (*workflow.Instance, error) {
	return c.CreateWorkflowInstance(ctx, client.WorkflowInstanceOptions{
		InstanceID: fmt.Sprintf("%s.%d", SetupJavaScriptProjectWorkflowName, time.Now().Unix()),
	}, SetupJavaScriptProjectWorkflowName, input)
}

func (w *Workflows) deployRender(ctx workflow.Context, input DeployPlan) (deployResult, error) {
	if w.registry == nil {
		return deployResult{}, errors.New("workflow registry is not set")
	}
	token := ""
	session := CtxWorkflowSession(ctx)
	if session != nil {
		token = session.AccessToken
	}

	// Use existing project info from DeployPlan
	existingProject := input.ExistingProjectInfo
	if existingProject.Exists {
		slog.Info("Using existing project from detection", "name", existingProject.Name, "id", existingProject.ProjectID, "databases", existingProject.ExistingDatabases)
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
	spec.Metadata["authToken"] = token

	// Set update mode if existing project detected
	if existingProject.Exists {
		spec.IsUpdate = true
		spec.ExistingProjectID = existingProject.ProjectID
		spec.ExistingDatabases = existingProject.ExistingDatabases
	}

	// Generate and summarize deployment steps (for UI feedback)
	workspaceID, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentGetRenderWorkspace).Get(ctx)
	if err != nil {
		// Send the original error before summarizing
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     DeployRenderWorkflowName,
			"activity":     AgentGetRenderWorkspace,
			"component":    "deployment",
			"platform":     "render",
			"project_name": input.Spec.Name,
			"language":     input.Spec.Language,
		})
		summary, e1 := workflow.ExecuteActivity[deployError](ctx, ActivityOpts, AgentSummarizeError, err.Error(), input).Get(ctx)
		if e1 != nil {
			// Send the summarize error
			prod_error.CaptureErrorWithContext(e1, map[string]any{
				"workflow":     DeployRenderWorkflowName,
				"activity":     AgentSummarizeError,
				"component":    "deployment",
				"platform":     "render",
				"project_name": input.Spec.Name,
				"language":     input.Spec.Language,
				"operation":    "summarize_original_error",
			})
			slog.Info("Failed to summarize error", "error", e1)
			return deployResult{Error: deployError{Summary: err.Error()}}, nil
		}
		return deployResult{Error: summary}, nil
	}

	_, err = workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentCreateDockerRepo, input.Spec.Name).Get(ctx)
	if err != nil {
		// Send the original error before summarizing
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     DeployRenderWorkflowName,
			"activity":     AgentCreateDockerRepo,
			"component":    "deployment",
			"platform":     "render",
			"project_name": input.Spec.Name,
			"language":     input.Spec.Language,
		})
		summary, e1 := workflow.ExecuteActivity[deployError](ctx, ActivityOpts, AgentSummarizeError, err.Error(), input).Get(ctx)
		if e1 != nil {
			// Send the summarize error
			prod_error.CaptureErrorWithContext(e1, map[string]any{
				"workflow":     DeployRenderWorkflowName,
				"activity":     AgentSummarizeError,
				"component":    "deployment",
				"platform":     "render",
				"project_name": input.Spec.Name,
				"language":     input.Spec.Language,
				"operation":    "summarize_original_error",
			})
			slog.Info("Failed to summarize error", "error", e1)
			return deployResult{Error: deployError{Summary: err.Error()}}, nil
		}
		return deployResult{Error: summary}, nil
	}
	dockerGen := deployment.NewDockerGenerator(w.uiWriter, spec.EnvVars)
	d := render.NewQueuedDeployment(w.renderClient, spec, dockerGen, true, w.uiWriter)
	steps := d.GenerateAPISteps(workspaceID)
	descriptions := make([]string, len(steps))
	for i, step := range steps {
		descriptions[i] = step.GetDescription()
	}
	_, err = workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentSummarizeDeploySteps, descriptions).Get(ctx)
	if err != nil {
		slog.Info("Failed to summarize deployment steps", "error", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     DeployRenderWorkflowName,
			"activity":     AgentSummarizeDeploySteps,
			"component":    "deployment",
			"platform":     "render",
			"project_name": input.Spec.Name,
			"language":     input.Spec.Language,
		})
	}

	buildOutputPath, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentDetermineBuildOutput, input.Spec.BuildOutput).Get(ctx)
	if err != nil {
		slog.Info("Failed to determine build output path", "error", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     DeployRenderWorkflowName,
			"activity":     AgentDetermineBuildOutput,
			"component":    "deployment",
			"platform":     "render",
			"project_name": input.Spec.Name,
			"language":     input.Spec.Language,
		})
	} else {
		slog.Info("Using build output path", "path", buildOutputPath)
		// Update the deployment spec's OutputDir with the final resolved build output path
		spec.OutputDir = buildOutputPath
	}

	createdResources, err := workflow.ExecuteActivity[[]deployment.CreatedResource](ctx, ActivityOpts, AgentDeploySteps, *spec, input.Platform).Get(ctx)
	if err != nil {
		// Send the original error before summarizing
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     DeployRenderWorkflowName,
			"activity":     AgentDeploySteps,
			"component":    "deployment",
			"platform":     "render",
			"project_name": input.Spec.Name,
			"language":     input.Spec.Language,
		})
		summary, e1 := workflow.ExecuteActivity[deployError](ctx, ActivityOpts, AgentSummarizeError, err.Error(), input).Get(ctx)
		if e1 != nil {
			// Send the summarize error
			prod_error.CaptureErrorWithContext(e1, map[string]any{
				"workflow":     DeployRenderWorkflowName,
				"activity":     AgentSummarizeError,
				"component":    "deployment",
				"platform":     "render",
				"project_name": input.Spec.Name,
				"language":     input.Spec.Language,
				"operation":    "summarize_original_error",
			})
			return deployResult{Error: deployError{Summary: err.Error()}}, nil
		}
		slog.Info("Deployment failed", "error", err)
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

	// Get service URL
	u, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentGetRenderServiceURL, ws.ID).Get(ctx)
	if err != nil {
		return deployResult{}, errors.Errorf("failed to get service URL for %s: %w", ws.Name, err)
	}

	// First, check deploy status (for both fresh and updates)
	if deployID, ok := ws.Metadata["deployId"].(string); ok && deployID != "" {
		deployCheckOpts := ActivityOpts
		deployCheckOpts.RetryOptions.MaxAttempts = 15
		_, err := workflow.ExecuteActivity[any](ctx, deployCheckOpts, AgentWaitForRenderDeploy, ws.ID, deployID).Get(ctx)
		if err != nil {
			prod_error.CaptureErrorWithContext(err, map[string]any{
				"workflow":     DeployRenderWorkflowName,
				"activity":     AgentWaitForRenderDeploy,
				"component":    "deployment",
				"platform":     "render",
				"project_name": input.Spec.Name,
				"language":     input.Spec.Language,
			})
			summary, e1 := workflow.ExecuteActivity[deployError](ctx, ActivityOpts, AgentSummarizeError, err.Error(), input).Get(ctx)
			if e1 != nil {
				prod_error.CaptureErrorWithContext(e1, map[string]any{
					"workflow":     DeployRenderWorkflowName,
					"activity":     AgentSummarizeError,
					"component":    "deployment",
					"platform":     "render",
					"project_name": input.Spec.Name,
					"language":     input.Spec.Language,
					"operation":    "summarize_original_error",
				})
				return deployResult{Error: deployError{Summary: err.Error()}}, nil
			}
			slog.Info("deployment failed", "deployId", deployID, "error", err)
			return deployResult{Error: summary}, nil
		}
	}

	// Then verify URL is live
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
	_, err = workflow.ExecuteActivity[string](ctx, liveCheckOpts, AgentIsURLLive, fullUrl).Get(ctx)
	if err != nil {
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     DeployRenderWorkflowName,
			"activity":     AgentIsURLLive,
			"component":    "deployment",
			"platform":     "render",
			"project_name": input.Spec.Name,
			"language":     input.Spec.Language,
		})
		summary, e1 := workflow.ExecuteActivity[deployError](ctx, ActivityOpts, AgentSummarizeError, err.Error(), input).Get(ctx)
		if e1 != nil {
			prod_error.CaptureErrorWithContext(e1, map[string]any{
				"workflow":     DeployRenderWorkflowName,
				"activity":     AgentSummarizeError,
				"component":    "deployment",
				"platform":     "render",
				"project_name": input.Spec.Name,
				"language":     input.Spec.Language,
				"operation":    "summarize_original_error",
			})
			return deployResult{Error: deployError{Summary: err.Error()}}, nil
		}
		slog.Info("service URL is not live", "url", fullUrl, "error", err)
		return deployResult{Error: summary}, nil
	}

	return deployResult{Url: fullUrl}, nil
}

func (w *Workflows) deployFly(ctx workflow.Context, input DeployPlan) (deployResult, error) {
	if w.registry == nil {
		return deployResult{}, errors.New("workflow registry is not set")
	}

	// Use existing project info from DeployPlan
	existingProject := input.ExistingProjectInfo
	if existingProject.Exists {
		slog.Info("Using existing project from detection", "name", existingProject.Name, "id", existingProject.ProjectID, "databases", existingProject.ExistingDatabases)
	}

	// Log deployment start
	operationId, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentLogDeploymentStart, "flyio", input.Spec, input.Source).Get(ctx)
	if err != nil {
		slog.Error("Failed to log deployment start", "error", err)
		// Continue with deployment even if logging fails
	}

	envVars := input.CollectedEnvVars

	// Build deployment spec
	db := deployment.NewDeploymentBuilder(&input.Spec, envVars)
	spec, err := db.Build()
	if err != nil {
		// Log deployment failure
		if operationId != "" {
			workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
				"error":    err.Error(),
				"platform": "flyio",
				"stage":    "spec_build",
			}).Get(ctx)
		}
		return deployResult{}, errors.Errorf("failed to build deployment spec: %w", err)
	}
	spec.Metadata["buildContext"] = input.Source

	// Set update mode if existing project detected
	if existingProject.Exists {
		spec.IsUpdate = true
		spec.ExistingProjectID = existingProject.ProjectID
		spec.ExistingDatabases = existingProject.ExistingDatabases
	}

	// Generate and summarize deployment steps
	dockerGen := deployment.NewDockerGenerator(w.uiWriter, spec.EnvVars)
	d := flyio.NewFlyioQueuedDeployment(w.flyClient, spec, dockerGen, w.uiWriter)
	steps := d.GenerateAPISteps()
	descriptions := make([]string, len(steps))
	for i, step := range steps {
		descriptions[i] = step.GetDescription()
	}
	_, err = workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentSummarizeDeploySteps, descriptions).Get(ctx)
	if err != nil {
		slog.Info("Failed to summarize deployment steps", "error", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     DeployFlyioWorkflowName,
			"activity":     AgentSummarizeDeploySteps,
			"component":    "deployment",
			"platform":     "flyio",
			"project_name": input.Spec.Name,
			"language":     input.Spec.Language,
		})
	}

	createdResources, err := workflow.ExecuteActivity[[]deployment.CreatedResource](ctx, ActivityOpts, AgentDeploySteps, *spec, input.Platform).Get(ctx)
	if err != nil {
		// Send the original error before summarizing
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     DeployFlyioWorkflowName,
			"activity":     AgentDeploySteps,
			"component":    "deployment",
			"platform":     "flyio",
			"project_name": input.Spec.Name,
			"language":     input.Spec.Language,
		})

		slog.Error("Deployment failed, attempting rollback", "error", err)

		previousDeploy, rollbackErr := workflow.ExecuteActivity[*deployment.DeploymentInfo](ctx, ActivityOpts, AgentGetPreviousDeployment, *spec, FlyIO).Get(ctx)
		if rollbackErr != nil {
			slog.Warn("No previous deployment available for rollback", "error", rollbackErr)
			if operationId != "" {
				workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
					"error":              err.Error(),
					"platform":           "flyio",
					"stage":              "deployment_steps",
					"no_previous_deploy": true,
				}).Get(ctx)
			}
			return deployResult{
				Error: deployError{
					Summary: "Deployment failed. This is your first deployment, so there's no previous version to roll back to",
				},
			}, nil
		}

		slog.Info("Found previous deployment for rollback", "image", previousDeploy.ID)

		_, rollbackErr = workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentRollbackDeployment, *spec, FlyIO, previousDeploy.ID).Get(ctx)
		if rollbackErr != nil {
			slog.Error("Rollback failed", "error", rollbackErr, "target_image", previousDeploy.ID)
			if operationId != "" {
				workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
					"error":           err.Error(),
					"platform":        "flyio",
					"stage":           "deployment_steps",
					"rollback_error":  rollbackErr.Error(),
					"rollback_target": previousDeploy.ID,
				}).Get(ctx)
			}
			summary, e1 := workflow.ExecuteActivity[deployError](ctx, ActivityOpts, AgentSummarizeError, err.Error(), input).Get(ctx)
			if e1 != nil {
				prod_error.CaptureErrorWithContext(e1, map[string]any{
					"workflow":     DeployFlyioWorkflowName,
					"activity":     AgentSummarizeError,
					"component":    "deployment",
					"platform":     "flyio",
					"project_name": input.Spec.Name,
					"language":     input.Spec.Language,
					"operation":    "summarize_original_error",
				})
				return deployResult{Error: deployError{Summary: err.Error()}}, nil
			}
			return deployResult{Error: summary}, nil
		}

		slog.Info("Rollback completed successfully", "rolled_back_to", previousDeploy.ID)

		if operationId != "" {
			workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "rolled_back", map[string]any{
				"error":          err.Error(),
				"platform":       "flyio",
				"stage":          "deployment_steps",
				"rolled_back_to": previousDeploy.ID,
			}).Get(ctx)
		}
		return deployResult{
			Error: deployError{
				Summary:   "Deployment failed. We've automatically rolled back to your previous working version",
				IsWarning: true,
			},
		}, nil
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
	u, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentGetFlyIOAppURL, ws.ID).Get(ctx)
	if err != nil {
		return deployResult{}, errors.Errorf("failed to get service URL for %s: %w", ws.Name, err)
	}
	path, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentDetermineRootPath, input.Spec.Routes).Get(ctx)
	if err != nil {
		// if there is an error, we will just default to /
		slog.Info("Failed to determine root path for application", "error", err)
		path = "/"
	}

	fullUrl, err := url.JoinPath(u, path)
	if err != nil {
		slog.Info("Failed to combine paths", "error", err)
		fullUrl = u
	}

	_, err = workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentIsURLLive, fullUrl).Get(ctx)
	if err != nil {
		// Send the original error before summarizing
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     DeployFlyioWorkflowName,
			"activity":     AgentIsURLLive,
			"component":    "deployment",
			"platform":     "flyio",
			"project_name": input.Spec.Name,
			"language":     input.Spec.Language,
		})

		slog.Error("Health check failed, attempting rollback", "error", err, "url", fullUrl)

		previousDeploy, rollbackErr := workflow.ExecuteActivity[*deployment.DeploymentInfo](ctx, ActivityOpts, AgentGetPreviousDeployment, *spec, FlyIO).Get(ctx)
		if rollbackErr != nil {
			slog.Warn("No previous deployment available for rollback", "error", rollbackErr)
			if operationId != "" {
				workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
					"error":              err.Error(),
					"platform":           "flyio",
					"stage":              "url_verification",
					"url":                fullUrl,
					"no_previous_deploy": true,
				}).Get(ctx)
			}
			return deployResult{
				Error: deployError{
					Summary: "Deployment failed health check. This is your first deployment, so there's no previous version to roll back to",
				},
			}, nil
		}

		slog.Info("Found previous deployment for rollback", "image", previousDeploy.ID)

		_, rollbackErr = workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentRollbackDeployment, *spec, FlyIO, previousDeploy.ID).Get(ctx)
		if rollbackErr != nil {
			slog.Error("Rollback failed", "error", rollbackErr, "target_image", previousDeploy.ID)
			if operationId != "" {
				workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
					"error":           err.Error(),
					"platform":        "flyio",
					"stage":           "url_verification",
					"url":             fullUrl,
					"rollback_error":  rollbackErr.Error(),
					"rollback_target": previousDeploy.ID,
				}).Get(ctx)
			}
			return deployResult{}, errors.Errorf("service URL %s is not live and rollback failed: %w", fullUrl, err)
		}

		slog.Info("Rollback completed successfully", "rolled_back_to", previousDeploy.ID)

		if operationId != "" {
			workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "rolled_back", map[string]any{
				"error":          err.Error(),
				"platform":       "flyio",
				"stage":          "url_verification",
				"url":            fullUrl,
				"rolled_back_to": previousDeploy.ID,
			}).Get(ctx)
		}
		return deployResult{
			Error: deployError{
				Summary:   "Deployment failed health check. We've automatically rolled back to your previous working version",
				IsWarning: true,
			},
		}, nil
	}

	// Log deployment success
	if operationId != "" {
		workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "success", map[string]any{
			"url":               fullUrl,
			"platform":          "flyio",
			"resources_created": createdResources,
			"app_id":            ws.ID,
		}).Get(ctx)
	}

	return deployResult{Url: fullUrl}, nil
}

func (w *Workflows) dryRunDeployRender(ctx workflow.Context, input DeployPlan) (DryRunResult, error) {
	if w.registry == nil {
		return DryRunResult{}, errors.New("workflow registry is not set")
	}

	token := ""
	session := CtxWorkflowSession(ctx)
	if session != nil {
		token = session.AccessToken
	}

	credentialStatus := make(map[string]bool)
	workspaceID, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentGetRenderWorkspace).Get(ctx)
	if err != nil {
		credentialStatus["Render API"] = false
	} else {
		credentialStatus["Render API"] = true
	}

	envVars := input.CollectedEnvVars

	db := deployment.NewDeploymentBuilder(&input.Spec, envVars)
	spec, err := db.Build()
	if err != nil {
		return DryRunResult{}, errors.Errorf("failed to build deployment spec: %w", err)
	}
	dockerGen := deployment.NewDockerGenerator(w.uiWriter, spec.EnvVars)

	spec.Metadata["buildContext"] = input.Source
	spec.Metadata["tenantID"] = "test"
	spec.Metadata["authToken"] = token

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
	if err != nil {
		slog.Error("Error estimating costs", "error", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     DryRunRenderWorkflowName,
			"activity":     AgentEstimateRenderCosts,
			"component":    "deployment",
			"platform":     "render",
			"project_name": input.Spec.Name,
			"language":     input.Spec.Language,
		})
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

func (w *Workflows) dryRunDeployFly(ctx workflow.Context, input DeployPlan) (DryRunResult, error) {
	if w.registry == nil {
		return DryRunResult{}, errors.New("workflow registry is not set")
	}

	credentialStatus := make(map[string]bool)
	// Check Fly.io credentials by attempting to get an app (this will fail if not authenticated)
	// Use a timeout to prevent hanging
	checkCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := w.flyClient.GetApp(checkCtx, "test-app")
	if err != nil {
		credentialStatus["Fly.io API"] = false
	} else {
		credentialStatus["Fly.io API"] = true
	}

	envVars := input.CollectedEnvVars

	// Build deployment spec
	db := deployment.NewDeploymentBuilder(&input.Spec, envVars)
	spec, err := db.Build()
	if err != nil {
		return DryRunResult{}, errors.Errorf("failed to build deployment spec: %w", err)
	}

	spec.Metadata["buildContext"] = input.Source

	// Generate deployment steps
	dockerGen := deployment.NewDockerGenerator(w.uiWriter, spec.EnvVars)
	d := flyio.NewFlyioQueuedDeployment(w.flyClient, spec, dockerGen, w.uiWriter)
	steps := d.GenerateAPISteps()

	dryRunSteps := make([]DryRunStep, len(steps))
	for i, step := range steps {
		dryRunSteps[i] = DryRunStep{
			ID:          step.GetID(),
			Description: step.GetDescription(),
			Type:        getFlyioStepType(step),
			Config:      extractFlyioStepConfig(step),
			DependsOn:   step.GetDependencies(),
		}
	}

	// Estimate costs
	estimatedCosts, err := workflow.ExecuteActivity[deployment.CostEstimate](ctx, ActivityOpts, AgentEstimateFlyioCosts, *spec, deployment.StrategyFlyio).Get(ctx)
	if err != nil {
		slog.Info("Failed to estimate costs", "error", err)
		estimatedCosts = deployment.CostEstimate{}
	}

	// Perform conflict checks and validation
	conflictChecks := performFlyioConflictChecks(spec, w.flyClient)
	validationErrors := validateDeploymentSpec(spec)

	return DryRunResult{
		Steps:            dryRunSteps,
		EstimatedCosts:   estimatedCosts,
		CredentialStatus: credentialStatus,
		ConflictChecks:   conflictChecks,
		ValidationErrors: validationErrors,
	}, nil
}

func (w *Workflows) detectExistingWorkflow(ctx workflow.Context, deployPlan DeployPlan) (ExistingProjectInfo, error) {
	workflow.Logger(ctx).Info("starting detectExisting workflow", "platform", deployPlan.Platform, "project", deployPlan.Spec.Name)

	existingProject, err := workflow.ExecuteActivity[ExistingProjectInfo](ctx, ActivityOpts, AgentCheckExistingProject, deployPlan.Platform, deployPlan.Spec.Name, deployPlan.Source).Get(ctx)
	if err != nil {
		workflow.Logger(ctx).Error("Failed to check for existing project", "error", err)
		return ExistingProjectInfo{}, err
	}

	if existingProject.Exists {
		workflow.Logger(ctx).Info("Detected existing project", "name", existingProject.Name, "id", existingProject.ProjectID, "databases", existingProject.ExistingDatabases)
	} else {
		workflow.Logger(ctx).Info("No existing project found")
	}

	return existingProject, nil
}

func (w *Workflows) categorizeEnvVars(ctx workflow.Context, deployPlan DeployPlan) ([]deployment.EnvVar, error) {
	startTime := time.Now()
	workflow.Logger(ctx).Info("starting categorizeEnvVars", "total_env_vars", len(deployPlan.Spec.EnvVars))

	spec := deployPlan.Spec
	if len(spec.EnvVars) == 0 {
		return []deployment.EnvVar{}, nil
	}

	dbList := make([]string, len(spec.ServiceRequirements))
	for i, service := range spec.ServiceRequirements {
		dbList[i] = service.Provider
	}
	workflow.Logger(ctx).Info("created db list", "providers", dbList)

	// Early return if no environment variables to process
	if len(spec.EnvVars) == 0 {
		workflow.Logger(ctx).Info("no environment variables to process, skipping categorization")

		// Still need to read env files for potential future use
		envFilesStart := time.Now()
		workflow.Logger(ctx).Info("starting to read env files (no env vars to categorize)")
		fromEnvFiles, err := workflow.ExecuteActivity[[]deployment.EnvVar](ctx, ActivityOpts, AgentReadEnvFiles, deployPlan.Source).Get(ctx)
		envFilesDuration := time.Since(envFilesStart)

		if err != nil {
			workflow.Logger(ctx).Error("failed to read environment variables from .env files", "error", err, "duration", envFilesDuration)
			prod_error.CaptureErrorWithContext(err, map[string]any{
				"workflow":     CategorizeEnvVarsWorkflowName,
				"activity":     AgentReadEnvFiles,
				"component":    "workflow",
				"project_name": deployPlan.Spec.Name,
				"language":     deployPlan.Spec.Language,
			})
			return []deployment.EnvVar{}, errors.Errorf("failed to read environment variables from .env files: %w", err)
		}
		workflow.Logger(ctx).Info("completed reading env files", "count", len(fromEnvFiles), "duration", envFilesDuration)

		totalDuration := time.Since(startTime)
		workflow.Logger(ctx).Info("categorizeEnvVars completed (no env vars)", "total_duration", totalDuration)

		return []deployment.EnvVar{}, nil
	}

	// Use a mutex to safely append to the shared slice
	var mu sync.Mutex
	envVars := make([]deployment.EnvVar, 0, len(spec.EnvVars))

	wg := workflow.NewWaitGroup()
	wg.Add(len(spec.EnvVars))

	// this could be a lot of env vars, so we will categorize them in parallel
	categorizeStart := time.Now()
	workflow.Logger(ctx).Info("starting parallel categorization", "count", len(spec.EnvVars))

	for i, envVar := range spec.EnvVars {
		workflow.Logger(ctx).Debug("scheduling categorization", "index", i, "var_name", envVar.VarName)
		workflow.Go(ctx, func(ctx workflow.Context) {
			defer wg.Done()
			activityStart := time.Now()
			workflow.Logger(ctx).Debug("starting categorization activity", "var_name", envVar.VarName)

			ev, err := workflow.ExecuteActivity[deployment.EnvVar](ctx, ActivityOpts, AgentCategorizeEnvVars, dbList, envVar).Get(ctx)
			activityDuration := time.Since(activityStart)

			if err != nil {
				workflow.Logger(ctx).Error("failed to categorize environment variable", "var", envVar.VarName, "error", err, "duration", activityDuration)
				// if there is an error, we will just add the env var as-is
				ev = deployment.EnvVar{
					Name: envVar.VarName,
				}
			} else {
				workflow.Logger(ctx).Debug("completed categorization activity", "var_name", envVar.VarName, "duration", activityDuration, "is_db_related", !ev.IsNotDBRelated())
			}

			// Safely append to the shared slice using a mutex
			mu.Lock()
			envVars = append(envVars, ev)
			mu.Unlock()
		})
	}

	workflow.Logger(ctx).Info("waiting for all categorization activities to complete")

	// Wait for all activities to complete
	wg.Wait(ctx)
	waitDuration := time.Since(categorizeStart)
	workflow.Logger(ctx).Info("all categorization activities completed", "total_duration", waitDuration)

	// Read env files
	envFilesStart := time.Now()
	workflow.Logger(ctx).Info("starting to read env files")
	fromEnvFiles, err := workflow.ExecuteActivity[[]deployment.EnvVar](ctx, ActivityOpts, AgentReadEnvFiles, deployPlan.Source).Get(ctx)
	envFilesDuration := time.Since(envFilesStart)

	if err != nil {
		workflow.Logger(ctx).Error("failed to read environment variables from .env files", "error", err, "duration", envFilesDuration)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     CategorizeEnvVarsWorkflowName,
			"activity":     AgentReadEnvFiles,
			"component":    "workflow",
			"project_name": deployPlan.Spec.Name,
			"language":     deployPlan.Spec.Language,
		})
		return []deployment.EnvVar{}, errors.Errorf("failed to read environment variables from .env files: %w", err)
	}
	workflow.Logger(ctx).Info("completed reading env files", "count", len(fromEnvFiles), "duration", envFilesDuration)

	// convert to a map to make the next step a little easier
	mapStart := time.Now()
	envMap := maps.Collect(func(yield func(string, deployment.EnvVar) bool) {
		for _, e := range fromEnvFiles {
			if !yield(e.Name, e) {
				return
			}
		}
	})
	mapDuration := time.Since(mapStart)
	workflow.Logger(ctx).Debug("created env map", "size", len(envMap), "duration", mapDuration)

	// we will take values that in env files and use those as suggested values
	mergeStart := time.Now()
	workflow.Logger(ctx).Info("starting to merge env file values")
	mergedCount := 0
	for i := range envVars {
		if envVars[i].IsNotDBRelated() {
			fromEnvFile, ok := envMap[envVars[i].Name]
			if !ok {
				continue
			}
			slog.Info("env var found in env file", "name", envVars[i].Name, "value", fromEnvFile.Value)
			envVars[i].Value = fromEnvFile.Value
			mergedCount++
		}
	}
	mergeDuration := time.Since(mergeStart)
	workflow.Logger(ctx).Info("completed merging env file values", "merged_count", mergedCount, "duration", mergeDuration)

	totalDuration := time.Since(startTime)
	workflow.Logger(ctx).Info("categorizeEnvVars completed",
		"total_duration", totalDuration,
		"categorization_duration", waitDuration,
		"env_files_duration", envFilesDuration,
		"map_creation_duration", mapDuration,
		"merge_duration", mergeDuration,
		"total_env_vars", len(envVars))

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

// Helper functions for Fly.io dry run workflow

func getFlyioStepType(step flyio.FlyioAPIStep) string {
	switch step.(type) {
	case *flyio.CreateFlyioAppStep:
		return "app"
	case *flyio.CreateFlyioServiceStep:
		return "service"
	case *flyio.DeployFlyioConfigStep:
		return "config"
	case *flyio.AttachPostgresStep:
		return "attach"
	default:
		return "unknown"
	}
}

func extractFlyioStepConfig(step flyio.FlyioAPIStep) map[string]any {
	config := make(map[string]any)

	// Since the fields are unexported, we'll just use the step description
	// and type information that's available through the interface
	config["step_id"] = step.GetID()
	config["description"] = step.GetDescription()

	return config
}

func performFlyioConflictChecks(spec *deployment.DeploymentSpec, client flyio.FlyioClient) []ConflictCheck {
	var conflicts []ConflictCheck

	// Check for app name conflicts by attempting to get the app
	// Use a timeout to prevent hanging
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := client.GetApp(ctx, spec.Name)
	if err == nil {
		// App exists, this is a conflict
		conflicts = append(conflicts, ConflictCheck{
			Resource: "app",
			Status:   "conflict",
			Message:  fmt.Sprintf("App name '%s' already exists", spec.Name),
		})
	}

	return conflicts
}

func (w *Workflows) deployNetlify(ctx workflow.Context, input DeployPlan) (deployResult, error) {
	slog.Info("deployNetlify workflow started", "platform", input.Platform)
	slog.Info("DeployPlan details", "action", input.Action, "source", input.Source, "specName", input.Spec.Name, "specLanguage", input.Spec.Language)

	// Use existing project info from DeployPlan
	existingProject := input.ExistingProjectInfo
	if existingProject.Exists {
		slog.Info("Using existing project from detection", "name", existingProject.Name, "id", existingProject.ProjectID, "databases", existingProject.ExistingDatabases)
	}

	// Log deployment start
	operationId, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentLogDeploymentStart, "netlify", input.Spec, input.Source).Get(ctx)
	if err != nil {
		slog.Error("Failed to log deployment start", "error", err)
		// Continue with deployment even if logging fails
	}

	// Build deployment spec from the plan
	slog.Info("Building deployment spec")
	db := deployment.NewDeploymentBuilder(&input.Spec, input.CollectedEnvVars)
	spec, err := db.Build()
	if err != nil {
		slog.Info("Failed to build deployment spec", "error", err)
		return deployResult{Error: deployError{Summary: fmt.Sprintf("Failed to build deployment spec: %v", err)}}, nil
	}
	slog.Info("Deployment spec built successfully")

	// Add metadata
	spec.Metadata["buildContext"] = input.Source
	spec.Metadata["platform"] = "netlify"

	// Set update mode if existing project detected
	if existingProject.Exists {
		spec.IsUpdate = true
		spec.ExistingProjectID = existingProject.ProjectID
		spec.ExistingDatabases = existingProject.ExistingDatabases
	}

	d := netlify.NewNetlifyQueuedDeployment(&netlify.CLINetlifyClient{}, spec, w.uiWriter)
	steps := d.GenerateAPISteps()
	descriptions := make([]string, len(steps))
	for i, step := range steps {
		descriptions[i] = step.GetDescription()
	}
	_, err = workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentSummarizeDeploySteps, descriptions).Get(ctx)
	if err != nil {
		slog.Error("Failed to summarize deployment steps", "error", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     DeployNetlifyWorkflowName,
			"activity":     AgentSummarizeDeploySteps,
			"component":    "deployment",
			"platform":     "netlify",
			"project_name": input.Spec.Name,
			"language":     input.Spec.Language,
		})
	}

	createdResources, err := workflow.ExecuteActivity[[]deployment.CreatedResource](ctx, ActivityOpts, AgentDeploySteps, *spec, input.Platform).Get(ctx)
	if err != nil {
		// Send the original error before summarizing
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     DeployNetlifyWorkflowName,
			"activity":     AgentDeploySteps,
			"component":    "deployment",
			"platform":     "netlify",
			"project_name": input.Spec.Name,
			"language":     input.Spec.Language,
		})
		// Log deployment failure
		if operationId != "" {
			workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
				"error":    err.Error(),
				"platform": "netlify",
				"stage":    "deployment_steps",
			}).Get(ctx)
		}
		summary, e1 := workflow.ExecuteActivity[deployError](ctx, ActivityOpts, AgentSummarizeError, err.Error(), input).Get(ctx)
		if e1 != nil {
			// Send the summarize error
			prod_error.CaptureErrorWithContext(e1, map[string]any{
				"workflow":     DeployNetlifyWorkflowName,
				"activity":     AgentSummarizeError,
				"component":    "deployment",
				"platform":     "netlify",
				"project_name": input.Spec.Name,
				"language":     input.Spec.Language,
				"operation":    "summarize_original_error",
			})
			return deployResult{Error: deployError{Summary: err.Error()}}, nil
		}
		slog.Error("Deployment failed", "error", err)
		return deployResult{Error: summary}, nil
	}

	// Extract deployment URL and site ID from created resources
	var deploymentURL string
	var siteID string
	for _, resource := range createdResources {
		if url, ok := resource.Metadata["url"].(string); ok {
			deploymentURL = url
		}
		if resource.Type == "netlify_site" {
			siteID = resource.ID
		}
	}

	if deploymentURL == "" {
		slog.Info("No deployment URL found in created resources")
		deploymentURL = "Deployment completed but URL not available"
	}

	// Store site ID in spec for rollback operations
	if siteID != "" {
		spec.ExistingProjectID = siteID
	}

	path, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentDetermineRootPath, input.Spec.Routes).Get(ctx)
	if err != nil {
		// if there is an error, we will just default to /
		slog.Info("Failed to determine root path for application", "error", err)
		path = "/"
	}

	fullUrl, err := url.JoinPath(deploymentURL, path)
	if err != nil {
		slog.Info("Failed to combine paths", "error", err)
		fullUrl = deploymentURL
	}

	_, err = workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentIsURLLive, fullUrl).Get(ctx)
	if err != nil {
		slog.Error("Health check failed, attempting rollback", "error", err, "url", fullUrl)

		previousDeploy, rollbackErr := workflow.ExecuteActivity[*deployment.DeploymentInfo](ctx, ActivityOpts, AgentGetPreviousDeployment, *spec, Netlify).Get(ctx)
		if rollbackErr != nil {
			slog.Warn("No previous deployment available for rollback", "error", rollbackErr)
			if operationId != "" {
				workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
					"error":              err.Error(),
					"platform":           "netlify",
					"stage":              "url_verification",
					"url":                fullUrl,
					"no_previous_deploy": true,
				}).Get(ctx)
			}
			return deployResult{
				Error: deployError{
					Summary: "Deployment failed health check. This is your first deployment, so there's no previous version to roll back to",
				},
			}, nil
		}

		slog.Info("Found previous deployment for rollback", "deployment_id", previousDeploy.ID, "url", previousDeploy.URL)

		_, rollbackErr = workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentRollbackDeployment, *spec, Netlify, previousDeploy.URL).Get(ctx)
		if rollbackErr != nil {
			slog.Error("Rollback failed", "error", rollbackErr, "target_deployment", previousDeploy.URL)
			if operationId != "" {
				workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
					"error":           err.Error(),
					"platform":        "netlify",
					"stage":           "url_verification",
					"url":             fullUrl,
					"rollback_error":  rollbackErr.Error(),
					"rollback_target": previousDeploy.URL,
				}).Get(ctx)
			}
			return deployResult{}, errors.Errorf("service URL %s is not live and rollback failed: %w", fullUrl, err)
		}

		slog.Info("Rollback completed successfully", "rolled_back_to", previousDeploy.ID)

		if operationId != "" {
			workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "rolled_back", map[string]any{
				"error":              err.Error(),
				"platform":           "netlify",
				"stage":              "url_verification",
				"url":                fullUrl,
				"rolled_back_to":     previousDeploy.ID,
				"rolled_back_to_url": previousDeploy.URL,
			}).Get(ctx)
		}
		return deployResult{
			Error: deployError{
				Summary:   "Deployment failed health check. We've automatically rolled back to your previous working version",
				IsWarning: true,
			},
		}, nil
	}

	// Log deployment success
	if operationId != "" {
		workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "success", map[string]any{
			"url":               fullUrl,
			"platform":          "netlify",
			"resources_created": createdResources,
		}).Get(ctx)
	}

	slog.Info("Netlify deployment workflow completed successfully")
	return deployResult{
		Url: deploymentURL,
	}, nil
}

func (w *Workflows) deployVercel(ctx workflow.Context, input DeployPlan) (deployResult, error) {
	slog.Info("deployVercel workflow started", "platform", input.Platform)
	slog.Info("DeployPlan details", "action", input.Action, "source", input.Source, "specName", input.Spec.Name, "specLanguage", input.Spec.Language)

	// Use existing project info from DeployPlan
	existingProject := input.ExistingProjectInfo
	if existingProject.Exists {
		slog.Info("Using existing project from detection", "name", existingProject.Name, "id", existingProject.ProjectID, "databases", existingProject.ExistingDatabases)
	}

	// Log deployment start
	operationId, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentLogDeploymentStart, "vercel", input.Spec, input.Source).Get(ctx)
	if err != nil {
		slog.Error("Failed to log deployment start", "error", err)
		// Continue with deployment even if logging fails
	}

	// Build deployment spec from the plan
	slog.Info("Building deployment spec")
	db := deployment.NewDeploymentBuilder(&input.Spec, input.CollectedEnvVars)
	spec, err := db.Build()
	if err != nil {
		slog.Info("Failed to build deployment spec", "error", err)
		return deployResult{Error: deployError{Summary: fmt.Sprintf("Failed to build deployment spec: %v", err)}}, nil
	}
	slog.Info("Deployment spec built successfully")

	// Add metadata
	spec.Metadata["buildContext"] = input.Source
	spec.Metadata["platform"] = "vercel"

	// Set update mode if existing project detected
	if existingProject.Exists {
		spec.IsUpdate = true
		spec.ExistingProjectID = existingProject.ProjectID
		spec.ExistingDatabases = existingProject.ExistingDatabases
	}

	d := vercel.NewVercelQueuedDeployment(vercel.NewCLIVercelClient(), spec, w.uiWriter)
	steps := d.GenerateAPISteps()
	descriptions := make([]string, len(steps))
	for i, step := range steps {
		descriptions[i] = step.GetDescription()
	}
	_, err = workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentSummarizeDeploySteps, descriptions).Get(ctx)
	if err != nil {
		slog.Error("Failed to summarize deployment steps", "error", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     DeployVercelWorkflowName,
			"activity":     AgentSummarizeDeploySteps,
			"component":    "deployment",
			"platform":     "vercel",
			"project_name": input.Spec.Name,
			"language":     input.Spec.Language,
		})
	}

	createdResources, err := workflow.ExecuteActivity[[]deployment.CreatedResource](ctx, ActivityOpts, AgentDeploySteps, *spec, input.Platform).Get(ctx)
	if err != nil {
		// Send the original error before summarizing
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     DeployVercelWorkflowName,
			"activity":     AgentDeploySteps,
			"component":    "deployment",
			"platform":     "vercel",
			"project_name": input.Spec.Name,
			"language":     input.Spec.Language,
		})
		// Log deployment failure
		if operationId != "" {
			workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
				"error":    err.Error(),
				"platform": "vercel",
				"stage":    "deployment_steps",
			}).Get(ctx)
		}
		summary, e1 := workflow.ExecuteActivity[deployError](ctx, ActivityOpts, AgentSummarizeError, err.Error(), input).Get(ctx)
		if e1 != nil {
			// Send the summarize error
			prod_error.CaptureErrorWithContext(e1, map[string]any{
				"workflow":     DeployVercelWorkflowName,
				"activity":     AgentSummarizeError,
				"component":    "deployment",
				"platform":     "vercel",
				"project_name": input.Spec.Name,
				"language":     input.Spec.Language,
				"operation":    "summarize_original_error",
			})
			return deployResult{Error: deployError{Summary: err.Error()}}, nil
		}
		slog.Error("Deployment failed", "error", err)
		return deployResult{Error: summary}, nil
	}

	// Extract deployment URL from created resources
	var deploymentURL string
	for _, resource := range createdResources {
		if url, ok := resource.Metadata["url"].(string); ok {
			deploymentURL = url
			break
		}
	}

	if deploymentURL == "" {
		slog.Info("No deployment URL found in created resources")
		deploymentURL = "Deployment completed but URL not available"
	}

	path, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentDetermineRootPath, input.Spec.Routes).Get(ctx)
	if err != nil {
		// if there is an error, we will just default to /
		slog.Info("Failed to determine root path for application", "error", err)
		path = "/"
	}

	fullUrl, err := url.JoinPath(deploymentURL, path)
	if err != nil {
		slog.Info("Failed to combine paths", "error", err)
		fullUrl = deploymentURL
	}

	_, err = workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentIsURLLive, fullUrl).Get(ctx)
	if err != nil {
		slog.Error("Health check failed, attempting rollback", "error", err, "url", fullUrl)

		previousDeploy, rollbackErr := workflow.ExecuteActivity[*deployment.DeploymentInfo](ctx, ActivityOpts, AgentGetPreviousDeployment, *spec, Vercel).Get(ctx)
		if rollbackErr != nil {
			slog.Warn("No previous deployment available for rollback", "error", rollbackErr)
			if operationId != "" {
				workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
					"error":              err.Error(),
					"platform":           "vercel",
					"stage":              "url_verification",
					"url":                fullUrl,
					"no_previous_deploy": true,
				}).Get(ctx)
			}
			return deployResult{
				Error: deployError{
					Summary: "Deployment failed health check. This is your first deployment, so there's no previous version to roll back to",
				},
			}, nil
		}

		slog.Info("Found previous deployment for rollback", "deployment_id", previousDeploy.ID, "url", previousDeploy.URL)

		_, rollbackErr = workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentRollbackDeployment, *spec, Vercel, previousDeploy.URL).Get(ctx)
		if rollbackErr != nil {
			slog.Error("Rollback failed", "error", rollbackErr, "target_deployment", previousDeploy.URL)
			if operationId != "" {
				workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
					"error":           err.Error(),
					"platform":        "vercel",
					"stage":           "url_verification",
					"url":             fullUrl,
					"rollback_error":  rollbackErr.Error(),
					"rollback_target": previousDeploy.URL,
				}).Get(ctx)
			}
			return deployResult{}, errors.Errorf("service URL %s is not live and rollback failed: %w", fullUrl, err)
		}

		slog.Info("Rollback completed successfully", "rolled_back_to", previousDeploy.ID)

		if operationId != "" {
			workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "rolled_back", map[string]any{
				"error":              err.Error(),
				"platform":           "vercel",
				"stage":              "url_verification",
				"url":                fullUrl,
				"rolled_back_to":     previousDeploy.ID,
				"rolled_back_to_url": previousDeploy.URL,
			}).Get(ctx)
		}
		return deployResult{
			Error: deployError{
				Summary:   "Deployment failed health check. We've automatically rolled back to your previous working version",
				IsWarning: true,
			},
		}, nil
	}

	// Log deployment success
	if operationId != "" {
		workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "success", map[string]any{
			"url":               fullUrl,
			"platform":          "vercel",
			"resources_created": createdResources,
		}).Get(ctx)
	}

	slog.Info("Vercel deployment workflow completed successfully")
	return deployResult{Url: fullUrl}, nil
}

func (w *Workflows) deployHeroku(ctx workflow.Context, input DeployPlan) (deployResult, error) {
	slog.Info("deployHeroku workflow started", "platform", input.Platform)
	slog.Info("DeployPlan details", "action", input.Action, "source", input.Source, "specName", input.Spec.Name, "specLanguage", input.Spec.Language)

	// Use existing project info from DeployPlan
	existingProject := input.ExistingProjectInfo
	if existingProject.Exists {
		slog.Info("Using existing project from detection", "name", existingProject.Name, "id", existingProject.ProjectID, "databases", existingProject.ExistingDatabases)
	}

	// Log deployment start
	operationId, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentLogDeploymentStart, "heroku", input.Spec, input.Source).Get(ctx)
	if err != nil {
		slog.Error("Failed to log deployment start", "error", err)
		// Continue with deployment even if logging fails
	}

	// Build deployment spec from the plan
	slog.Info("Building deployment spec")
	db := deployment.NewDeploymentBuilder(&input.Spec, input.CollectedEnvVars)
	spec, err := db.Build()
	if err != nil {
		slog.Info("Failed to build deployment spec", "error", err)
		return deployResult{Error: deployError{Summary: fmt.Sprintf("Failed to build deployment spec: %v", err)}}, nil
	}
	slog.Info("Deployment spec built successfully")

	// Add metadata
	spec.Metadata["buildContext"] = input.Source
	spec.Metadata["platform"] = "heroku"

	// Set update mode if existing project detected
	if existingProject.Exists {
		spec.IsUpdate = true
		spec.ExistingProjectID = existingProject.ProjectID
		spec.ExistingDatabases = existingProject.ExistingDatabases
		slog.Info("Set spec for existing project", "ExistingProjectID", spec.ExistingProjectID, "IsUpdate", spec.IsUpdate, "Name", existingProject.Name)
	}

	// Use default Heroku adapter
	herokuAdapter := heroku.NewDefaultHerokuDeploymentAdapter(w.uiWriter)
	d, err := herokuAdapter.GenerateArtifactsWithSource(spec, deployment.StrategyHeroku, input.Source)
	if err != nil {
		slog.Error("Failed to generate Heroku deployment", "error", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     DeployHerokuWorkflowName,
			"activity":     "generate_artifacts", // This is not an activity, it's a local operation
			"component":    "deployment",
			"platform":     "heroku",
			"project_name": input.Spec.Name,
			"language":     input.Spec.Language,
		})
		return deployResult{Error: deployError{Summary: fmt.Sprintf("Failed to generate deployment: %v", err)}}, nil
	}

	// Generate steps for summary
	if qd, ok := d.(*heroku.QueuedDeployment); ok {
		steps := qd.GenerateAPISteps()
		descriptions := make([]string, len(steps))
		for i, step := range steps {
			descriptions[i] = step.GetDescription()
		}
		_, err = workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentSummarizeDeploySteps, descriptions).Get(ctx)
		if err != nil {
			slog.Error("Failed to summarize deployment steps", "error", err)
			prod_error.CaptureErrorWithContext(err, map[string]any{
				"workflow":     DeployHerokuWorkflowName,
				"activity":     AgentSummarizeDeploySteps,
				"component":    "deployment",
				"platform":     "heroku",
				"project_name": input.Spec.Name,
				"language":     input.Spec.Language,
			})
		}
	}

	// Use limited retries for Heroku deployment (it has long-running git operations)
	deployOpts := ActivityOpts
	if input.Platform == Heroku {
		deployOpts.RetryOptions.MaxAttempts = 2 // Only retry once for Heroku
	}

	createdResources, err := workflow.ExecuteActivity[[]deployment.CreatedResource](ctx, deployOpts, AgentDeploySteps, *spec, input.Platform).Get(ctx)
	if err != nil {
		// Send the original error before summarizing
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     DeployHerokuWorkflowName,
			"activity":     AgentDeploySteps,
			"component":    "deployment",
			"platform":     "heroku",
			"project_name": input.Spec.Name,
			"language":     input.Spec.Language,
		})
		summary, e1 := workflow.ExecuteActivity[deployError](ctx, ActivityOpts, AgentSummarizeError, err.Error(), input).Get(ctx)
		if e1 != nil {
			// Send the summarize error
			prod_error.CaptureErrorWithContext(e1, map[string]any{
				"workflow":     DeployHerokuWorkflowName,
				"activity":     AgentSummarizeError,
				"component":    "deployment",
				"platform":     "heroku",
				"project_name": input.Spec.Name,
				"language":     input.Spec.Language,
				"operation":    "summarize_original_error",
			})
			return deployResult{Error: deployError{Summary: err.Error()}}, nil
		}
		slog.Error("Deployment failed", "error", err)
		return deployResult{Error: summary}, nil
	}

	// Extract deployment URL and app name from created resources
	var deploymentURL string
	var appName string
	for _, resource := range createdResources {
		if url, ok := resource.Metadata["url"].(string); ok {
			deploymentURL = url
		}
		if resource.Type == "heroku_app" {
			appName = resource.Name
		}
	}

	if deploymentURL == "" {
		slog.Info("No deployment URL found in created resources")
		deploymentURL = "Deployment completed but URL not available"
	}

	// Store app name in spec for rollback operations (only if not already set from existing project)
	if appName != "" && spec.ExistingProjectID == "" {
		spec.ExistingProjectID = appName
	}

	path, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentDetermineRootPath, input.Spec.Routes).Get(ctx)
	if err != nil {
		// if there is an error, we will just default to /
		slog.Info("Failed to determine root path for application", "error", err)
		path = "/"
	}

	fullUrl, err := url.JoinPath(deploymentURL, path)
	if err != nil {
		slog.Info("Failed to combine paths", "error", err)
		fullUrl = deploymentURL
	}

	_, err = workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentIsURLLive, fullUrl).Get(ctx)
	if err != nil {
		slog.Error("Health check failed, attempting rollback", "error", err, "url", fullUrl)

		previousDeploy, rollbackErr := workflow.ExecuteActivity[*deployment.DeploymentInfo](ctx, ActivityOpts, AgentGetPreviousDeployment, *spec, Heroku).Get(ctx)
		if rollbackErr != nil {
			slog.Warn("No previous deployment available for rollback", "error", rollbackErr)
			if operationId != "" {
				workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
					"error":              err.Error(),
					"platform":           "heroku",
					"stage":              "url_verification",
					"url":                fullUrl,
					"no_previous_deploy": true,
				}).Get(ctx)
			}
			return deployResult{
				Error: deployError{
					Summary: "Deployment failed health check. This is your first deployment, so there's no previous version to roll back to",
				},
			}, nil
		}

		slog.Info("Found previous deployment for rollback", "deployment_id", previousDeploy.ID)

		_, rollbackErr = workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentRollbackDeployment, *spec, Heroku, previousDeploy.ID).Get(ctx)
		if rollbackErr != nil {
			slog.Error("Rollback failed", "error", rollbackErr, "target_deployment", previousDeploy.ID)
			if operationId != "" {
				workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "failed", map[string]any{
					"error":           err.Error(),
					"platform":        "heroku",
					"stage":           "url_verification",
					"url":             fullUrl,
					"rollback_error":  rollbackErr.Error(),
					"rollback_target": previousDeploy.ID,
				}).Get(ctx)
			}
			return deployResult{}, errors.Errorf("service URL %s is not live and rollback failed: %w", fullUrl, err)
		}

		slog.Info("Rollback completed successfully", "rolled_back_to", previousDeploy.ID)

		if operationId != "" {
			workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "rolled_back", map[string]any{
				"error":          err.Error(),
				"platform":       "heroku",
				"stage":          "url_verification",
				"url":            fullUrl,
				"rolled_back_to": previousDeploy.ID,
			}).Get(ctx)
		}
		return deployResult{
			Error: deployError{
				Summary:   "Deployment failed health check. We've automatically rolled back to your previous working version",
				IsWarning: true,
			},
		}, nil
	}

	// Log deployment success
	if operationId != "" {
		workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentUpdateDeploymentStatus, operationId, "success", map[string]any{
			"url":               fullUrl,
			"platform":          "heroku",
			"resources_created": createdResources,
		}).Get(ctx)
	}

	slog.Info("Heroku deployment workflow completed successfully")
	return deployResult{Url: fullUrl}, nil
}

func (w *Workflows) dryRunNetlify(ctx workflow.Context, input DeployPlan) (deployResult, error) {
	slog.Info("dryRunNetlify workflow started", "platform", input.Platform)
	slog.Info("DeployPlan details", "action", input.Action, "source", input.Source, "specName", input.Spec.Name, "specLanguage", input.Spec.Language)

	// TODO: Implement Netlify dry run
	slog.Info("Netlify dry run not yet implemented")
	return deployResult{Error: deployError{Summary: "Netlify dry run not yet implemented"}}, nil
}

// setupJavaScriptProject sets up a JavaScript/Node.js project for deployment
func (w *Workflows) setupJavaScriptProject(ctx workflow.Context, input DeployPlan) (SetupJavaScriptProjectResult, error) {
	slog.Info("setupJavaScriptProject workflow started", "platform", input.Platform)
	slog.Info("DeployPlan details", "action", input.Action, "source", input.Source, "specName", input.Spec.Name, "specLanguage", input.Spec.Language)

	result := SetupJavaScriptProjectResult{}

	// Step 1: Update JavaScript project configuration (Svelte config + package.json)
	slog.Info("Updating JavaScript project configuration")
	jsConfig, err := workflow.ExecuteActivity[JavaScriptConfigResult](ctx, ActivityOpts, AgentUpdateJavaScriptConfig, input).Get(ctx)
	if err != nil {
		slog.Error("Failed to update JavaScript configuration", "error", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     SetupJavaScriptProjectWorkflowName,
			"activity":     AgentUpdateJavaScriptConfig,
			"component":    "javascript_config",
			"platform":     input.Platform.String(),
			"project_name": input.Spec.Name,
			"language":     input.Spec.Language,
		})
		summary, e1 := workflow.ExecuteActivity[deployError](ctx, ActivityOpts, AgentSummarizeError, err.Error(), input).Get(ctx)
		if e1 != nil {
			// Send the summarize error
			prod_error.CaptureErrorWithContext(e1, map[string]any{
				"workflow":     SetupJavaScriptProjectWorkflowName,
				"activity":     AgentSummarizeError,
				"component":    "javascript_config",
				"platform":     input.Platform.String(),
				"project_name": input.Spec.Name,
				"language":     input.Spec.Language,
				"operation":    "summarize_original_error",
			})
			slog.Error("Failed to summarize JavaScript config error", "error", e1)
			return SetupJavaScriptProjectResult{Error: deployError{Summary: err.Error()}}, nil
		}
		return SetupJavaScriptProjectResult{Error: summary}, nil
	}

	// Update result with configuration changes
	if len(jsConfig.ConfigDiff) > 0 {
		result.ConfigUpdated = true
		result.ConfigDiff = jsConfig.ConfigDiff
		result.ConfigPath = jsConfig.ConfigPath
		slog.Info("JavaScript configuration updated")
	} else {
		slog.Info("No JavaScript configuration found or no changes needed")
	}

	if jsConfig.PackageJsonUpdated {
		result.PackageJsonUpdated = true
		result.PackageJsonDiff = jsConfig.PackageJsonDiff
		slog.Info("Package.json configuration updated")
	} else {
		slog.Info("No package.json changes needed")
	}

	// Step 2: Create/update package-lock.json (after config changes)
	slog.Info("Creating/updating package-lock.json")
	configUpdated := result.ConfigUpdated || result.PackageJsonUpdated
	_, err = workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentCreatePackageLock, input, configUpdated).Get(ctx)
	if err != nil {
		slog.Error("Failed to create package-lock.json", "error", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     SetupJavaScriptProjectWorkflowName,
			"activity":     AgentCreatePackageLock,
			"component":    "javascript_config",
			"platform":     input.Platform.String(),
			"project_name": input.Spec.Name,
			"language":     input.Spec.Language,
		})
		summary, e1 := workflow.ExecuteActivity[deployError](ctx, ActivityOpts, AgentSummarizeError, err.Error(), input).Get(ctx)
		if e1 != nil {
			// Send the summarize error
			prod_error.CaptureErrorWithContext(e1, map[string]any{
				"workflow":     SetupJavaScriptProjectWorkflowName,
				"activity":     AgentSummarizeError,
				"component":    "javascript_config",
				"platform":     input.Platform.String(),
				"project_name": input.Spec.Name,
				"language":     input.Spec.Language,
				"operation":    "summarize_original_error",
			})
			slog.Error("Failed to summarize package-lock error", "error", e1)
			return SetupJavaScriptProjectResult{Error: deployError{Summary: err.Error()}}, nil
		}
		return SetupJavaScriptProjectResult{Error: summary}, nil
	}
	result.PackageLockCreated = true
	slog.Info("Package-lock.json handling completed")

	plan, err := workflow.ExecuteActivity[DeployPlan](ctx, ActivityOpts, AgentPrepareDeployment, input).Get(ctx)
	if err != nil {
		slog.Error("Failed to prepare deployment", "error", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     SetupJavaScriptProjectWorkflowName,
			"activity":     AgentPrepareDeployment,
			"component":    "javascript_config",
			"platform":     input.Platform.String(),
			"project_name": input.Spec.Name,
			"language":     input.Spec.Language,
		})
	}
	result.UpdatedPlan = plan
	slog.Info("JavaScript project setup completed successfully")
	return result, nil
}

func (w *Workflows) rollbackDeployment(ctx workflow.Context, plan DeployPlan) (deployResult, error) {
	workflow.Logger(ctx).Info("starting rollback workflow", "platform", plan.Platform, "project", plan.Spec.Name)

	// For platforms that need existing project info, we need to detect it first if not already available
	// or if the existing info is from a different platform (multi-platform case)
	existingProject := plan.ExistingProjectInfo
	if !existingProject.Exists || existingProject.ProjectID == "" || existingProject.Platform != plan.Platform {
		workflow.Logger(ctx).Info("detecting existing project for rollback", "platform", plan.Platform)
		detectedProject, err := workflow.ExecuteActivity[ExistingProjectInfo](ctx, ActivityOpts, AgentCheckExistingProject, plan.Platform, plan.Spec.Name, plan.Source).Get(ctx)
		if err != nil {
			workflow.Logger(ctx).Error("Failed to detect existing project", "error", err)
			prod_error.CaptureErrorWithContext(err, map[string]any{
				"workflow":     RollbackDeploymentWorkflowName,
				"activity":     AgentCheckExistingProject,
				"component":    "workflow",
				"platform":     plan.Platform.String(),
				"project_name": plan.Spec.Name,
			})
			return deployResult{
				Error: deployError{
					Summary: "Could not find existing deployment to rollback. Please make sure the application is deployed.",
				},
			}, nil
		}

		if !detectedProject.Exists {
			workflow.Logger(ctx).Warn("No existing deployment found for rollback")
			return deployResult{
				Error: deployError{
					Summary: "No existing deployment found to rollback. Please make sure the application is deployed.",
				},
			}, nil
		}

		existingProject = detectedProject
	}

	// Build deployment spec from the plan
	workflow.Logger(ctx).Info("Building deployment spec for rollback")
	db := deployment.NewDeploymentBuilder(&plan.Spec, plan.CollectedEnvVars)
	spec, err := db.Build()
	if err != nil {
		workflow.Logger(ctx).Error("Failed to build deployment spec", "error", err)
		return deployResult{Error: deployError{Summary: fmt.Sprintf("Failed to build deployment spec: %v", err)}}, nil
	}

	// Set existing project info
	spec.IsUpdate = true
	spec.ExistingProjectID = existingProject.ProjectID
	spec.ExistingDatabases = existingProject.ExistingDatabases

	// Get the previous deployment to rollback to
	previousDeploy, err := workflow.ExecuteActivity[*deployment.DeploymentInfo](ctx, ActivityOpts, AgentGetPreviousDeployment, *spec, plan.Platform).Get(ctx)
	if err != nil {
		workflow.Logger(ctx).Error("Failed to get previous deployment", "error", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     RollbackDeploymentWorkflowName,
			"activity":     AgentGetPreviousDeployment,
			"component":    "workflow",
			"platform":     plan.Platform.String(),
			"project_name": spec.Name,
		})
		return deployResult{
			Error: deployError{
				Summary: "No previous deployment found to rollback to. This might be your first deployment.",
			},
		}, nil
	}

	if previousDeploy == nil {
		workflow.Logger(ctx).Warn("No previous deployment available for rollback")
		return deployResult{
			Error: deployError{
				Summary: "No previous deployment found to rollback to. This might be your first deployment.",
			},
		}, nil
	}

	workflow.Logger(ctx).Info("Found previous deployment", "deployment_id", previousDeploy.ID)

	// Execute the rollback
	_, err = workflow.ExecuteActivity[any](ctx, ActivityOpts, AgentRollbackDeployment, *spec, plan.Platform, previousDeploy.ID).Get(ctx)
	if err != nil {
		workflow.Logger(ctx).Error("Failed to rollback deployment", "error", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":          RollbackDeploymentWorkflowName,
			"activity":          AgentRollbackDeployment,
			"component":         "workflow",
			"platform":          plan.Platform.String(),
			"project_name":      spec.Name,
			"target_deployment": previousDeploy.ID,
		})
		return deployResult{
			Error: deployError{
				Summary: fmt.Sprintf("Failed to rollback deployment: %v", err),
			},
		}, nil
	}

	workflow.Logger(ctx).Info("Rollback completed successfully")

	return deployResult{
		Url: previousDeploy.URL,
	}, nil
}
