package netlify

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"

	"github.com/meroxa/prod/cli/internal/deployment"
)

// NetlifyAPIStep represents a single deployment step (matching Fly.io's interface pattern)
type NetlifyAPIStep interface {
	Execute(ctx context.Context, client NetlifyClient, stepResults map[string]interface{}) (interface{}, error)
	Rollback(ctx context.Context, client NetlifyClient, stepResults map[string]interface{}) error
	GetID() string
	GetDescription() string
	GetDependencies() []string
}

// BaseStep provides common functionality for all steps
type BaseStep struct {
	ID           string
	Description  string
	Dependencies []string
}

func (b *BaseStep) GetID() string {
	return b.ID
}

func (b *BaseStep) GetDescription() string {
	return b.Description
}

func (b *BaseStep) GetDependencies() []string {
	if b.Dependencies == nil {
		return []string{}
	}
	return b.Dependencies
}

// CreateNetlifySiteStep creates a new Netlify site
type CreateNetlifySiteStep struct {
	BaseStep
	siteName string
	envVars  map[string]string
}

func NewCreateNetlifySiteStep(siteName string, envVars map[string]string) *CreateNetlifySiteStep {
	return &CreateNetlifySiteStep{
		BaseStep: BaseStep{
			ID:           "create-site",
			Description:  fmt.Sprintf("Create Netlify site: %s", siteName),
			Dependencies: []string{},
		},
		siteName: siteName,
		envVars:  envVars,
	}
}

func (s *CreateNetlifySiteStep) Execute(ctx context.Context, client NetlifyClient, stepResults map[string]interface{}) (interface{}, error) {
	// Validate site name format
	if s.siteName != "" {
		// Netlify site names must be lowercase, alphanumeric with hyphens
		// They cannot start or end with a hyphen
		for i, ch := range s.siteName {
			if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || (ch == '-' && i > 0 && i < len(s.siteName)-1)) {
				return nil, fmt.Errorf("invalid site name '%s': must be lowercase alphanumeric with hyphens (not at start/end)", s.siteName)
			}
		}
	}

	site, err := client.CreateSite(CreateSiteRequest{
		Name:    s.siteName,
		EnvVars: s.envVars,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create site: %w", err)
	}

	// Return as CreatedResource for consistency
	return deployment.CreatedResource{
		ID:   site.ID,
		Type: "netlify_site",
		Name: site.Name,
		Metadata: map[string]interface{}{
			"url":        site.URL,
			"admin_url":  site.AdminURL,
			"created_at": site.CreatedAt,
		},
	}, nil
}

func (s *CreateNetlifySiteStep) Rollback(ctx context.Context, client NetlifyClient, stepResults map[string]interface{}) error {
	// Get the site ID from step results
	if siteResult, ok := stepResults[s.GetID()]; ok {
		if resource, ok := siteResult.(deployment.CreatedResource); ok {
			return client.DeleteSite(resource.ID)
		}
	}
	return fmt.Errorf("could not find site ID for rollback")
}

// BuildProjectStep runs the build command for the project
type BuildProjectStep struct {
	BaseStep
	buildCommand string
	sourcePath   string
	envVars      []deployment.EnvVar
	writer       io.Writer
}

func NewBuildProjectStep(buildCommand, sourcePath string, envVars []deployment.EnvVar, writer io.Writer) *BuildProjectStep {
	return &BuildProjectStep{
		BaseStep: BaseStep{
			ID:           "build-project",
			Description:  fmt.Sprintf("Build project: %s", buildCommand),
			Dependencies: []string{},
		},
		buildCommand: buildCommand,
		sourcePath:   sourcePath,
		envVars:      envVars,
		writer:       writer,
	}
}

