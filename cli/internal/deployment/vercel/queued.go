package vercel

import (
	"context"
	"fmt"
	"io"

	"github.com/go-errors/errors"

	"github.com/meroxa/prod/cli/internal/deployment"
)

// VercelQueuedDeployment handles step-by-step deployments to Vercel
// This deployment strategy creates resources one at a time with progress tracking
type VercelQueuedDeployment struct {
	client VercelClient
	spec   *deployment.DeploymentSpec
	writer io.Writer
}

// NewVercelQueuedDeployment creates a new queued deployment for Vercel
func NewVercelQueuedDeployment(client VercelClient, spec *deployment.DeploymentSpec, writer io.Writer) *VercelQueuedDeployment {
	return &VercelQueuedDeployment{
		client: client,
		spec:   spec,
		writer: writer,
	}
}

// Deploy performs the queued deployment to Vercel
func (vqd *VercelQueuedDeployment) Deploy(ctx context.Context) ([]deployment.CreatedResource, error) {
	steps := vqd.GenerateAPISteps()

	var createdResources []deployment.CreatedResource
	stepResults := make(map[string]any)

	// Track executed steps for rollback
	var executedSteps []VercelAPIStep

	for _, step := range steps {
		fmt.Fprintf(vqd.writer, "🔄 Executing: %s...\n", step.GetDescription())

		result, err := step.Execute(ctx, vqd.client, stepResults)
		if err != nil {
			fmt.Fprintf(vqd.writer, "❌ Failed: %s - %v\n", step.GetDescription(), err)

			// Attempt rollback of executed steps
			if len(executedSteps) > 0 {
				fmt.Fprintf(vqd.writer, "🔄 Rolling back...\n")
				for i := len(executedSteps) - 1; i >= 0; i-- {
					rollbackStep := executedSteps[i]
					fmt.Fprintf(vqd.writer, "  ↩️  Rolling back: %s\n", rollbackStep.GetDescription())
					if rbErr := rollbackStep.Rollback(ctx, vqd.client, stepResults); rbErr != nil {
						fmt.Fprintf(vqd.writer, "  ⚠️  Rollback failed: %v\n", rbErr)
					}
				}
			}

			return nil, errors.Errorf("step %s failed: %w", step.GetID(), err)
		}

		stepResults[step.GetID()] = result
		executedSteps = append(executedSteps, step)

		// Convert result to CreatedResource if applicable
		if resource, ok := result.(deployment.CreatedResource); ok {
			createdResources = append(createdResources, resource)
		}

		fmt.Fprintf(vqd.writer, "✅ Completed: %s\n", step.GetDescription())
	}

	// Print final deployment URL if available
	if deployResult, ok := stepResults["deploy-project"]; ok {
		if resource, ok := deployResult.(deployment.CreatedResource); ok {
			if url, ok := resource.Metadata["url"].(string); ok {
				fmt.Fprintf(vqd.writer, "\n🎉 Deployment successful!\n")
				fmt.Fprintf(vqd.writer, "🌐 Site URL: %s\n", url)
			}
		}
	}

	return createdResources, nil
}

// GenerateAPISteps generates the deployment steps for Vercel
func (vqd *VercelQueuedDeployment) GenerateAPISteps() []VercelAPIStep {
	var steps []VercelAPIStep

	// Step 1: Create project
	createProjectStep := NewCreateVercelProjectStep(vqd.spec.Name, "", nil)
	steps = append(steps, createProjectStep)

	// Step 2: Link CLI to project
	linkStep := NewLinkVercelProjectStep("create-project", vqd.getSourcePath(), vqd.writer)
	steps = append(steps, linkStep)

	// Step 3: Set all environment variables after linking
	if len(vqd.spec.EnvVars) > 0 {
		envVars := make(map[string]string)
		for _, env := range vqd.spec.EnvVars {
			envVars[env.Name] = env.Value
		}
		envStep := NewSetEnvironmentVariablesStep("create-project", "link-project", vqd.getSourcePath(), envVars, vqd.writer)
		steps = append(steps, envStep)
	}

	// Step 4: Pull project configuration from Vercel
	pullStep := NewPullProjectStep("link-project", vqd.getSourcePath(), vqd.writer)
	steps = append(steps, pullStep)

	// Step 5: Build project (required for --prebuilt deployment)
	if vqd.spec.BuildCommand != "" {
		buildStep := NewBuildProjectStep(vqd.spec.BuildCommand, vqd.spec.MigrationCommand, vqd.getSourcePath(), vqd.spec.EnvVars, vqd.writer)
		steps = append(steps, buildStep)
	}

	// Step 5: Deploy the project
	deployStep := NewDeployVercelProjectStep(
		"create-project",
		vqd.getBuildStepID(),
		vqd.getSourcePath(),
	)
	steps = append(steps, deployStep)

	return steps
}

// getBuildStepID returns the ID of the build step if it exists
func (vqd *VercelQueuedDeployment) getBuildStepID() string {
	if vqd.spec.BuildCommand != "" {
		return "build-project"
	}
	return ""
}

// getSourcePath gets the source path for deployment
func (vqd *VercelQueuedDeployment) getSourcePath() string {
	if path, ok := vqd.spec.Metadata["buildContext"].(string); ok && path != "" {
		return path
	}
	return "."
}
