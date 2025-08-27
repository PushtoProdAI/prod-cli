package agent

import (
	"context"
	"log"
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
		w.uiWriter.SendStatus("pricing", "Calculating estimated costs...")

		// Build deployment spec for cost estimation
		db := deployment.NewDeploymentBuilder(&spec, []deployment.EnvVar{})
		deploymentSpec, err := db.Build()
		if err != nil {
			log.Printf("Failed to build deployment spec for cost estimation: %v", err)
		} else {
			// Estimate costs based on platform
			var estimatedCosts deployment.CostEstimate
			switch platform {
			case Render:
				estimatedCosts, err = workflow.ExecuteActivity[deployment.CostEstimate](ctx, ActivityOpts, AgentEstimateRenderCosts, *deploymentSpec, deployment.StrategyRenderQueued).Get(ctx)
			case FlyIO:
				estimatedCosts, err = workflow.ExecuteActivity[deployment.CostEstimate](ctx, ActivityOpts, AgentEstimateFlyioCosts, *deploymentSpec, deployment.StrategyFlyio).Get(ctx)
			}

			if err != nil {
				log.Printf("Failed to estimate costs: %v", err)
			} else {
				// Display pricing information
				displayPricingInfo(w.uiWriter, estimatedCosts)
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
			log.Printf("failed to get current working directory: %v", err)
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
	return *spec, err
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
