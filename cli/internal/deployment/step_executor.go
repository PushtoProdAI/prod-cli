package deployment

import (
	"context"
	"fmt"
	"io"

	"github.com/go-errors/errors"
	"github.com/meroxa/prod/cli/internal/output"
)

type Step[C any] interface {
	Execute(ctx context.Context, client C, stepResults map[string]any) (any, error)
	Rollback(ctx context.Context, client C, stepResults map[string]any) error
	GetID() string
	GetDescription() string
	GetDependencies() []string
}

type BaseStep struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	DependsOn   []string `json:"dependsOn,omitempty"`
}

func (b *BaseStep) GetID() string {
	return b.ID
}

func (b *BaseStep) GetDescription() string {
	return b.Description
}

func (b *BaseStep) GetDependencies() []string {
	if b.DependsOn == nil {
		return []string{}
	}
	return b.DependsOn
}

type StepExecutor[C any] struct {
	client           C
	stepResults      map[string]any
	createdResources []CreatedResource
	executedSteps    []Step[C]
	writer           io.Writer
}

func NewStepExecutor[C any](client C, writer io.Writer) *StepExecutor[C] {
	if writer == nil {
		writer = output.NewNoOpWriter()
	}
	return &StepExecutor[C]{
		client:           client,
		stepResults:      make(map[string]any),
		createdResources: make([]CreatedResource, 0),
		executedSteps:    make([]Step[C], 0),
		writer:           writer,
	}
}

func (se *StepExecutor[C]) GetStepResults() map[string]any {
	return se.stepResults
}

func (se *StepExecutor[C]) GetCreatedResources() []CreatedResource {
	return se.createdResources
}

func (se *StepExecutor[C]) InjectStepResult(stepID string, result any) {
	se.stepResults[stepID] = result
	if resource, ok := result.(CreatedResource); ok {
		se.createdResources = append(se.createdResources, resource)
	}
}

func (se *StepExecutor[C]) ExecuteSteps(ctx context.Context, steps []Step[C]) ([]CreatedResource, error) {
	executed := make(map[string]bool)

	for len(executed) < len(steps) {
		progress := false

		for _, step := range steps {
			if executed[step.GetID()] {
				continue
			}

			if se.dependenciesSatisfied(step.GetDependencies(), executed) {
				fmt.Fprintf(se.writer, "🔄 Executing: %s...\n", step.GetDescription())

				if err := se.ExecuteStep(ctx, step); err != nil {
					fmt.Fprintf(se.writer, "✗ Failed: %s - %v\n", step.GetDescription(), err)
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

func (se *StepExecutor[C]) dependenciesSatisfied(dependencies []string, executed map[string]bool) bool {
	for _, dep := range dependencies {
		if !executed[dep] {
			return false
		}
	}
	return true
}

func (se *StepExecutor[C]) ExecuteStep(ctx context.Context, step Step[C]) error {
	result, err := step.Execute(ctx, se.client, se.stepResults)
	if err != nil {
		return err
	}

	se.stepResults[step.GetID()] = result

	se.executedSteps = append(se.executedSteps, step)

	if result != nil {
		if res, ok := result.(CreatedResource); ok {
			se.createdResources = append(se.createdResources, res)
		}
	}

	return nil
}

func (se *StepExecutor[C]) rollback(ctx context.Context) error {
	fmt.Fprintf(se.writer, "🔄 Initiating rollback...\n")

	for i := len(se.executedSteps) - 1; i >= 0; i-- {
		step := se.executedSteps[i]
		fmt.Fprintf(se.writer, "  Rolling back: %s\n", step.GetDescription())

		if err := step.Rollback(ctx, se.client, se.stepResults); err != nil {
			fmt.Fprintf(se.writer, "  ⚠️  Failed to rollback %s: %v\n", step.GetDescription(), err)
		}
	}

	fmt.Fprintf(se.writer, "✓ Rollback completed\n")
	return nil
}
