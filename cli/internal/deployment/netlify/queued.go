package netlify

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/go-errors/errors"

	"github.com/meroxa/prod/cli/internal/deployment"
)

// NetlifyQueuedDeployment handles step-by-step deployments to Netlify
// This deployment strategy creates resources one at a time with progress tracking
type NetlifyQueuedDeployment struct {
	client NetlifyClient
	spec   *deployment.DeploymentSpec
	writer io.Writer
}

// NewNetlifyQueuedDeployment creates a new queued deployment for Netlify
func NewNetlifyQueuedDeployment(client NetlifyClient, spec *deployment.DeploymentSpec, writer io.Writer) *NetlifyQueuedDeployment {
	return &NetlifyQueuedDeployment{
		client: client,
		spec:   spec,
		writer: writer,
	}
}

// Deploy performs the queued deployment to Netlify
func (nqd *NetlifyQueuedDeployment) Deploy(ctx context.Context) ([]deployment.CreatedResource, error) {
	steps := nqd.GenerateAPISteps()

	var createdResources []deployment.CreatedResource
	stepResults := make(map[string]interface{})

	if nqd.spec.IsUpdate && nqd.spec.ExistingProjectID != "" {
		stepResults["existing-site"] = deployment.CreatedResource{
			ID:   nqd.spec.ExistingProjectID,
			Type: "netlify_site",
			Name: nqd.spec.Name,
		}
	}

	var executedSteps []NetlifyAPIStep

	for _, step := range steps {
		fmt.Fprintf(nqd.writer, "🔄 Executing: %s...\n", step.GetDescription())

		result, err := step.Execute(ctx, nqd.client, stepResults)
		if err != nil {
			fmt.Fprintf(nqd.writer, "❌ Failed: %s - %v\n", step.GetDescription(), err)

			// Attempt rollback of executed steps
			if len(executedSteps) > 0 {
				fmt.Fprintf(nqd.writer, "🔄 Rolling back...\n")
				for i := len(executedSteps) - 1; i >= 0; i-- {
					rollbackStep := executedSteps[i]
					fmt.Fprintf(nqd.writer, "  ↩️  Rolling back: %s\n", rollbackStep.GetDescription())
					if rbErr := rollbackStep.Rollback(ctx, nqd.client, stepResults); rbErr != nil {
						fmt.Fprintf(nqd.writer, "  ⚠️  Rollback failed: %v\n", rbErr)
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

		fmt.Fprintf(nqd.writer, "✅ Completed: %s\n", step.GetDescription())
	}

	// Print final deployment URL if available
	if deployResult, ok := stepResults["deploy-site"]; ok {
		if resource, ok := deployResult.(deployment.CreatedResource); ok {
			if url, ok := resource.Metadata["url"].(string); ok {
				fmt.Fprintf(nqd.writer, "\n🎉 Deployment successful!\n")
				fmt.Fprintf(nqd.writer, "🌐 Site URL: %s\n", url)
			}
		}
	}

	return createdResources, nil
}

// GenerateAPISteps generates the deployment steps for Netlify
func (nqd *NetlifyQueuedDeployment) GenerateAPISteps() []NetlifyAPIStep {
	var steps []NetlifyAPIStep

	if nqd.spec.IsUpdate {

		if len(nqd.spec.EnvVars) > 0 {
			// Pass the full EnvVar objects so sensitive flag is preserved
			envStep := NewSetEnvironmentVariablesStep("existing-site", "", nqd.getSourcePath(), nqd.spec.EnvVars, nqd.writer)
			steps = append(steps, envStep)
		}

		if nqd.spec.BuildCommand != "" {
			buildStep := NewBuildProjectStep(nqd.spec.BuildCommand, nqd.getSourcePath(), nqd.spec.EnvVars, nqd.writer)
			steps = append(steps, buildStep)
		}

		deployStep := NewDeployNetlifySiteStep(
			"existing-site",
			nqd.getBuildStepID(),
			nqd.getPublishDir(),
			nqd.getFunctionsDir(),
		)
		steps = append(steps, deployStep)

		return steps
	}

	// Fresh deployment flow below

	// Step 1: Initialize git repository
	initGitStep := NewInitializeGitRepoStep(nqd.getSourcePath())
	steps = append(steps, initGitStep)

	// Step 2: Create site (without environment variables - CLI doesn't support it)
	createSiteStep := NewCreateNetlifySiteStep(nqd.spec.Name, nil)
	steps = append(steps, createSiteStep)

	// Step 3: Link CLI to site (always required)
	linkStep := NewLinkNetlifySiteStep("create-site", nqd.getSourcePath(), nqd.writer)
	steps = append(steps, linkStep)

	// Step 4: Set all environment variables after linking
	if len(nqd.spec.EnvVars) > 0 {
		// Pass the full EnvVar objects so sensitive flag is preserved
		envStep := NewSetEnvironmentVariablesStep("create-site", "link-site", nqd.getSourcePath(), nqd.spec.EnvVars, nqd.writer)
		steps = append(steps, envStep)
	}

	// Step 5: Update build settings (optional, mainly for UI visibility)
	if nqd.spec.BuildCommand != "" || nqd.getPublishDir() != "." {
		buildSettingsStep := NewUpdateBuildSettingsStep("create-site", nqd.spec.BuildCommand, nqd.getPublishDir())
		steps = append(steps, buildSettingsStep)
	}

	// Step 6: Build project (validation step)
	if nqd.spec.BuildCommand != "" {
		buildStep := NewBuildProjectStep(nqd.spec.BuildCommand, nqd.getSourcePath(), nqd.spec.EnvVars, nqd.writer)
		steps = append(steps, buildStep)
	}

	// Step 7: Deploy the site
	deployStep := NewDeployNetlifySiteStep(
		"create-site",
		nqd.getBuildStepID(),
		nqd.getPublishDir(),
		nqd.getFunctionsDir(),
	)
	steps = append(steps, deployStep)

	return steps
}

// getBuildStepID returns the ID of the build step if it exists
func (nqd *NetlifyQueuedDeployment) getBuildStepID() string {
	if nqd.spec.BuildCommand != "" {
		return "build-project"
	}
	return ""
}

// getSourcePath gets the source path for deployment
func (nqd *NetlifyQueuedDeployment) getSourcePath() string {
	if path, ok := nqd.spec.Metadata["buildContext"].(string); ok && path != "" {
		return path
	}
	return "."
}

// getPublishDir determines the publish directory
func (nqd *NetlifyQueuedDeployment) getPublishDir() string {
	// Check if explicitly set in metadata
	if dir, ok := nqd.spec.Metadata["publishDir"].(string); ok && dir != "" {
		return dir
	}

	// Check common build output directories
	sourcePath := nqd.getSourcePath()
	for _, dir := range GetCommonBuildDirs() {
		if _, err := os.Stat(filepath.Join(sourcePath, dir)); err == nil {
			return dir
		}
	}

	// Default to current directory
	return "."
}

// getFunctionsDir determines the functions directory
func (nqd *NetlifyQueuedDeployment) getFunctionsDir() string {
	// Check if explicitly set in metadata
	if dir, ok := nqd.spec.Metadata["functionsDir"].(string); ok && dir != "" {
		return dir
	}

	// Common functions directories
	commonDirs := GetCommonFunctionDirs()

	sourcePath := nqd.getSourcePath()
	for _, dir := range commonDirs {
		if _, err := os.Stat(filepath.Join(sourcePath, dir)); err == nil {
			return dir
		}
	}

	// No functions directory found
	return ""
}

func (nqd *NetlifyQueuedDeployment) GetCurrentDeployment(ctx context.Context) (*deployment.DeploymentInfo, error) {
	if nqd.spec.ExistingProjectID == "" {
		return nil, errors.Errorf("no site ID available")
	}

	return &deployment.DeploymentInfo{
		ID:     nqd.spec.ExistingProjectID,
		Status: "active",
	}, nil
}

func (nqd *NetlifyQueuedDeployment) GetPreviousDeployment(ctx context.Context) (*deployment.DeploymentInfo, error) {
	if nqd.spec.ExistingProjectID == "" {
		return nil, errors.Errorf("no site ID available")
	}

	if !nqd.spec.IsUpdate {
		return nil, errors.Errorf("no previous deployment found for site (only 1 deployment exists)")
	}

	return &deployment.DeploymentInfo{
		ID:     nqd.spec.ExistingProjectID,
		Status: "previous",
	}, nil
}

func (nqd *NetlifyQueuedDeployment) Rollback(ctx context.Context, targetDeploymentID string) error {
	if nqd.spec.ExistingProjectID == "" {
		return errors.Errorf("no site ID available for rollback")
	}

	siteID := nqd.spec.ExistingProjectID

	return rollbackNetlifySiteDeploy(ctx, siteID)
}

func rollbackNetlifySiteDeploy(ctx context.Context, siteID string) error {
	cmd := exec.CommandContext(ctx, "netlify", "api", "rollbackSiteDeploy", "--data", fmt.Sprintf(`{"site_id": "%s"}`, siteID))

	output, err := cmd.CombinedOutput()
	if err != nil {
		return errors.Errorf("failed to rollback Netlify site: %w\nOutput: %s", err, string(output))
	}

	return nil
}
