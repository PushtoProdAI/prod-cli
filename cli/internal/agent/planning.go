package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/cschleiden/go-workflows/workflow"
	"github.com/go-errors/errors"
	"github.com/meroxa/prod/cli/baml_client"
	"github.com/meroxa/prod/cli/baml_client/types"
	"github.com/meroxa/prod/cli/internal/analyzer"
	"github.com/meroxa/prod/cli/internal/deployment"
	prod_error "github.com/meroxa/prod/cli/internal/error"
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

	summary, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentSummarizeIntent, intent, spec.Name, spec.Language).Get(ctx)
	if err != nil {
		slog.Error("Failed to summarize intent", "error", err)
		prod_error.CaptureErrorWithContext(err, map[string]any{
			"workflow":     PlanDeployWorkflowName,
			"activity":     AgentSummarizeIntent,
			"component":    "workflow",
			"platform":     intent.Platform,
			"project_name": spec.Name,
			"language":     spec.Language,
		})
	}
	platform := UnknownPlatform
	switch strings.ToLower(intent.Platform) {
	case "render":
		platform = Render
	case "fly.io":
		platform = FlyIO
	case "netlify":
		platform = Netlify
	case "vercel":
		platform = Vercel
	case "heroku":
		platform = Heroku
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

	plan := DeployPlan{
		Action:           action,
		Platform:         platform,
		Source:           intent.Source,
		Spec:             spec,
		Summary:          summary,
		DryRunFromPrompt: intent.DryRun,
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
		db := deployment.NewDeploymentBuilder(&spec, []deployment.EnvVar{})
		deploymentSpec, err := db.Build()
		if err != nil {
			slog.Info("Failed to build deployment spec for cost estimation", "error", err)
		} else {
			// Estimate costs based on platform
			var estimatedCosts deployment.CostEstimate
			switch platform {
			case Render:
				estimatedCosts, err = workflow.ExecuteActivity[deployment.CostEstimate](ctx, ActivityOpts, AgentEstimateRenderCosts, *deploymentSpec, deployment.StrategyRenderQueued).Get(ctx)
			case FlyIO:
				estimatedCosts, err = workflow.ExecuteActivity[deployment.CostEstimate](ctx, ActivityOpts, AgentEstimateFlyioCosts, *deploymentSpec, deployment.StrategyFlyio).Get(ctx)
			case Netlify:
				estimatedCosts, err = workflow.ExecuteActivity[deployment.CostEstimate](ctx, ActivityOpts, AgentEstimateNetlifyCosts, *deploymentSpec, deployment.StrategyNetlify).Get(ctx)
			case Vercel:
				estimatedCosts, err = workflow.ExecuteActivity[deployment.CostEstimate](ctx, ActivityOpts, AgentEstimateVercelCosts, *deploymentSpec, deployment.StrategyVercel).Get(ctx)
			}

			if err != nil {
				slog.Info("Failed to estimate costs", "error", err)
				var activity string
				switch platform {
				case Render:
					activity = AgentEstimateRenderCosts
				case FlyIO:
					activity = AgentEstimateFlyioCosts
				case Netlify:
					activity = AgentEstimateNetlifyCosts
				case Vercel:
					activity = AgentEstimateVercelCosts
				}
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
	intent, err := baml_client.ExtractIntent(ctx, prompt)
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

func (a *Activities) sendProjectStats(ctx context.Context, platform string, spec analyzer.ProjectSpec) error {
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

func (a *Activities) logDeploymentStart(ctx context.Context, platform string, spec analyzer.ProjectSpec, source string) (string, error) {
	session := CtxSession(ctx)
	if session == nil {
		return "", workflow.NewPermanentError(errors.New("no session found in context"))
	}

	// Build deployment operation data
	operation := map[string]any{
		"user_id":        session.User.ID,
		"operation_type": "deploy",
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

	slog.Info("Deployment start logged", "operation_id", operationId, "platform", platform)
	return operationId, nil
}

func (a *Activities) updateDeploymentStatus(ctx context.Context, operationId string, status string, metadata map[string]any) error {
	session := CtxSession(ctx)
	if session == nil {
		return workflow.NewPermanentError(errors.New("no session found in context"))
	}

	err := a.beClient.UpdateDeploymentOperation(ctx, session.AccessToken, operationId, status, metadata)
	if err != nil {
		slog.Error("Failed to update deployment status", "error", err, "operation_id", operationId, "status", status)
		return errors.Errorf("failed to update deployment status: %w", err)
	}

	slog.Info("Deployment status updated", "operation_id", operationId, "status", status)
	return nil
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
	cmd, err := baml_client.DetermineLaunchCommand(ctx, spec.Language, frameworks, envVars, lc)
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

	cmd, err := baml_client.DetermineMigrationCommand(ctx, spec.Language, frameworks, spec.MigrationContext.ORMTools, mc)
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
