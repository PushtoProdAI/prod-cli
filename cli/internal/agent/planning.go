package agent

import (
	"context"
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
)

// planDeploy workflow handles the planning phase of deployment
func (w *Workflows) planDeploy(ctx workflow.Context, input string) (DeployPlan, error) {
	intent, err := workflow.ExecuteActivity[types.Intent](ctx, ActivityOpts, AgentDetermineIntent, input).Get(ctx)
	if err != nil {
		slog.Error("Failed to determine intent", "error", err)
		w.uiWriter.SendStatus("error", "Failed to determine intent")
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
		} else {
			w.uiWriter.SendStatusComplete("analyzing", "✅ Project analyzed")
		}
	}

	opts := ActivityOpts
	opts.RetryOptions.MaxAttempts = 2
	_, err = workflow.ExecuteActivity[any](ctx, opts, AgentSendProjectStats, intent.Platform, spec).Get(ctx)
	if err != nil {
		slog.Error("Failed to send project stats", "error", err)
	}

	summary, err := workflow.ExecuteActivity[string](ctx, ActivityOpts, AgentSummarizeIntent, intent, spec.Name, spec.Language).Get(ctx)
	if err != nil {
		slog.Error("Failed to summarize intent", "error", err)
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
			}
			if cmd != "" {
				plan.Spec.StartCommand = cmd
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

func (a *Activities) summarize(ctx context.Context, intent types.Intent, name string, language string) (string, error) {
	a.uiWriter.SendStatus("summarizing", "Summarizing your request...")
	summary, err := a.llmClient.SummarizeIntent(ctx, intent, name, language)
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

// maskToken masks sensitive tokens for logging
func maskToken(token string) string {
	if token == "" {
		return "(empty)"
	}
	if len(token) < 8 {
		return "***"
	}
	return token[:4] + "***" + token[len(token)-4:]
}

// min returns the smaller of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// debugExtractIntent is a debug wrapper that calls the BAML function with detailed logging
func debugExtractIntent(ctx context.Context, prompt, accessToken, supabaseURL string) string {
	// Debug logging to file
	logFile := "/Users/william-meroxa/.prod/log.txt"
	debugLog := func(msg string, args ...interface{}) {
		if f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
			defer f.Close()
			fmt.Fprintf(f, "[%s] %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(msg, args...))
		}
	}

	debugLog("[DEBUG WRAPPER] Starting debug ExtractIntent call")
	debugLog("[DEBUG WRAPPER] Prompt: %s", prompt)
	debugLog("[DEBUG WRAPPER] AccessToken: %s", maskToken(accessToken))
	debugLog("[DEBUG WRAPPER] SupabaseURL: %s", supabaseURL)

	// Call the debug BAML function
	debugResult, err := baml_client.DebugExtractIntent(ctx, prompt, baml_client.WithEnv(map[string]string{
		"PROXY_API_KEY": accessToken,
		"SUPABASE_URL":  supabaseURL,
	}))
	if err != nil {
		debugLog("[DEBUG WRAPPER] DebugExtractIntent failed: %v", err)
		return fmt.Sprintf("Debug failed: %v", err)
	}

	debugLog("[DEBUG WRAPPER] DebugExtractIntent succeeded: %s", debugResult)
	return debugResult
}
