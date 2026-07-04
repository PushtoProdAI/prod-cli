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
	"github.com/pushtoprodai/prod-cli/internal/backend"
	"github.com/pushtoprodai/prod-cli/internal/deployment"
	"github.com/pushtoprodai/prod-cli/internal/deployment/flyio"
	"github.com/pushtoprodai/prod-cli/internal/deployment/render"
	prod_error "github.com/pushtoprodai/prod-cli/internal/error"
	"github.com/pushtoprodai/prod-cli/internal/history"
	"github.com/pushtoprodai/prod-cli/internal/llm"
	"github.com/pushtoprodai/prod-cli/internal/output"
	"github.com/pushtoprodai/prod-cli/internal/workflowext"
)

const (
	PlanDeployWorkflowName             = "agent.planDeploy"
	DeployRenderWorkflowName           = "agent.deploy.render"
	DeployFlyioWorkflowName            = "agent.deploy.flyio"
	CategorizeEnvVarsWorkflowName      = "agent.categorizeEnvVars"
	DetectExistingWorkflowName         = "agent.detectExisting"
	DeployNetlifyWorkflowName          = "agent.deploy.netlify"
	SetupJavaScriptProjectWorkflowName = "agent.setupJavaScriptProject"
	SetupPythonProjectWorkflowName     = "agent.setupPythonProject"
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

// ConfigChange represents a single configuration file modification
type ConfigChange struct {
	Name      string     `json:"name"`      // Display name (e.g., "Package.json", ".python-version", "svelte.config.js")
	Path      string     `json:"path"`      // File path for reference
	Diff      []DiffLine `json:"diff"`      // The actual diff to display
	Icon      string     `json:"icon"`      // Optional emoji/icon for display (e.g., "📦", "🐍", "⚙️")
	ExtraInfo string     `json:"extraInfo"` // Additional context (e.g., "Added WhiteNoise for static files")
}

// SetupProjectResult is a language-agnostic result for project setup workflows
type SetupProjectResult struct {
	ConfigChanges []ConfigChange    `json:"configChanges,omitempty"` // All configuration files that were modified
	EnvVars       map[string]string `json:"envVars,omitempty"`       // Environment variables to be set (framework-specific)
	Error         deployError       `json:"error"`                   // Any errors encountered
	UpdatedPlan   DeployPlan        `json:"updatedPlan"`             // The updated deployment plan
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
	frameworkRegistry := NewFrameworkRegistry()
	// Local deployment history (used in local mode). If it can't be created we
	// still deploy — history just isn't recorded — so failure is non-fatal.
	histStore, err := history.NewStore()
	if err != nil {
		slog.Warn("failed to initialize local history store; deploy history will not be recorded", "error", err)
	}
	return &Workflows{
		Acts: &Activities{
			renderClient:      renderClient,
			flyClient:         flyClient,
			beClient:          beClient,
			uiWriter:          uiWriter,
			llmClient:         llmClient,
			frameworkRegistry: frameworkRegistry,
			history:           histStore,
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
			errs = errors.Join(
				errs,
				errors.Errorf("failed to register agent workflow %q: %w", wf.Name, err),
			)
		}
	}

	for _, act := range w.Acts.Activities() {
		if err := act.Register(registry); err != nil {
			errs = errors.Join(
				errs,
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
		{Name: DeployFlyioWorkflowName, WorkflowFunc: w.deployFly},
		{Name: CategorizeEnvVarsWorkflowName, WorkflowFunc: w.categorizeEnvVars},
		{Name: DetectExistingWorkflowName, WorkflowFunc: w.detectExistingWorkflow},
		{Name: DeployNetlifyWorkflowName, WorkflowFunc: w.deployNetlify},
		{Name: SetupJavaScriptProjectWorkflowName, WorkflowFunc: w.setupJavaScriptProject},
		{Name: SetupPythonProjectWorkflowName, WorkflowFunc: w.setupPythonProject},
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

func (Workflows) SetupPythonProject(ctx context.Context, c *client.Client, input DeployPlan) (*workflow.Instance, error) {
	return c.CreateWorkflowInstance(ctx, client.WorkflowInstanceOptions{
		InstanceID: fmt.Sprintf("%s.%d", SetupPythonProjectWorkflowName, time.Now().Unix()),
	}, SetupPythonProjectWorkflowName, input)
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

	// Merge .env file values as suggested defaults
	// Note: Framework handlers will override these later in PrepareDeployment
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

func (w *Workflows) setupJavaScriptProject(ctx workflow.Context, input DeployPlan) (SetupProjectResult, error) {
	slog.Info("setupJavaScriptProject workflow started", "platform", input.Platform)
	slog.Info("DeployPlan details", "action", input.Action, "source", input.Source, "specName", input.Spec.Name, "specLanguage", input.Spec.Language)

	result := SetupProjectResult{}

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
			return SetupProjectResult{Error: deployError{Summary: err.Error()}}, nil
		}
		return SetupProjectResult{Error: summary}, nil
	}

	// Add configuration changes to result
	if len(jsConfig.ConfigDiff) > 0 {
		result.ConfigChanges = append(result.ConfigChanges, ConfigChange{
			Name: jsConfig.ConfigPath,
			Path: jsConfig.ConfigPath,
			Diff: jsConfig.ConfigDiff,
			Icon: "⚙️",
		})
		slog.Info("JavaScript configuration updated")
	} else {
		slog.Info("No JavaScript configuration found or no changes needed")
	}

	if jsConfig.PackageJsonUpdated && len(jsConfig.PackageJsonDiff) > 0 {
		result.ConfigChanges = append(result.ConfigChanges, ConfigChange{
			Name: "Package.json",
			Path: "package.json",
			Diff: jsConfig.PackageJsonDiff,
			Icon: "📦",
		})
		slog.Info("Package.json configuration updated")
	} else {
		slog.Info("No package.json changes needed")
	}

	// Step 2: Create/update package-lock.json (after config changes)
	slog.Info("Creating/updating package-lock.json")
	configUpdated := len(jsConfig.ConfigDiff) > 0 || jsConfig.PackageJsonUpdated
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
			return SetupProjectResult{Error: deployError{Summary: err.Error()}}, nil
		}
		return SetupProjectResult{Error: summary}, nil
	}
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

func (w *Workflows) setupPythonProject(ctx workflow.Context, input DeployPlan) (SetupProjectResult, error) {
	slog.Info("setupPythonProject workflow started", "platform", input.Platform)
	slog.Info("DeployPlan details", "action", input.Action, "source", input.Source, "specName", input.Spec.Name, "specLanguage", input.Spec.Language)

	result := SetupProjectResult{}

	// Step 1: Generate .python-version file
	slog.Info("Generating .python-version file")
	pyConfig, err := workflow.ExecuteActivity[PythonConfigResult](ctx, ActivityOpts, AgentGeneratePythonVersion, input).Get(ctx)
	if err != nil {
		slog.Error("Failed to generate Python version file", "error", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     SetupPythonProjectWorkflowName,
			"activity":     AgentGeneratePythonVersion,
			"component":    "python_config",
			"platform":     input.Platform.String(),
			"project_name": input.Spec.Name,
			"language":     input.Spec.Language,
		})
		summary, e1 := workflow.ExecuteActivity[deployError](ctx, ActivityOpts, AgentSummarizeError, err.Error(), input).Get(ctx)
		if e1 != nil {
			// Send the summarize error
			prod_error.CaptureErrorWithContext(e1, map[string]any{
				"workflow":     SetupPythonProjectWorkflowName,
				"activity":     AgentSummarizeError,
				"component":    "python_config",
				"platform":     input.Platform.String(),
				"project_name": input.Spec.Name,
				"language":     input.Spec.Language,
				"operation":    "summarize_original_error",
			})
			slog.Error("Failed to summarize Python version error", "error", e1)
			return SetupProjectResult{Error: deployError{Summary: err.Error()}}, nil
		}
		return SetupProjectResult{Error: summary}, nil
	}

	// Add Python version changes to result
	if pyConfig.PythonVersionCreated && len(pyConfig.PythonVersionDiff) > 0 {
		result.ConfigChanges = append(result.ConfigChanges, ConfigChange{
			Name: ".python-version",
			Path: ".python-version",
			Diff: pyConfig.PythonVersionDiff,
			Icon: "🐍",
		})
		slog.Info("Python version file created/updated")
	} else {
		slog.Info("No Python version file changes needed")
	}

	// Step 2: Configure framework-specific settings (Django, Flask, FastAPI, etc.)
	slog.Info("Checking for framework-specific configuration")
	frameworkConfig, err := workflow.ExecuteActivity[PythonConfigResult](ctx, ActivityOpts, AgentConfigurePythonFramework, input).Get(ctx)
	if err != nil {
		slog.Error("Framework configuration failed", "error", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     SetupPythonProjectWorkflowName,
			"activity":     AgentConfigurePythonFramework,
			"component":    "python_config",
			"platform":     input.Platform.String(),
			"project_name": input.Spec.Name,
			"language":     input.Spec.Language,
		})
	} else {
		// Add framework configuration changes to result
		if frameworkConfig.FrameworkConfigUpdated && len(frameworkConfig.FrameworkConfigDiff) > 0 {
			result.ConfigChanges = append(result.ConfigChanges, ConfigChange{
				Name: fmt.Sprintf("Framework %s", frameworkConfig.FrameworkConfigPath),
				Path: frameworkConfig.FrameworkConfigPath,
				Diff: frameworkConfig.FrameworkConfigDiff,
				Icon: "⚙️",
			})
			slog.Info("Framework configuration updated", "path", frameworkConfig.FrameworkConfigPath)
		}
		if len(frameworkConfig.FrameworkEnvVars) > 0 {
			result.EnvVars = frameworkConfig.FrameworkEnvVars
			slog.Info("Framework environment variables prepared", "count", len(frameworkConfig.FrameworkEnvVars))
		}
	}

	// Step 2b: Setup production server for Python frameworks
	slog.Info("Setting up production server for Python framework")
	serverConfig, err := workflow.ExecuteActivity[PythonConfigResult](ctx, ActivityOpts, AgentSetupPythonServer, input).Get(ctx)
	if err != nil {
		slog.Error("Python server setup failed", "error", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     SetupPythonProjectWorkflowName,
			"activity":     AgentSetupPythonServer,
			"component":    "python_config",
			"platform":     input.Platform.String(),
			"project_name": input.Spec.Name,
			"language":     input.Spec.Language,
		})
	} else {
		// Update result with server configuration
		if serverConfig.FrameworkRunCommand != "" {
			slog.Info("Python server configured", "command", serverConfig.FrameworkRunCommand, "serverAdded", serverConfig.ServerAdded)

			// Always update the plan's StartCommand with the production-ready command
			// This overrides any bad suggestions like "python manage.py runserver"
			input.Spec.StartCommand = serverConfig.FrameworkRunCommand
			slog.Info("Updated StartCommand to production server", "command", serverConfig.FrameworkRunCommand)
		}
	}

	// Step 2c: Configure static files for Python frameworks (Django)
	slog.Info("Configuring static files for Python framework")
	staticConfig, err := workflow.ExecuteActivity[PythonConfigResult](ctx, ActivityOpts, AgentConfigurePythonStaticFiles, input).Get(ctx)
	if err != nil {
		slog.Error("Python static files setup failed", "error", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     SetupPythonProjectWorkflowName,
			"activity":     AgentConfigurePythonStaticFiles,
			"component":    "python_config",
			"platform":     input.Platform.String(),
			"project_name": input.Spec.Name,
			"language":     input.Spec.Language,
		})
	} else {
		// Add static files configuration changes to result
		if staticConfig.StaticFilesConfigured && len(staticConfig.StaticFilesDiff) > 0 {
			extraInfo := ""
			if staticConfig.WhiteNoiseAdded {
				extraInfo = "✅ Added WhiteNoise for static file serving"
			}
			result.ConfigChanges = append(result.ConfigChanges, ConfigChange{
				Name:      "Static files configuration",
				Path:      "",
				Diff:      staticConfig.StaticFilesDiff,
				Icon:      "📦",
				ExtraInfo: extraInfo,
			})
			slog.Info("Static files configured", "whiteNoiseAdded", staticConfig.WhiteNoiseAdded)
		}
	}

	// Step 3: Prepare deployment (framework-specific adjustments)
	plan, err := workflow.ExecuteActivity[DeployPlan](ctx, ActivityOpts, AgentPrepareDeployment, input).Get(ctx)
	if err != nil {
		slog.Error("Failed to prepare deployment", "error", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     SetupPythonProjectWorkflowName,
			"activity":     AgentPrepareDeployment,
			"component":    "python_config",
			"platform":     input.Platform.String(),
			"project_name": input.Spec.Name,
			"language":     input.Spec.Language,
		})
	}
	result.UpdatedPlan = plan
	slog.Info("Python project setup completed successfully")
	return result, nil
}
