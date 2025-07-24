package render

import (
	"context"
	"fmt"
)

func (se *StepExecutor) rollback(ctx context.Context) error {
	fmt.Println("🔄 Attempting rollback of executed steps...")
	
	// Try step-based rollback first (in reverse order)
	for i := len(se.executedSteps) - 1; i >= 0; i-- {
		step := se.executedSteps[i]
		if err := step.Rollback(ctx, se.client, se.stepResults); err != nil {
			fmt.Printf("⚠️  Failed to rollback step %s: %v\n", step.GetDescription(), err)
		} else {
			fmt.Printf("✓ Rolled back step: %s\n", step.GetDescription())
		}
	}
	
	// Fallback to resource-based rollback for any missed resources
	return se.rollbackResources(ctx)
}

func (se *StepExecutor) rollbackResources(_ context.Context) error {
	fmt.Println("🔄 Attempting resource-based rollback fallback...")
	
	// Rollback in reverse order
	for i := len(se.createdResources) - 1; i >= 0; i-- {
		resource := se.createdResources[i]
		
		// Currently Render doesn't support programmatic deletion of services
		// This is a placeholder for future implementation
		fmt.Printf("⚠️  No rollback handler for resource type: %s (name: %s)\n", resource.Type, resource.Name)
	}
	
	return nil
}