func (s *BuildProjectStep) Execute(ctx context.Context, client NetlifyClient, stepResults map[string]interface{}) (interface{}, error) {
	// Check if source path exists
	if _, err := os.Stat(s.sourcePath); err != nil {
		return nil, fmt.Errorf("source path does not exist: %s", s.sourcePath)
	}

	// Change to source directory to run build
	originalDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get current directory: %w", err)
	}

	if s.sourcePath != "." && s.sourcePath != "" {
		if err := os.Chdir(s.sourcePath); err != nil {
			return nil, fmt.Errorf("failed to change to source directory: %w", err)
		}
		defer os.Chdir(originalDir)
	}

	// Set up environment variables for the build
	env := os.Environ()
	for _, envVar := range s.envVars {
		env = append(env, fmt.Sprintf("%s=%s", envVar.Name, envVar.Value))
	}

	// Create context with timeout
	buildCtx, cancel := context.WithTimeout(ctx, defaultBuildTimeout)
	defer cancel()

	// Install dependencies first
	fmt.Fprintf(s.writer, "  📦 Installing dependencies...\n")

	// Check if package.json exists
	if _, err := os.Stat("package.json"); err == nil {
		// Try npm install first
		installCmd := exec.CommandContext(buildCtx, "npm", "install")
		installCmd.Env = env
		installOutput, installErr := installCmd.CombinedOutput()

		if installErr != nil {
			// If npm install fails, try yarn install
			fmt.Fprintf(s.writer, "  📦 npm install failed, trying yarn install...\n")
			yarnCmd := exec.CommandContext(buildCtx, "yarn", "install")
			yarnCmd.Env = env
			yarnOutput, yarnErr := yarnCmd.CombinedOutput()

			if yarnErr != nil {
				// Both failed, show both outputs
				fmt.Fprintf(s.writer, "  ❌ npm install failed:\n%s\n", string(installOutput))
				fmt.Fprintf(s.writer, "  ❌ yarn install failed:\n%s\n", string(yarnOutput))
				return nil, fmt.Errorf("failed to install dependencies: npm error: %w, yarn error: %w", installErr, yarnErr)
			} else {
				fmt.Fprintf(s.writer, "  ✅ Dependencies installed with yarn\n")
			}
		} else {
			fmt.Fprintf(s.writer, "  ✅ Dependencies installed with npm\n")
		}
	} else {
		fmt.Fprintf(s.writer, "  ⚠️  No package.json found, skipping dependency installation\n")
	}

	// Always try netlify build first
	fmt.Fprintf(s.writer, "  📦 Running Netlify build...\n")
	cmd := exec.CommandContext(buildCtx, "netlify", "build")
	cmd.Env = env

	// Capture output
	output, err := cmd.CombinedOutput()

	if err != nil {
		// Check if it was a timeout
		if buildCtx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("build timed out after %v", defaultBuildTimeout)
		}

		// If netlify build fails, try running the build command directly
		if s.buildCommand != "" {
			fmt.Fprintf(s.writer, "  📦 Running custom build command: %s\n", s.buildCommand)

			// Use shell to handle complex commands properly
			shellCmd := exec.CommandContext(buildCtx, "sh", "-c", s.buildCommand)
			shellCmd.Env = env

			buildOutput, buildErr := shellCmd.CombinedOutput()

			// Write output to writer
			if len(buildOutput) > 0 {
				fmt.Fprintf(s.writer, "  Build output:\n%s\n", string(buildOutput))
			}

			if buildErr != nil {
				if buildCtx.Err() == context.DeadlineExceeded {
					return nil, fmt.Errorf("build timed out after %v", defaultBuildTimeout)
				}
				return nil, fmt.Errorf("build failed: %w\nOutput: %s", buildErr, string(buildOutput))
			}
		} else {
			// No custom build command, netlify build failed
			fmt.Fprintf(s.writer, "  Build output:\n%s\n", string(output))
			return nil, fmt.Errorf("netlify build failed: %w", err)
		}
	} else {
		// Netlify build succeeded, show output
		if len(output) > 0 {
			fmt.Fprintf(s.writer, "  Build output:\n%s\n", string(output))
		}
	}

	fmt.Fprintf(s.writer, "  ✅ Build completed successfully\n")

	// Return build configuration for the deploy step
	return map[string]interface{}{
		"build_command": s.buildCommand,
		"source_path":   s.sourcePath,
		"env_vars":      s.envVars,
		"built":         true,
	}, nil
}

