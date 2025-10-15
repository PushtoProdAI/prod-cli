package render

import (
	"context"
	"fmt"
	"io"

	"github.com/go-errors/errors"

	"github.com/meroxa/prod/cli/internal/deployment"
	"github.com/meroxa/prod/cli/internal/output"
)

type StepExecutor struct {
	client           RenderClient
	stepResults      map[string]any
	createdResources []deployment.CreatedResource
	executedSteps    []RenderAPIStep
	writer           io.Writer
}

func NewStepExecutor(client RenderClient, writer io.Writer) *StepExecutor {
	if writer == nil {
		writer = output.NewNoOpWriter()
	}
	return &StepExecutor{
		client:           client,
		stepResults:      make(map[string]any),
		createdResources: make([]deployment.CreatedResource, 0),
		executedSteps:    make([]RenderAPIStep, 0),
		writer:           writer,
	}
}

func (se *StepExecutor) ExecuteSteps(ctx context.Context, steps []RenderAPIStep, out io.Writer) ([]deployment.CreatedResource, error) {
	executed := make(map[string]bool)

	for len(executed) < len(steps) {
		progress := false

		for _, step := range steps {
			if executed[step.GetID()] {
				continue
			}

			// Check if all dependencies are satisfied
			if se.dependenciesSatisfied(step.GetDependencies(), executed) {
				// Show step start message (will trigger spinner automatically)
				fmt.Fprintf(out, "🔄 Executing: %s...\n", step.GetDescription())

				if err := se.ExecuteStep(ctx, step); err != nil {
					fmt.Fprintf(out, "✗ Failed: %s - %v\n", step.GetDescription(), err)
					// Attempt rollback of created resources
					if rollbackErr := se.rollback(ctx, out); rollbackErr != nil {
						fmt.Fprintf(out, "⚠️  Rollback failed: %v\n", rollbackErr)
					}
					return se.createdResources, errors.Errorf("failed to execute step %s: %w", step.GetID(), err)
				}

				executed[step.GetID()] = true
				progress = true
				fmt.Fprintf(out, "✓ Completed: %s\n", step.GetDescription())
			}
		}

		if !progress {
			return se.createdResources, errors.Errorf("circular dependency detected or unresolvable dependencies")
		}
	}

	return se.createdResources, nil
}

func (se *StepExecutor) dependenciesSatisfied(dependencies []string, executed map[string]bool) bool {
	for _, dep := range dependencies {
		if !executed[dep] {
			return false
		}
	}
	return true
}

func (se *StepExecutor) ExecuteStep(ctx context.Context, step RenderAPIStep) error {
	result, err := step.Execute(ctx, se.client, se.stepResults)
	if err != nil {
		return err
	}

	se.stepResults[step.GetID()] = result

	se.executedSteps = append(se.executedSteps, step)

	if result != nil {
		switch res := result.(type) {
		case *RenderService:
			cr := deployment.CreatedResource{
				ID:   res.ID,
				Type: res.Type,
				Name: res.Name,
			}

			if extra, ok := se.stepResults["trigger_deploy_extra"].(map[string]any); ok {
				cr.Metadata = extra
			}

			se.createdResources = append(se.createdResources, cr)
		}
	}
	return nil
}
