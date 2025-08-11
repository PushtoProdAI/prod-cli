package render

import (
	"context"
	"fmt"
	"io"
)

func (se *StepExecutor) rollback(ctx context.Context, out io.Writer) error {
	fmt.Fprintf(out, "🔄 Attempting rollback of executed steps...\n")

	// Try step-based rollback first (in reverse order)
	for i := len(se.executedSteps) - 1; i >= 0; i-- {
		step := se.executedSteps[i]
		if err := step.Rollback(ctx, se.client, se.stepResults); err != nil {
			fmt.Fprintf(out, "⚠️  Failed to rollback step %s: %v\n", step.GetDescription(), err)
		} else {
			fmt.Fprintf(out, "✓ Rolled back step: %s\n", step.GetDescription())
		}
	}

	// Fallback to resource-based rollback for any missed resources
	err := se.rollbackResources(ctx, out)

	// Always write a completion message to stop any active spinners
	if err != nil {
		fmt.Fprintf(out, "✗ Failed to complete rollback: %v\n", err)
	} else {
		fmt.Fprintf(out, "✓ Completed rollback process\n")
	}

	return err
}
func (se *StepExecutor) rollbackResources(_ context.Context, out io.Writer) error {
	fmt.Fprintf(out, "🔄 Attempting resource-based rollback fallback...\n")

	// Rollback in reverse order
	for i := len(se.createdResources) - 1; i >= 0; i-- {
		resource := se.createdResources[i]

		// Currently Render doesn't support programmatic deletion of services
		// This is a placeholder for future implementation
		fmt.Fprintf(out, "⚠️  No rollback handler for resource type: %s (name: %s)\n", resource.Type, resource.Name)
	}

	// Write completion message to stop the spinner
	if len(se.createdResources) > 0 {
		fmt.Fprintf(out, "✓ Completed resource cleanup (manual cleanup may be required)\n")
	} else {
		fmt.Fprintf(out, "✓ Completed resource cleanup (no resources to clean)\n")
	}

	return nil
}
