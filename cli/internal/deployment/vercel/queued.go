package vercel

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/go-errors/errors"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
)

// A VercelQueuedDeployment can tear itself down, so the destroy dispatch
// (agent/deployment.go) recognizes Vercel as supported instead of reporting
// "Teardown isn't supported for Vercel yet".
var _ deployment.Destroyer = (*VercelQueuedDeployment)(nil)

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

func (vqd *VercelQueuedDeployment) GetCurrentDeployment(ctx context.Context) (*deployment.DeploymentInfo, error) {
	return GetCurrentVercelDeployment(ctx, vqd.client, vqd.spec.Name, vqd.getSourcePath())
}

func (vqd *VercelQueuedDeployment) GetPreviousDeployment(ctx context.Context) (*deployment.DeploymentInfo, error) {
	return GetPreviousVercelDeployment(ctx, vqd.client, vqd.spec.Name, vqd.getSourcePath())
}

func (vqd *VercelQueuedDeployment) Rollback(ctx context.Context, targetDeploymentID string) error {
	return RollbackVercelDeployment(ctx, vqd.client, targetDeploymentID, vqd.getSourcePath())
}

// Destroy tears down the Vercel deployment by deleting the whole project.
//
// Vercel teardown is keyed by project NAME, not id: `vercel project rm <name>`
// removes the project and all its deployments. spec.Name comes from the plan and
// matches the created project. The `prj_…` id from .vercel/project.json is
// deliberately NOT used here — `vercel remove <prj_id>` targets deployments, not the
// project, and would misbehave (the semantic trap this adapter avoids).
//
// This adapter provisions no databases; any Vercel-managed storage (Postgres/KV) is
// a separate resource with its own lifecycle, so destroy orphans nothing this
// adapter created.
func (vqd *VercelQueuedDeployment) Destroy(ctx context.Context) error {
	if vqd.spec.Name == "" {
		return errors.Errorf("no project name available to destroy Vercel deployment")
	}

	slog.Info("Destroying Vercel project", "project", vqd.spec.Name)

	if err := vqd.client.DeleteProjectByName(vqd.spec.Name); err != nil {
		// Idempotent teardown: a project that's already gone is a success, not a
		// failure. `vercel project rm` reports "No such project exists" in that case.
		if isVercelNotFound(err) {
			slog.Info("Vercel project already deleted or not found", "project", vqd.spec.Name)
			return nil
		}
		return errors.Errorf("failed to destroy Vercel project %q: %w", vqd.spec.Name, err)
	}

	slog.Info("Vercel project destroyed", "project", vqd.spec.Name)

	return nil
}

// isVercelNotFound reports whether a DeleteProjectByName error signals the project is
// already gone, so destroy can treat it as a no-op.
func isVercelNotFound(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such project") ||
		strings.Contains(msg, "not found") ||
		strings.Contains(msg, "does not exist")
}

func (vqd *VercelQueuedDeployment) Deploy(ctx context.Context) ([]deployment.CreatedResource, error) {
	steps := vqd.GenerateAPISteps()

	var createdResources []deployment.CreatedResource
	stepResults := make(map[string]any)

	// For updates, inject existing project info
	if vqd.spec.IsUpdate && vqd.spec.ExistingProjectID != "" {
		existingProject := deployment.CreatedResource{
			ID:   vqd.spec.ExistingProjectID,
			Type: "vercel_project",
			Name: vqd.spec.Name,
		}
		stepResults["existing-project"] = existingProject
		createdResources = append(createdResources, existingProject)
	}

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
			// For promote-deployment step, replace the previous deployment resource
			if step.GetID() == "promote-deployment" {
				// Find and replace the deployment resource
				for i, cr := range createdResources {
					if cr.Type == "vercel_deployment" {
						createdResources[i] = resource
						break
					}
				}
			} else {
				createdResources = append(createdResources, resource)
			}
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

	projectStepID := "existing-project"

	if vqd.spec.IsUpdate {
		// For updates, .vercel directory already exists with project config
		// No need to create or link project
	} else {
		createProjectStep := NewCreateVercelProjectStep(vqd.spec.Name, "", nil)
		steps = append(steps, createProjectStep)

		linkStep := NewLinkVercelProjectStep("create-project", vqd.getSourcePath(), vqd.writer)
		steps = append(steps, linkStep)
		projectStepID = "create-project" // Use create-project for downstream steps since it has the CreatedResource
	}

	if len(vqd.spec.EnvVars) > 0 {
		// Pass the full EnvVar objects so sensitive flag is preserved
		envStep := NewSetEnvironmentVariablesStep(projectStepID, "link-project", vqd.getSourcePath(), vqd.spec.EnvVars, vqd.writer)
		steps = append(steps, envStep)
	}

	pullStep := NewPullProjectStep("link-project", vqd.getSourcePath(), vqd.writer)
	steps = append(steps, pullStep)

	if vqd.spec.BuildCommand != "" {
		buildStep := NewBuildProjectStep(vqd.spec.BuildCommand, vqd.spec.MigrationCommand, vqd.getSourcePath(), vqd.spec.EnvVars, vqd.writer, true)
		steps = append(steps, buildStep)
	}

	// Always deploy to production
	deployStep := NewDeployVercelProjectStep(
		projectStepID,
		vqd.getBuildStepID(),
		vqd.getSourcePath(),
		true, // Always deploy to production
	)
	steps = append(steps, deployStep)

	// Explicitly promote the deployment to make it the current production deployment
	promoteStep := NewPromoteDeploymentStep("deploy-project", projectStepID, vqd.getSourcePath())
	steps = append(steps, promoteStep)

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
