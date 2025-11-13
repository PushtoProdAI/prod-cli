package agent

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"sync"
	"time"

	"github.com/cschleiden/go-workflows/client"
	"github.com/cschleiden/go-workflows/workflow"
	"github.com/go-errors/errors"
	"github.com/meroxa/prod/cli/internal/backend"
	"github.com/meroxa/prod/cli/internal/deployment"
	"github.com/meroxa/prod/cli/internal/deployment/flyio"
	"github.com/meroxa/prod/cli/internal/deployment/render"
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
	DeployAWSWorkflowName              = "agent.deploy.aws"
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
	llmClient    llm.Client
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
	llmClient := newAgentLLMClient()
	return &Workflows{
		Acts: &Activities{
			renderClient: renderClient,
			flyClient:    flyClient,
			beClient:     beClient,
			uiWriter:     uiWriter,
			llmClient:    llmClient,
		},
		renderClient: renderClient,
		flyClient:    flyClient,
		uiWriter:     uiWriter,
		llmClient:    llmClient,
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
		{Name: DeployAWSWorkflowName, WorkflowFunc: w.deployAWS},
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
	case AWS:
		return c.CreateWorkflowInstance(ctx, client.WorkflowInstanceOptions{InstanceID: fmt.Sprintf("%s.%d", DeployAWSWorkflowName, time.Now().Unix())}, DeployAWSWorkflowName, input)
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

// Shared workflow implementations

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

// validateDeploymentSpec validates a deployment specification
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
