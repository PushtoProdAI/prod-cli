package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cschleiden/go-workflows/workflow"
	"github.com/go-errors/errors"
	"github.com/google/uuid"
	"github.com/pushtoprodai/prod-cli/baml_client/types"
	"github.com/pushtoprodai/prod-cli/internal/analyzer"
	"github.com/pushtoprodai/prod-cli/internal/config"
	"github.com/pushtoprodai/prod-cli/internal/deployment"
	prod_error "github.com/pushtoprodai/prod-cli/internal/error"
	"github.com/pushtoprodai/prod-cli/internal/history"
)

// planDeploy workflow handles the planning phase of deployment
func (w *Workflows) planDeploy(ctx workflow.Context, input string) (DeployPlan, error) {
	intent, err := workflow.ExecuteActivity[types.Intent](ctx, ActivityOpts, AgentDetermineIntent, input).Get(ctx)
	if err != nil {
		slog.Error("Failed to determine intent", "error", err)
		w.uiWriter.SendStatus("error", "Failed to determine intent")
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":  PlanDeployWorkflowName,
			"activity":  AgentDetermineIntent,
			"component": "workflow",
		})
	}
	spec := analyzer.ProjectSpec{}
	if intent.Source != "" {
		opts := ActivityOpts
		opts.RetryOptions.MaxAttempts = 3
		opts.RetryOptions.FirstRetryInterval = time.Second * 2
		w.uiWriter.SendStatus("analyzing", "Analyzing project...")
		spec, err = workflow.ExecuteActivity[analyzer.ProjectSpec](ctx, opts, AgentAnalyzeProject, intent).Get(ctx)
		if err != nil {
			w.uiWriter.SendStatusComplete("analyzing", "❌ Failed to analyze project")
			slog.Error("Failed to analyze project", "error", err)
			prod_error.CaptureErrorWithContext(err, map[string]any{
				"workflow":  PlanDeployWorkflowName,
				"activity":  AgentAnalyzeProject,
				"component": "workflow",
				"platform":  intent.Platform,
			})
		} else {
			w.uiWriter.SendStatusComplete("analyzing", "✅ Project analyzed")
		}
	}

	action := UnknownAction
	switch strings.ToLower(intent.Action) {
	case "deploy":
		action = Deploy
	case "rollback":
		action = Rollback
	case "delete", "destroy":
		action = Destroy
	default:
		action = UnknownAction
	}

	platform := UnknownPlatform
	if p, ok := PlatformByString(intent.Platform); ok {
		platform = p
	}

	var existingProjectInfo ExistingProjectInfo

	if (action == Rollback || action == Destroy) && platform == UnknownPlatform {
		// Rollback and destroy both target an existing deployment: auto-detect where
		// it's deployed if the user didn't name a platform.
		w.uiWriter.SendStatus("detecting", "Detecting deployment platforms...")

		existingProject, err := workflow.ExecuteActivity[ExistingProjectInfo](ctx, ActivityOpts, AgentDetectPlatformsForRollback, spec.Name, intent.Source).Get(ctx)
		if err != nil {
			slog.Error("Failed to detect platforms for rollback", "error", err)
			prod_error.CaptureErrorWithContext(err, map[string]any{
				"workflow":     PlanDeployWorkflowName,
				"activity":     AgentDetectPlatformsForRollback,
				"component":    "workflow",
				"project_name": spec.Name,
			})
			w.uiWriter.SendStatusComplete("detecting", "❌ Failed to detect platforms")
		} else {
			existingProjectInfo = existingProject
			if len(existingProject.DetectedPlatforms) == 0 {
				w.uiWriter.SendStatusComplete("detecting", "❌ No deployments found on any platform")
			} else if len(existingProject.DetectedPlatforms) == 1 {
				platform = existingProject.DetectedPlatforms[0]
				intent.Platform = platform.DisplayName()
				w.uiWriter.SendStatusComplete("detecting", fmt.Sprintf("✅ Found deployment on %s", platform.DisplayName()))
			} else {
				platformNames := make([]string, len(existingProject.DetectedPlatforms))
				for i, p := range existingProject.DetectedPlatforms {
					platformNames[i] = p.String()
				}
				w.uiWriter.SendStatusComplete("detecting", fmt.Sprintf("⚠️ Found deployments on multiple platforms: %s", strings.Join(platformNames, ", ")))
			}
		}
	} else if (action == Rollback || action == Destroy) && platform != UnknownPlatform {
		// Platform was specified in the prompt, use it directly
		slog.Info("Using platform from prompt", "action", action, "platform", platform)
	}

	opts := ActivityOpts
	opts.RetryOptions.MaxAttempts = 2
	_, err = workflow.ExecuteActivity[any](ctx, opts, AgentSendProjectStats, intent.Platform, spec).Get(ctx)
	if err != nil {
		slog.Error("Failed to send project stats", "error", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":  PlanDeployWorkflowName,
			"activity":  AgentSendProjectStats,
			"component": "workflow",
			"platform":  intent.Platform,
		})
	}

	// Prepare detected platforms list for summary
	// If platform came from prompt (not auto-detected), use that; otherwise use detected platforms
	var detectedPlatformNames []string
	if (action == Rollback || action == Destroy) && platform != UnknownPlatform && len(existingProjectInfo.DetectedPlatforms) == 0 {
		// Platform was specified in prompt, not auto-detected
		detectedPlatformNames = []string{platform.DisplayName()}
	} else {
		// Use auto-detected platforms
		detectedPlatformNames = make([]string, len(existingProjectInfo.DetectedPlatforms))
		for i, p := range existingProjectInfo.DetectedPlatforms {
			detectedPlatformNames[i] = p.String()
		}
	}

	summary, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentSummarize, intent, spec.Name, spec.Language, detectedPlatformNames).Get(ctx)
	if err != nil {
		slog.Error("Failed to summarize intent", "error", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     PlanDeployWorkflowName,
			"activity":     AgentSummarize,
			"component":    "workflow",
			"platform":     intent.Platform,
			"project_name": spec.Name,
			"language":     spec.Language,
		})
	}

	// Deploy shape: the LLM classifies it from the request ("deploy this mcp server", "my
	// worker", "every night"), but a conclusive code signal from the analyzer (an MCP SDK,
	// an agent framework with no web server) WINS — so a project mislabeled "web" doesn't
	// get HTTP liveness applied to a worker, and an MCP server gets the initialize handshake.
	// (ACD.4 / DetectAgentShape.) Platform/intent still come from the LLM.
	shape := deployment.ParseShape(intent.DeployShape)
	if spec.DetectedShape != "" {
		if detected := deployment.ParseShape(spec.DetectedShape); detected != shape {
			slog.Info("analyzer overrode deploy shape from code", "llm_shape", shape, "detected_shape", detected)
			shape = detected
		}
	}

	// A cron deploy only actually runs on a schedule where the platform can express one, and
	// prod must never SHOW a schedule it won't honor: on Fly/AWS/etc. a cron shape is a
	// continuous portless process, so claiming "Schedule: 0 2 * * *" and then running 24/7
	// would be a lie. So gate by platform capability:
	//   - Render supplies the schedule to a cron_job — require a valid expression, else degrade.
	//   - Modal deploys the user's own app code, whose schedule lives in the function decorator,
	//     so prod neither supplies nor validates one (the shape just skips HTTP liveness).
	//   - Any other platform can't run a scheduled job through prod → degrade to a continuous
	//     worker with an honest message, and carry no schedule.
	schedule := ""
	if shape == deployment.ShapeCron {
		switch {
		case platform == Modal:
			w.uiWriter.SendStatus("planning", "ℹ️ On Modal, set the schedule in your function decorator, e.g. @app.function(schedule=modal.Cron(\"0 2 * * *\")).")
		case platform == Render:
			if deployment.IsValidCron(intent.Schedule) {
				schedule = intent.Schedule
			} else {
				slog.Info("cron requested without a parseable schedule; deploying as a worker", "raw", intent.Schedule)
				shape = deployment.ShapeWorker
				w.uiWriter.SendStatus("planning", "ℹ️ No schedule detected — deploying as a continuous worker. To schedule it, say e.g. \"every night at 2am\".")
			}
		default:
			slog.Info("platform can't run scheduled jobs; deploying cron as a continuous worker", "platform", platform.String())
			shape = deployment.ShapeWorker
			w.uiWriter.SendStatus("planning", fmt.Sprintf("ℹ️ %s can't run scheduled jobs through prod — deploying as a continuous worker. (Render supports real cron jobs.)", platform.DisplayName()))
		}
	}

	plan := DeployPlan{
		Action:              action,
		Platform:            platform,
		Source:              intent.Source,
		Spec:                spec,
		Summary:             summary,
		ExistingProjectInfo: existingProjectInfo,
		Shape:               shape,
		Schedule:            schedule,
	}
	// Pin a plugin plan to its plugin by name, so a resumed workflow validates it
	// resolves to the same plugin (a plugin's Platform value is a hash of its name).
	if IsPlugin(platform) {
		plan.PluginName = platform.DisplayName()
	}

	// Estimate costs during planning phase
	if action == Deploy && platform != UnknownPlatform {

		if plan.Spec.StartCommand == "" {
			cmd, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentDetermineRunCommand, spec).Get(ctx)
			if err != nil {
				slog.Info("Failed to determine run command", "error", err)
				prod_error.CaptureErrorWithContext(err, map[string]any{
					"workflow":     PlanDeployWorkflowName,
					"activity":     AgentDetermineRunCommand,
					"component":    "workflow",
					"platform":     platform.String(),
					"project_name": spec.Name,
					"language":     spec.Language,
				})
			}
			if cmd != "" {
				plan.Spec.StartCommand = cmd
			}
		}

		// Determine migration command if databases are present
		hasDatabases := false
		for _, req := range spec.ServiceRequirements {
			if req.Type == analyzer.TypeDatabase {
				hasDatabases = true
				break
			}
		}

		if hasDatabases && plan.Spec.MigrationCommand == "" {
			migrationCmd, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentDetermineMigrationCommand, spec).Get(ctx)
			if err != nil {
				slog.Info("Failed to determine migration command", "error", err)
			}
			if migrationCmd != "" {
				plan.Spec.MigrationCommand = migrationCmd
			}
		}

		w.uiWriter.SendStatus("pricing", "Calculating estimated costs...")

		// Build deployment spec for cost estimation
		db := deployment.NewDeploymentBuilder(&spec, []deployment.EnvVar{}, plan.Shape)
		deploymentSpec, err := db.Build()
		if err != nil {
			slog.Info("Failed to build deployment spec for cost estimation", "error", err)
		} else {
			// Add auth token to metadata for AWS pricing (follows same pattern as deployment workflows)
			session := CtxWorkflowSession(ctx)
			if session != nil && session.AccessToken != "" {
				deploymentSpec.Metadata["authToken"] = session.AccessToken
			}

			// Estimate costs based on platform
			var estimatedCosts deployment.CostEstimate
			var activity string
			switch platform {
			case Render:
				estimatedCosts, err = workflow.ExecuteActivity[deployment.CostEstimate](ctx, ActivityOpts, AgentEstimateRenderCosts, *deploymentSpec, deployment.StrategyRenderQueued).Get(ctx)
				activity = AgentEstimateRenderCosts
			case FlyIO:
				estimatedCosts, err = workflow.ExecuteActivity[deployment.CostEstimate](ctx, ActivityOpts, AgentEstimateFlyioCosts, *deploymentSpec, deployment.StrategyFlyio).Get(ctx)
				activity = AgentEstimateFlyioCosts
			case Netlify:
				estimatedCosts, err = workflow.ExecuteActivity[deployment.CostEstimate](ctx, ActivityOpts, AgentEstimateNetlifyCosts, *deploymentSpec, deployment.StrategyNetlify).Get(ctx)
				activity = AgentEstimateNetlifyCosts
			case Vercel:
				estimatedCosts, err = workflow.ExecuteActivity[deployment.CostEstimate](ctx, ActivityOpts, AgentEstimateVercelCosts, *deploymentSpec, deployment.StrategyVercel).Get(ctx)
				activity = AgentEstimateVercelCosts
			case Heroku:
				estimatedCosts, err = workflow.ExecuteActivity[deployment.CostEstimate](ctx, ActivityOpts, AgentEstimateHerokuCosts, *deploymentSpec, deployment.StrategyHeroku).Get(ctx)
				activity = AgentEstimateHerokuCosts
			default:
				// Managed-container clouds (App Runner, Cloud Run, Azure Container
				// Apps) share one rough estimate.
				if spec, ok := LookupPlatform(platform); ok && spec.ManagedContainer {
					estimatedCosts, err = workflow.ExecuteActivity[deployment.CostEstimate](ctx, ActivityOpts, AgentEstimateContainerCosts, *deploymentSpec, platform).Get(ctx)
					activity = AgentEstimateContainerCosts
				}
			}

			if err != nil {
				slog.Info("Failed to estimate costs", "error", err)
				prod_error.CaptureErrorWithContext(err, map[string]any{
					"workflow":     PlanDeployWorkflowName,
					"activity":     activity,
					"component":    "workflow",
					"platform":     platform.String(),
					"project_name": spec.Name,
					"language":     spec.Language,
				})
			} else {
				plan.Pricing = estimatedCosts
				w.uiWriter.SendStatusComplete("pricing", "✅ Costs calculated")
			}
		}
	}

	return plan, err
}

