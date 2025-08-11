package render

import (
	"context"
)

func (se *StepExecutor) rollback(ctx context.Context) error {
	se.writer.Printf("🔄 Attempting rollback of executed steps...\n")

	// Try step-based rollback first (in reverse order)
	for i := len(se.executedSteps) - 1; i >= 0; i-- {
		step := se.executedSteps[i]
		if err := step.Rollback(ctx, se.client, se.stepResults); err != nil {
			se.writer.Printf("⚠️  Failed to rollback step %s: %v\n", step.GetDescription(), err)
		} else {
			se.writer.Printf("✓ Rolled back step: %s\n", step.GetDescription())
		}
	}

	// Fallback to resource-based rollback for any missed resources
	err := se.rollbackResources(ctx)

	// Always write a completion message to stop any active spinners
	if err != nil {
		se.writer.Printf("✗ Failed to complete rollback: %v\n", err)
	} else {
		se.writer.Printf("✓ Completed rollback process\n")
	}

	return err
}
func (se *StepExecutor) rollbackResources(_ context.Context) error {
	se.writer.Printf("🔄 Attempting resource-based rollback fallback...\n")

	// Rollback in reverse order
	for i := len(se.createdResources) - 1; i >= 0; i-- {
		resource := se.createdResources[i]

		// Currently Render doesn't support programmatic deletion of services
		// This is a placeholder for future implementation
		se.writer.Printf("⚠️  No rollback handler for resource type: %s (name: %s)\n", resource.Type, resource.Name)
	}

	// Write completion message to stop the spinner
	if len(se.createdResources) > 0 {
		se.writer.Printf("✓ Completed resource cleanup (manual cleanup may be required)\n")
	} else {
		se.writer.Printf("✓ Completed resource cleanup (no resources to clean)\n")
	}

	return nil
}
