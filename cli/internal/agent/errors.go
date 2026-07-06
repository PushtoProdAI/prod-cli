package agent

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"strings"

	"github.com/go-errors/errors"
	"github.com/pushtoprodai/prod-cli/baml_client/types"
)

func (a *Activities) summarizeError(ctx context.Context, error string, input DeployPlan) (deployError, error) {
	intent := types.Intent{
		Action:   input.Action.String(),
		Platform: input.Platform.DisplayName(),
		Source:   input.Source,
	}

	spec := types.ProjectSpec{
		BuildCommand: input.Spec.BuildCommand,
		Language:     input.Spec.Language,
		Name:         input.Spec.Name,
		StartCommand: input.Spec.StartCommand,
	}

	a.uiWriter.SendStatus("summarizing", "Creating next steps...")

	var summary types.Error
	var violations []string
	// handling this internally for now, but we could also bubble this up to the workflow
	for {
		s, err := a.llmClient.SummarizeDeployError(ctx, error, intent, spec, runtime.GOOS, violations)
		if err != nil {
			return deployError{}, errors.Errorf("failed to summarize error: %w", err)
		}

		violations = findErrorViolations(s, error, input.Platform.DisplayName())
		if len(violations) == 0 {
			summary = s
			break
		}

		slog.Info("Found violations in summary, re-prompting", "violationCount", len(violations), "violations", violations)
	}

	deployError := deployError{
		Summary:      summary.Summary,
		Remediations: make([]Remediation, len(summary.Remediations)),
	}

	for i, r := range summary.Remediations {
		deployError.Remediations[i] = Remediation{
			Description: r.Description,
			CliCommand:  r.CliCommand,
		}
	}

	slog.Info("Error summary", "summary", deployError.Summary)
	slog.Info("Remediations", "remediations", deployError.Remediations)

	return deployError, nil
}

func findErrorViolations(summary types.Error, errorMsg string, platform string) []string {
	var errs []string

	lowerOutput := strings.ToLower(summary.Summary)
	lowerError := strings.ToLower(errorMsg)
	lowerPlatform := strings.ToLower(platform)

	containsNotInError := func(text string) bool {
		return strings.Contains(lowerOutput, text) && !strings.Contains(lowerError, text)
	}

	// 1. Wrong platform mentions
	if lowerPlatform == FlyIO.String() {
		if containsNotInError("render") {
			errs = append(errs, "Mentioned Render in Fly.io context")
		}
		if containsNotInError("~/.render") {
			errs = append(errs, "Mentioned Render config path in Fly.io context")
		}
		if containsNotInError("$render_api_key") {
			errs = append(errs, "Mentioned Render env var in Fly.io context")
		}
		if (strings.Contains(lowerOutput, "docker") || strings.Contains(lowerOutput, "ecr")) &&
			!strings.Contains(lowerError, "docker") {
			errs = append(errs, "Mentioned Docker/ECR in Fly.io context without Docker in error message")
		}
	}

	if lowerPlatform == Render.String() {
		if containsNotInError("fly.io") || containsNotInError("fly") || containsNotInError("flyio") {
			errs = append(errs, "Mentioned Fly.io in Render context")
		}
		if containsNotInError("~/.fly") {
			errs = append(errs, "Mentioned Fly.io config path in Render context")
		}
	}

	// 2. Forbidden commands
	forbiddenCmds := []string{"docker login", "docker push", "prod login"}
	for _, cmd := range forbiddenCmds {
		if strings.Contains(lowerOutput, cmd) {
			errs = append(errs, fmt.Sprintf("Suggested forbidden command: %s", cmd))
		}
	}

	return errs
}
