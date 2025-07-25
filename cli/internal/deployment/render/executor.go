package render

import (
	"context"
	"fmt"
)

type StepExecutor struct {
	client           RenderClient
	stepResults      map[string]any
	createdResources []CreatedResource
	executedSteps    []RenderAPIStep
}

type CreatedResource struct {
	ID   string
	Type string
	Name string
}

func NewStepExecutor(client RenderClient) *StepExecutor {
	return &StepExecutor{
		client:           client,
		stepResults:      make(map[string]any),
		createdResources: make([]CreatedResource, 0),
		executedSteps:    make([]RenderAPIStep, 0),
	}
}

func (se *StepExecutor) ExecuteSteps(ctx context.Context, steps []RenderAPIStep) error {
	executed := make(map[string]bool)

	for len(executed) < len(steps) {
		progress := false

		for _, step := range steps {
			if executed[step.GetID()] {
				continue
			}

			// Check if all dependencies are satisfied
			if se.dependenciesSatisfied(step.GetDependencies(), executed) {
				if err := se.ExecuteStep(ctx, step); err != nil {
					fmt.Printf("✗ Failed: %s - %v\n", step.GetDescription(), err)
					// Attempt rollback of created resources
					if rollbackErr := se.rollback(ctx); rollbackErr != nil {
						fmt.Printf("⚠️  Rollback failed: %v\n", rollbackErr)
					}
					return fmt.Errorf("failed to execute step %s: %w", step.GetID(), err)
				}
				executed[step.GetID()] = true
				progress = true
				fmt.Printf("✓ Completed: %s\n", step.GetDescription())
			}
		}

		if !progress {
			return fmt.Errorf("circular dependency detected or unresolvable dependencies")
		}
	}

	return nil
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

	// Store the result for future steps to use
	se.stepResults[step.GetID()] = result

	// Track executed step for rollback
	se.executedSteps = append(se.executedSteps, step)

	// Track created resources for rollback (if the result is a resource)
	if result != nil {
		switch res := result.(type) {
		case *RenderService:
			se.createdResources = append(se.createdResources, CreatedResource{
				ID:   res.ID,
				Type: res.Type,
				Name: res.Name,
			})
		}
	}

	return nil
}