func (s *BuildProjectStep) Rollback(ctx context.Context, client NetlifyClient, stepResults map[string]interface{}) error {
	// No rollback needed for build validation
	return nil
}

// DeployNetlifySiteStep deploys a site to Netlify
type DeployNetlifySiteStep struct {
	BaseStep
	siteStepID   string // ID of the step that created the site
	buildStepID  string // ID of the step that ran the build
	publishDir   string
	functionsDir string
}

func NewDeployNetlifySiteStep(siteStepID, buildStepID, publishDir, functionsDir string) *DeployNetlifySiteStep {
	deps := []string{siteStepID}
	if buildStepID != "" {
		deps = append(deps, buildStepID)
	}
	return &DeployNetlifySiteStep{
		BaseStep: BaseStep{
			ID:           "deploy-site",
			Description:  "Deploy to Netlify",
			Dependencies: deps,
		},
		siteStepID:   siteStepID,
		buildStepID:  buildStepID,
		publishDir:   publishDir,
		functionsDir: functionsDir,
	}
}

func (s *DeployNetlifySiteStep) Execute(ctx context.Context, client NetlifyClient, stepResults map[string]interface{}) (interface{}, error) {
	// Get site ID from create step
	siteID := ""
	if siteResult, ok := stepResults[s.siteStepID]; ok {
		if resource, ok := siteResult.(deployment.CreatedResource); ok {
			siteID = resource.ID
		}
	}

	if siteID == "" {
		return nil, fmt.Errorf("could not find site ID from step %s", s.siteStepID)
	}

	// Get build configuration
	sourcePath := "."
	if buildResult, ok := stepResults[s.buildStepID]; ok {
		if config, ok := buildResult.(map[string]interface{}); ok {
			if path, ok := config["source_path"].(string); ok {
				sourcePath = path
			}
		}
	}

	// Change to source directory to run deployment
	originalDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get current directory: %w", err)
	}

	if sourcePath != "." && sourcePath != "" {
		if err := os.Chdir(sourcePath); err != nil {
			return nil, fmt.Errorf("failed to change to source directory: %w", err)
		}
		defer os.Chdir(originalDir)
	}

	// Construct relative paths (now that we're in the source directory)
	deployPath := s.publishDir
	functionsPath := s.discoverFunctionsDir()

	// Deploy using CLI client
	deploy, err := client.DeploySite(siteID, deployPath, functionsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to deploy site: %w", err)
	}

	// Return deployment resource
	return deployment.CreatedResource{
		ID:   deploy.ID,
		Type: "netlify_deployment",
		Name: deploy.Name,
		Metadata: map[string]interface{}{
			"url":        deploy.URL,
			"deploy_url": deploy.DeployURL,
			"site_id":    deploy.SiteID,
			"state":      deploy.State,
		},
	}, nil
}

func (s *DeployNetlifySiteStep) Rollback(ctx context.Context, client NetlifyClient, stepResults map[string]interface{}) error {
	// Netlify deployments can be rolled back through the UI or API
	// For now, we'll just log that rollback would require a previous deploy ID
	return fmt.Errorf("deployment rollback not implemented - use Netlify UI to rollback")
}

