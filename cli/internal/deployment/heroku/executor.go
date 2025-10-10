package heroku

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/go-errors/errors"

	"github.com/meroxa/prod/cli/internal/deployment"
	"github.com/meroxa/prod/cli/internal/output"
)

// StepExecutor executes deployment steps with dependency resolution
type StepExecutor struct {
	client           *HerokuClient
	stepResults      map[string]interface{}
	createdResources []deployment.CreatedResource
	executedSteps    []HerokuAPIStep
	writer           io.Writer
}

// NewStepExecutor creates a new step executor
func NewStepExecutor(client *HerokuClient, writer io.Writer) *StepExecutor {
	if writer == nil {
		writer = output.NewNoOpWriter()
	}
	return &StepExecutor{
		client:           client,
		stepResults:      make(map[string]interface{}),
		createdResources: make([]deployment.CreatedResource, 0),
		executedSteps:    make([]HerokuAPIStep, 0),
		writer:           writer,
	}
}

func (se *StepExecutor) InjectExistingApp(appName string) {
	app, err := se.client.GetApp(context.Background(), appName)
	if err != nil {
		slog.Warn("Failed to get app details for existing app", "app", appName, "error", err)
		return
	}

	// Get web URL from app or construct it
	var webURL string
	if app.WebURL != nil && *app.WebURL != "" {
		webURL = *app.WebURL
	} else {
		webURL = fmt.Sprintf("https://%s.herokuapp.com", app.Name)
	}

	resource := deployment.CreatedResource{
		Name: app.Name,
		Type: "heroku-app",
		ID:   app.ID,
		Metadata: map[string]interface{}{
			"url":     webURL,
			"git_url": app.GitURL,
			"region":  app.Region.Name,
			"app":     app,
		},
	}

	se.stepResults["app"] = resource
	// IMPORTANT: Also add to createdResources so workflow can find the URL
	se.createdResources = append(se.createdResources, resource)
}

// ExecuteSteps executes all steps in dependency order
func (se *StepExecutor) ExecuteSteps(ctx context.Context, steps []HerokuAPIStep) ([]deployment.CreatedResource, error) {
	executed := make(map[string]bool)

	for len(executed) < len(steps) {
		progress := false

		for _, step := range steps {
			if executed[step.GetID()] {
				continue
			}

			// Check if all dependencies are satisfied
			if se.dependenciesSatisfied(step.GetDependencies(), executed) {
				// Show step start message
				fmt.Fprintf(se.writer, "🔄 Executing: %s...\n", step.GetDescription())

				if err := se.ExecuteStep(ctx, step); err != nil {
					fmt.Fprintf(se.writer, "✗ Failed: %s - %v\n", step.GetDescription(), err)
					// Attempt rollback of executed steps
					if rollbackErr := se.rollback(ctx); rollbackErr != nil {
						fmt.Fprintf(se.writer, "⚠️  Rollback failed: %v\n", rollbackErr)
					}
					return se.createdResources, errors.Errorf("failed to execute step %s: %w", step.GetID(), err)
				}

				executed[step.GetID()] = true
				progress = true
				fmt.Fprintf(se.writer, "✓ Completed: %s\n", step.GetDescription())
			}
		}

		if !progress {
			return se.createdResources, errors.Errorf("circular dependency detected or unresolvable dependencies")
		}
	}

	return se.createdResources, nil
}

// dependenciesSatisfied checks if all dependencies for a step have been executed
func (se *StepExecutor) dependenciesSatisfied(dependencies []string, executed map[string]bool) bool {
	for _, dep := range dependencies {
		if !executed[dep] {
			return false
		}
	}
	return true
}

// ExecuteStep executes a single step
func (se *StepExecutor) ExecuteStep(ctx context.Context, step HerokuAPIStep) error {
	result, err := step.Execute(ctx, se.client, se.stepResults)
	if err != nil {
		return err
	}

	// Store the result for future steps to use
	se.stepResults[step.GetID()] = result

	// Track executed step for rollback
	se.executedSteps = append(se.executedSteps, step)

	// Track created resources
	if result != nil {
		if res, ok := result.(deployment.CreatedResource); ok {
			se.createdResources = append(se.createdResources, res)
		}
	}

	return nil
}

// rollback attempts to rollback all executed steps in reverse order
func (se *StepExecutor) rollback(ctx context.Context) error {
	fmt.Fprintf(se.writer, "🔄 Initiating rollback...\n")

	// Rollback in reverse order
	for i := len(se.executedSteps) - 1; i >= 0; i-- {
		step := se.executedSteps[i]
		fmt.Fprintf(se.writer, "  Rolling back: %s\n", step.GetDescription())

		if err := step.Rollback(ctx, se.client, se.stepResults); err != nil {
			fmt.Fprintf(se.writer, "  ⚠️  Failed to rollback %s: %v\n", step.GetDescription(), err)
			// Continue with other rollbacks even if one fails
		}
	}

	fmt.Fprintf(se.writer, "✓ Rollback completed\n")
	return nil
}
