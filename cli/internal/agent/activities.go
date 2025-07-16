package agent

import (
	"context"
	"log"
	"os"

	"github.com/go-errors/errors"
	"github.com/meroxa/prod/cli/baml_client"
	"github.com/meroxa/prod/cli/baml_client/types"
	"github.com/meroxa/prod/cli/internal/analyzer"
	"github.com/meroxa/prod/cli/internal/workflowext"
)

const (
	AgentDetermineIntent = "agent.determineIntent"
	AgentAnalyzeProject  = "agent.analyzeProject"
	AgentSummarizeIntent = "agent.summarize"
)

type Activities struct{}

func (a *Activities) Activities() []workflowext.Activity {
	return []workflowext.Activity{
		{Name: AgentDetermineIntent, ActFunc: a.determineIntent},
		{Name: AgentAnalyzeProject, ActFunc: a.analyze},
		{Name: AgentSummarizeIntent, ActFunc: a.summarize},
	}
}

func (a *Activities) determineIntent(ctx context.Context, prompt string) (types.Intent, error) {
	intent, err := baml_client.ExtractIntent(ctx, prompt)
	if err != nil {
		return types.Intent{}, errors.Errorf("failed to extract intent: %w", err)
	}
	if intent.Source == "pwd" {
		path, err := os.Getwd()
		if err != nil {
			intent.Source = ""
			log.Printf("failed to get current working directory: %v", err)
			return intent, nil
		}
		intent.Source = path
	}
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
	summary, err := baml_client.SummarizeIntent(ctx, intent, name, language)
	if err != nil {
		return "", errors.Errorf("failed to summarize intent: %w", err)
	}
	return summary.Summary, nil
}