// discoverFunctionsDir dynamically discovers the functions directory at execution time
// This is important because the build step may create new function directories (e.g., .netlify/functions for SvelteKit)
func (s *DeployNetlifySiteStep) discoverFunctionsDir() string {
	// First check if we already have a functions directory from step generation
	if s.functionsDir != "" {
		// Check if it still exists
		if _, err := os.Stat(s.functionsDir); err == nil {
			return s.functionsDir
		}
	}

	// Check common function directories (especially those created during build)
	commonDirs := GetCommonFunctionDirs()
	for _, dir := range commonDirs {
		if _, err := os.Stat(dir); err == nil {
			// Log when we discover a functions directory that wasn't detected during step generation
			if s.functionsDir == "" {
				log.Printf("Discovered functions directory during deployment: %s", dir)
			}
			return dir
		}
	}

	// No functions directory found
	return ""
}

// LinkNetlifySiteStep links the Netlify CLI to a site
type LinkNetlifySiteStep struct {
	BaseStep
	siteStepID string
	sourcePath string
	writer     io.Writer
}

func NewLinkNetlifySiteStep(siteStepID string, sourcePath string, writer io.Writer) *LinkNetlifySiteStep {
	return &LinkNetlifySiteStep{
		BaseStep: BaseStep{
			ID:           "link-site",
			Description:  "Link CLI to Netlify site",
			Dependencies: []string{siteStepID},
		},
		siteStepID: siteStepID,
		sourcePath: sourcePath,
		writer:     writer,
	}
}

func (s *LinkNetlifySiteStep) Execute(ctx context.Context, client NetlifyClient, stepResults map[string]interface{}) (interface{}, error) {
	// Get site ID from create step
	siteID := ""
	if siteResult, ok := stepResults[s.siteStepID]; ok {
		if resource, ok := siteResult.(deployment.CreatedResource); ok {
			siteID = resource.ID
		}
	}

	if siteID == "" {
		return nil, fmt.Errorf("could not find site ID from step %s", s.siteStepID)
	}

	originalDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get current directory: %w", err)
	}

	if s.sourcePath != "." && s.sourcePath != "" {
		if err := os.Chdir(s.sourcePath); err != nil {
			return nil, fmt.Errorf("failed to change to source directory: %w", err)
		}
		defer os.Chdir(originalDir)
	}

	// Remove .netlify directory to fix intermittent issues with env vars
	err = os.RemoveAll(".netlify")
	if err != nil {
		log.Printf("Warning: failed to remove .netlify directory: %v", err)
	}

	err = client.LinkSite(siteID)
	if err != nil {
		return nil, fmt.Errorf("failed to link CLI to site: %w", err)
	}

	// Return success indicator
	return map[string]interface{}{
		"site_id": siteID,
		"linked":  true,
	}, nil
}

func (s *LinkNetlifySiteStep) Rollback(ctx context.Context, client NetlifyClient, stepResults map[string]interface{}) error {
	// Remove .netlify directory to unlink
	originalDir, err := os.Getwd()
	if err != nil {
		log.Printf("Warning: failed to get current directory during rollback: %v", err)
		return nil
	}

	if s.sourcePath != "." && s.sourcePath != "" {
		if err := os.Chdir(s.sourcePath); err != nil {
			log.Printf("Warning: failed to change to source directory during rollback: %v", err)
			return nil
		}
		defer os.Chdir(originalDir)
	}

	err = os.RemoveAll(".netlify")
	if err != nil {
		log.Printf("Warning: failed to remove .netlify directory during rollback: %v", err)
	}

	return nil
}

// SetEnvironmentVariablesStep sets environment variables for a Netlify site
type SetEnvironmentVariablesStep struct {
	BaseStep
	siteStepID string
	envVars    map[string]string
	sourcePath string
	writer     io.Writer
}

func NewSetEnvironmentVariablesStep(siteStepID string, linkStepID string, sourcePath string, envVars map[string]string, writer io.Writer) *SetEnvironmentVariablesStep {
	return &SetEnvironmentVariablesStep{
		BaseStep: BaseStep{
			ID:           "set-env-vars",
			Description:  fmt.Sprintf("Set %d environment variables", len(envVars)),
			Dependencies: []string{siteStepID, linkStepID},
		},
		siteStepID: siteStepID,
		envVars:    envVars,
		sourcePath: sourcePath,
		writer:     writer,
	}
}