func (a *Activities) determineIntent(ctx context.Context, prompt string) (types.Intent, error) {
	a.uiWriter.SendStatus("planning", "Understanding your request...")

	intent, err := a.llmClient.ExtractIntent(ctx, prompt)
	if err != nil {
		a.uiWriter.SendStatusComplete("planning", "❌ Failed to understand request")
		return types.Intent{}, errors.Errorf("failed to extract intent: %w", err)
	}

	if intent.Source == "pwd" {
		path, err := os.Getwd()
		if err != nil {
			intent.Source = ""
			slog.Info("failed to get current working directory", "error", err)
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
	if err != nil {
		return analyzer.ProjectSpec{}, err
	}
	return *spec, nil
}

func (a *Activities) summarize(ctx context.Context, intent types.Intent, name string, language string, detectedPlatforms []string) (string, error) {
	a.uiWriter.SendStatus("summarizing", "Summarizing your request...")
	summary, err := a.llmClient.SummarizeIntent(ctx, intent, name, language, detectedPlatforms)
	if err != nil {
		a.uiWriter.SendStatusComplete("summarizing", "❌ Failed to summarize request")
		return "", errors.Errorf("failed to summarize intent: %w", err)
	}
	a.uiWriter.SendStatusComplete("summarizing", "✅ Request summarized")
	return summary.Summary, nil
}

func (a *Activities) sendProjectStats(ctx context.Context, platform string, spec analyzer.ProjectSpec) error {
	// Usage analytics is a managed-backend concern. In local mode there's nothing
	// to report to, so this is a no-op.
	if !config.BackendConfigured() {
		return nil
	}
	session := CtxSession(ctx)
	if session == nil {
		return workflow.NewPermanentError(errors.New("no session found in context"))
	}
	err := a.beClient.RecordRequestedStack(ctx, session.AccessToken, platform, spec.Language, spec.ServiceRequirements)
	if err != nil {
		return errors.Errorf("failed to record project stats: %w", err)
	}
	return nil
}

func (a *Activities) logDeploymentStart(ctx context.Context, platform string, spec analyzer.ProjectSpec, source string, action Action) (string, error) {
	// Map Action to operation_type string
	operationType := "deploy"
	switch action {
	case Rollback:
		operationType = "rollback"
	case Destroy:
		operationType = "destroy"
	}

	// Local mode: record to the local history store instead of the backend. No
	// session is required; the returned id is a local UUID used downstream.
	if !config.BackendConfigured() {
		id := uuid.NewString()
		if a.history != nil {
			if err := a.history.Add(history.Record{
				ID:            id,
				OperationType: operationType,
				ResourceName:  spec.Name,
				// Canonicalize so deploy/rollback/destroy all key the same app the same
				// way (they used to write different casing).
				Platform:  history.CanonicalPlatform(platform),
				Language:  spec.Language,
				Status:    "started",
				StartedAt: time.Now(),
			}); err != nil {
				slog.Warn("failed to record local deployment start", "error", err)
			}
		}
		a.uiWriter.SendDeploymentStart(platform, source)
		slog.Info("Deployment start logged (local)", "operation_id", id, "platform", platform)
		return id, nil
	}

	session := CtxSession(ctx)
	if session == nil {
		return "", workflow.NewPermanentError(errors.New("no session found in context"))
	}

	// Build deployment operation data
	operation := map[string]any{
		"user_id":        session.User.ID,
		"operation_type": operationType,
		"resource_type":  "app",
		"resource_id":    fmt.Sprintf("%s-%s", platform, spec.Name),
		"resource_name":  spec.Name,
		"status":         "started",
		"platform":       platform,
		"language":       spec.Language,
		"deployment_config": map[string]any{
			"source":        source,
			"build_command": spec.BuildCommand,
			"start_command": spec.StartCommand,
		},
		"metadata": map[string]any{
			"service_requirements": spec.ServiceRequirements,
			"env_vars_count":       len(spec.EnvVars),
			"framework":            getFrameworkFromSpec(spec),
		},
	}

	// Add service type and provider if available
	if len(spec.ServiceRequirements) > 0 {
		for _, req := range spec.ServiceRequirements {
			if req.Type != "framework" {
				operation["service_type"] = req.Type
				operation["service_provider"] = req.Provider
				break
			}
		}
	}

	operationId, err := a.beClient.LogDeploymentOperation(ctx, session.AccessToken, operation)
	if err != nil {
		slog.Error("Failed to log deployment start", "error", err)
		return "", errors.Errorf("failed to log deployment start: %w", err)
	}

	// Emit deployment_start event for VSCode extension
	a.uiWriter.SendDeploymentStart(platform, source)

	slog.Info("Deployment start logged", "operation_id", operationId, "platform", platform)
	return operationId, nil
}

func (a *Activities) updateDeploymentStatus(ctx context.Context, operationId string, status string, metadata map[string]any) error {
	// Local mode: update the local history store instead of the backend.
	if !config.BackendConfigured() {
		if a.history != nil {
			if err := a.history.Update(operationId, status, time.Now(), metadata); err != nil {
				slog.Warn("failed to update local deployment status", "error", err, "operation_id", operationId)
			}
		}
		a.emitDeploymentCompleteIfDone(status, metadata)
		slog.Info("Deployment status updated (local)", "operation_id", operationId, "status", status)
		return nil
	}

	session := CtxSession(ctx)
	if session == nil {
		return workflow.NewPermanentError(errors.New("no session found in context"))
	}

	err := a.beClient.UpdateDeploymentOperation(ctx, session.AccessToken, operationId, status, metadata)
	if err != nil {
		slog.Error("Failed to update deployment status", "error", err, "operation_id", operationId, "status", status)
		return errors.Errorf("failed to update deployment status: %w", err)
	}

	a.emitDeploymentCompleteIfDone(status, metadata)
	slog.Info("Deployment status updated", "operation_id", operationId, "status", status)
	return nil
}

// emitDeploymentCompleteIfDone emits the deployment_complete UI event when a
// deploy reaches a terminal state, so all output modes see the final result.
func (a *Activities) emitDeploymentCompleteIfDone(status string, metadata map[string]any) {
	if status != "success" && status != "failed" {
		return
	}
	platform, _ := metadata["platform"].(string)
	url, _ := metadata["url"].(string)
	errorMsg, _ := metadata["error"].(string)
	a.uiWriter.SendDeploymentComplete(platform, status, url, errorMsg, 0)
}

// Helper function to extract framework from spec
func getFrameworkFromSpec(spec analyzer.ProjectSpec) string {
	for _, req := range spec.ServiceRequirements {
		if req.Type == "framework" {
			return req.Provider
		}
	}
	return "unknown"
}

func (a *Activities) determineRunCommand(ctx context.Context, spec analyzer.ProjectSpec) (string, error) {
	a.uiWriter.SendStatus("planning", "Calculating run command")
	var frameworks []string
	for _, req := range spec.ServiceRequirements {
		if req.Type == "framework" {
			frameworks = append(frameworks, req.Provider)
		}
	}

	envVars := make([]string, len(spec.EnvVars))
	for i, ev := range spec.EnvVars {
		envVars[i] = ev.VarName
	}

	lc := types.LaunchContext{
		Launchers: make([]types.LauncherFile, len(spec.LaunchContext.Launchers)),
		Readme:    spec.LaunchContext.Readme,
	}

	for _, l := range spec.LaunchContext.Launchers {
		slog.Info("launcher file", "name", l.Name)
		lc.Launchers = append(lc.Launchers, types.LauncherFile{
			Name:    l.Name,
			Content: l.Content,
		})
	}
	cmd, err := a.llmClient.DetermineLaunchCommand(ctx, spec.Language, frameworks, envVars, lc)
	if err != nil {
		a.uiWriter.SendStatusComplete("planning", "❌ Failed to calculate run command")
		return "", errors.Errorf("failed to determine launch command: %w", err)
	}
	a.uiWriter.SendStatusComplete("planning", "✅ Run command determined")
	return cmd.Command, nil
}

func (a *Activities) determineMigrationCommand(ctx context.Context, spec analyzer.ProjectSpec) (string, error) {
	a.uiWriter.SendStatus("planning", "Detecting database migrations")

	// Extract frameworks
	var frameworks []string
	for _, req := range spec.ServiceRequirements {
		if req.Type == "framework" {
			frameworks = append(frameworks, req.Provider)
		}
	}

	// Convert MigrationContext to BAML types
	mc := types.MigrationContext{
		MigrationFiles: make([]types.MigrationFile, 0),
		OrmTools:       spec.MigrationContext.ORMTools,
		PackageScripts: "",
		ConfigSnippets: "",
	}

	// Convert migration files
	for _, file := range spec.MigrationContext.MigrationFiles {
		fileType := "migration"
		if strings.Contains(file, "config") || strings.Contains(file, ".ini") || strings.Contains(file, ".json") {
			fileType = "config"
		} else if strings.Contains(file, "schema") {
			fileType = "schema"
		}
		mc.MigrationFiles = append(mc.MigrationFiles, types.MigrationFile{
			Path: file,
			Type: fileType,
		})
	}

	// Convert package scripts to JSON string
	if len(spec.MigrationContext.PackageScripts) > 0 {
		scriptsJSON, err := json.Marshal(spec.MigrationContext.PackageScripts)
		if err == nil {
			mc.PackageScripts = string(scriptsJSON)
		}
	}

	// Combine config snippets into a single string
	var configSnippets []string
	for file, content := range spec.MigrationContext.ConfigFiles {
		configSnippets = append(configSnippets, fmt.Sprintf("=== %s ===\n%s", file, content))
	}
	if len(configSnippets) > 0 {
		mc.ConfigSnippets = strings.Join(configSnippets, "\n\n")
	}

	cmd, err := a.llmClient.DetermineMigrationCommand(ctx, spec.Language, frameworks, spec.MigrationContext.ORMTools, mc)
	if err != nil {
		a.uiWriter.SendStatusComplete("planning", "❌ Failed to detect migration command")
		return "", errors.Errorf("failed to determine migration command: %w", err)
	}

	if cmd.Command == "" {
		a.uiWriter.SendStatusComplete("planning", "✓ No database migrations detected")
	} else {
		a.uiWriter.SendStatusComplete("planning", fmt.Sprintf("✅ Migration command: %s", cmd.Command))
	}

	return cmd.Command, nil
}

func (a *Activities) detectPlatformsForRollback(ctx context.Context, projectName string, sourcePath string) (ExistingProjectInfo, error) {
	platforms := []Platform{Render, FlyIO, Netlify, Vercel, Heroku, AWS}

	var mu sync.Mutex
	var wg sync.WaitGroup
	var detectedPlatforms []Platform
	var primaryResult ExistingProjectInfo
	primaryResult.Platform = UnknownPlatform

	type platformCheck struct {
		platform Platform
		exists   bool
		skipped  bool
		err      error
	}

	checkResults := make([]platformCheck, 0)

	for _, p := range platforms {
		wg.Add(1)
		platform := p
		go func() {
			defer wg.Done()

			detector, err := a.getProjectDetector(platform)
			if err != nil {
				slog.Info("Failed to get detector for platform", "platform", platform, "error", err)
				mu.Lock()
				checkResults = append(checkResults, platformCheck{platform: platform, skipped: true, err: err})
				mu.Unlock()
				return
			}

			result, err := detector.DetectExistingProject(ctx, projectName, sourcePath)
			if err != nil {
				slog.Info("Failed to detect project on platform (auth/API error)", "platform", platform, "error", err)
				mu.Lock()
				checkResults = append(checkResults, platformCheck{platform: platform, skipped: true, err: err})
				mu.Unlock()
				return
			}

			slog.Info("Platform detection result", "platform", platform, "exists", result.Exists)

			if result.Exists {
				mu.Lock()
				detectedPlatforms = append(detectedPlatforms, platform)
				checkResults = append(checkResults, platformCheck{platform: platform, exists: true})
				if primaryResult.Platform == UnknownPlatform {
					primaryResult = result
				}
				mu.Unlock()
			} else {
				mu.Lock()
				checkResults = append(checkResults, platformCheck{platform: platform, exists: false})
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	// Log what was skipped
	var skippedPlatforms []Platform
	for _, check := range checkResults {
		if check.skipped {
			skippedPlatforms = append(skippedPlatforms, check.platform)
		}
	}

	if len(skippedPlatforms) > 0 {
		slog.Warn("Some platforms were skipped due to authentication or API errors", "skipped", skippedPlatforms)
	}

	slog.Info("Detected platforms for rollback", "count", len(detectedPlatforms), "platforms", detectedPlatforms)

	primaryResult.DetectedPlatforms = detectedPlatforms

	if len(detectedPlatforms) == 0 {
		return ExistingProjectInfo{
			Exists:            false,
			Platform:          UnknownPlatform,
			DetectedPlatforms: []Platform{},
		}, nil
	}

	if len(detectedPlatforms) == 1 {
		primaryResult.Platform = detectedPlatforms[0]
	}

	return primaryResult, nil
}