func (s *SetEnvironmentVariablesStep) Execute(ctx context.Context, client NetlifyClient, stepResults map[string]interface{}) (interface{}, error) {
	// Get site ID from create step
	siteID := ""
	if siteResult, ok := stepResults[s.siteStepID]; ok {
		if resource, ok := siteResult.(deployment.CreatedResource); ok {
			siteID = resource.ID
		}
	}

	if siteID == "" {
		return nil, fmt.Errorf("could not find site ID from step %s", s.siteStepID)
	}

	// Set environment variables
	err := client.SetEnvironmentVariables(siteID, s.envVars)
	if err != nil {
		return nil, fmt.Errorf("failed to set environment variables: %w", err)
	}

	// Return success indicator
	return map[string]interface{}{
		"site_id":  siteID,
		"env_vars": s.envVars,
	}, nil
}

func (s *SetEnvironmentVariablesStep) Rollback(ctx context.Context, client NetlifyClient, stepResults map[string]interface{}) error {
	// Get site ID from previous step
	siteID := ""
	if siteResult, ok := stepResults[s.siteStepID]; ok {
		if resource, ok := siteResult.(deployment.CreatedResource); ok {
			siteID = resource.ID
		}
	}

	if siteID == "" {
		return fmt.Errorf("could not find site ID for rollback")
	}

	// Unset each environment variable
	// Note: We're unsetting all vars we tried to set, even if some failed
	for key := range s.envVars {
		// Using exec directly since client doesn't have unset method
		cmd := exec.Command("netlify", "env:unset", key, "--site", siteID)
		if err := cmd.Run(); err != nil {
			// Log error but continue trying to unset others
			fmt.Fprintf(s.writer, "  ⚠️ Warning: failed to unset env var: %s: %v\n", key, err)
		}
	}

	return nil
}

// UpdateBuildSettingsStep updates build settings for a Netlify site
type UpdateBuildSettingsStep struct {
	BaseStep
	siteStepID   string
	buildCommand string
	publishDir   string
}

func NewUpdateBuildSettingsStep(siteStepID, buildCommand, publishDir string) *UpdateBuildSettingsStep {
	return &UpdateBuildSettingsStep{
		BaseStep: BaseStep{
			ID:           "update-build-settings",
			Description:  "Update build settings",
			Dependencies: []string{siteStepID},
		},
		siteStepID:   siteStepID,
		buildCommand: buildCommand,
		publishDir:   publishDir,
	}
}

func (s *UpdateBuildSettingsStep) Execute(ctx context.Context, client NetlifyClient, stepResults map[string]interface{}) (interface{}, error) {
	// Get site ID from create step
	siteID := ""
	if siteResult, ok := stepResults[s.siteStepID]; ok {
		if resource, ok := siteResult.(deployment.CreatedResource); ok {
			siteID = resource.ID
		}
	}

	if siteID == "" {
		return nil, fmt.Errorf("could not find site ID from step %s", s.siteStepID)
	}

	// Update build settings
	err := client.UpdateBuildSettings(siteID, BuildSettings{
		Command:    s.buildCommand,
		PublishDir: s.publishDir,
	})
	if err != nil {
		// This is often not critical for CLI deployments
		// Log warning but don't fail
		fmt.Printf("Warning: could not update build settings: %v\n", err)
	}

	return map[string]interface{}{
		"site_id":       siteID,
		"build_command": s.buildCommand,
		"publish_dir":   s.publishDir,
	}, nil
}

func (s *UpdateBuildSettingsStep) Rollback(ctx context.Context, client NetlifyClient, stepResults map[string]interface{}) error {
	// Build settings rollback would require storing previous settings
	return nil
}
