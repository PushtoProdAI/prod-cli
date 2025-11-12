package vercel

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"time"

	"github.com/go-errors/errors"

	"github.com/meroxa/prod/cli/internal/deployment"
)

// Default timeout for build operations
const defaultBuildTimeout = 15 * time.Minute

type VercelAPIStep = deployment.Step[VercelClient]

type BaseStep = deployment.BaseStep

// CreateVercelProjectStep creates a new Vercel project
type CreateVercelProjectStep struct {
	BaseStep
	projectName string
	framework   string
	envVars     map[string]string
}

func NewCreateVercelProjectStep(projectName, framework string, envVars map[string]string) *CreateVercelProjectStep {
	return &CreateVercelProjectStep{
		BaseStep: BaseStep{
			ID:          "create-project",
			Description: fmt.Sprintf("Create Vercel project: %s", projectName),
			DependsOn:   []string{},
		},
		projectName: projectName,
		framework:   framework,
		envVars:     envVars,
	}
}

func (s *CreateVercelProjectStep) Execute(ctx context.Context, client VercelClient, stepResults map[string]any) (any, error) {
	project, err := client.CreateProject(CreateProjectRequest{
		Name:      s.projectName,
		Framework: s.framework,
		EnvVars:   s.envVars,
	})
	if err != nil {
		return nil, errors.Errorf("failed to create project: %w", err)
	}

	// Return as CreatedResource for consistency
	return deployment.CreatedResource{
		ID:   project.ID,
		Type: "vercel_project",
		Name: project.Name,
		Metadata: map[string]any{
			"account_id": project.AccountID,
			"created_at": project.CreatedAt,
		},
	}, nil
}

func (s *CreateVercelProjectStep) Rollback(ctx context.Context, client VercelClient, stepResults map[string]any) error {
	// Get the project ID from step results
	if projectResult, ok := stepResults[s.GetID()]; ok {
		if resource, ok := projectResult.(deployment.CreatedResource); ok {
			return client.DeleteProject(resource.ID)
		}
	}
	return errors.Errorf("could not find project ID for rollback")
}

// LinkVercelProjectStep links the current directory to a Vercel project
type LinkVercelProjectStep struct {
	BaseStep
	projectDependency string
	sourcePath        string
	writer            io.Writer
}

func NewLinkVercelProjectStep(projectDependency, sourcePath string, writer io.Writer) *LinkVercelProjectStep {
	return &LinkVercelProjectStep{
		BaseStep: BaseStep{
			ID:          "link-project",
			Description: "Link directory to Vercel project",
			DependsOn:   []string{projectDependency},
		},
		projectDependency: projectDependency,
		sourcePath:        sourcePath,
		writer:            writer,
	}
}

func (s *LinkVercelProjectStep) Execute(ctx context.Context, client VercelClient, stepResults map[string]any) (any, error) {
	// Get project ID from dependency
	projectResult, ok := stepResults[s.projectDependency]
	if !ok {
		return nil, errors.Errorf("project dependency %s not found in results", s.projectDependency)
	}

	resource, ok := projectResult.(deployment.CreatedResource)
	if !ok {
		return nil, errors.Errorf("project dependency result is not a CreatedResource")
	}

	// Change to source directory for linking
	originalDir, err := os.Getwd()
	if err != nil {
		return nil, errors.Errorf("failed to get current directory: %w", err)
	}

	if s.sourcePath != "." && s.sourcePath != "" {
		if err := os.Chdir(s.sourcePath); err != nil {
			return nil, errors.Errorf("failed to change to source directory: %w", err)
		}
		defer os.Chdir(originalDir)
	}

	if err := client.LinkProject(resource.Name); err != nil {
		return nil, errors.Errorf("failed to link project: %w", err)
	}

	fmt.Fprintf(s.writer, "  🔗 Project linked successfully\n")
	return map[string]string{"status": "linked"}, nil
}

func (s *LinkVercelProjectStep) Rollback(ctx context.Context, client VercelClient, stepResults map[string]any) error {
	// No specific rollback needed for linking
	return nil
}

// PullProjectStep pulls project configuration from Vercel
type PullProjectStep struct {
	BaseStep
	linkDependency string
	sourcePath     string
	writer         io.Writer
}

func NewPullProjectStep(linkDependency, sourcePath string, writer io.Writer) *PullProjectStep {
	return &PullProjectStep{
		BaseStep: BaseStep{
			ID:          "pull-project",
			Description: "Pull project configuration from Vercel",
			DependsOn:   []string{linkDependency},
		},
		linkDependency: linkDependency,
		sourcePath:     sourcePath,
		writer:         writer,
	}
}

func (s *PullProjectStep) Execute(ctx context.Context, client VercelClient, stepResults map[string]any) (any, error) {
	// Change to source directory for pulling
	originalDir, err := os.Getwd()
	if err != nil {
		return nil, errors.Errorf("failed to get current directory: %w", err)
	}

	if s.sourcePath != "." && s.sourcePath != "" {
		if err := os.Chdir(s.sourcePath); err != nil {
			return nil, errors.Errorf("failed to change to source directory: %w", err)
		}
		defer os.Chdir(originalDir)
	}

	if err := client.PullProject(); err != nil {
		return nil, errors.Errorf("failed to pull project configuration: %w", err)
	}

	fmt.Fprintf(s.writer, "  📥 Project configuration pulled successfully\n")
	return map[string]string{"status": "pulled"}, nil
}

func (s *PullProjectStep) Rollback(ctx context.Context, client VercelClient, stepResults map[string]any) error {
	// No specific rollback needed for pulling configuration
	return nil
}

// SetEnvironmentVariablesStep sets environment variables for a project
type SetEnvironmentVariablesStep struct {
	BaseStep
	projectDependency string
	linkDependency    string
	sourcePath        string
	envVars           []deployment.EnvVar
	writer            io.Writer
}

func NewSetEnvironmentVariablesStep(projectDependency, linkDependency, sourcePath string, envVars []deployment.EnvVar, writer io.Writer) *SetEnvironmentVariablesStep {
	// Count sensitive vs non-sensitive for description
	sensitiveCount := 0
	for _, ev := range envVars {
		if ev.Sensitive {
			sensitiveCount++
		}
	}

	description := fmt.Sprintf("Set %d environment variables", len(envVars))
	if sensitiveCount > 0 {
		description = fmt.Sprintf("Set %d environment variables (%d sensitive)", len(envVars), sensitiveCount)
	}

	return &SetEnvironmentVariablesStep{
		BaseStep: BaseStep{
			ID:          "set-env-vars",
			Description: description,
			DependsOn:   []string{projectDependency, linkDependency},
		},
		projectDependency: projectDependency,
		linkDependency:    linkDependency,
		sourcePath:        sourcePath,
		envVars:           envVars,
		writer:            writer,
	}
}

func (s *SetEnvironmentVariablesStep) Execute(ctx context.Context, client VercelClient, stepResults map[string]any) (any, error) {
	// Get project ID from dependency
	projectResult, ok := stepResults[s.projectDependency]
	if !ok {
		return nil, errors.Errorf("project dependency %s not found in results", s.projectDependency)
	}

	resource, ok := projectResult.(deployment.CreatedResource)
	if !ok {
		return nil, errors.Errorf("project dependency result is not a CreatedResource")
	}

	// Change to source directory
	originalDir, err := os.Getwd()
	if err != nil {
		return nil, errors.Errorf("failed to get current directory: %w", err)
	}

	if s.sourcePath != "." && s.sourcePath != "" {
		if err := os.Chdir(s.sourcePath); err != nil {
			return nil, errors.Errorf("failed to change to source directory: %w", err)
		}
		defer os.Chdir(originalDir)
	}

	if err := client.SetEnvironmentVariables(resource.ID, s.envVars); err != nil {
		return nil, errors.Errorf("failed to set environment variables: %w", err)
	}

	fmt.Fprintf(s.writer, "  ✅ Environment variables set\n")
	return map[string]int{"count": len(s.envVars)}, nil
}

func (s *SetEnvironmentVariablesStep) Rollback(ctx context.Context, client VercelClient, stepResults map[string]any) error {
	// Environment variables rollback is not easily implemented with Vercel CLI
	return nil
}

// BuildProjectStep runs the build command for the project
type BuildProjectStep struct {
	BaseStep
	buildCommand     string
	migrationCommand string
	sourcePath       string
	envVars          []deployment.EnvVar
	writer           io.Writer
	production       bool
}

func NewBuildProjectStep(buildCommand, migrationCommand, sourcePath string, envVars []deployment.EnvVar, writer io.Writer, production bool) *BuildProjectStep {
	return &BuildProjectStep{
		BaseStep: BaseStep{
			ID:          "build-project",
			Description: fmt.Sprintf("Build project: %s", buildCommand),
			DependsOn:   []string{},
		},
		buildCommand:     buildCommand,
		migrationCommand: migrationCommand,
		sourcePath:       sourcePath,
		envVars:          envVars,
		writer:           writer,
		production:       production,
	}
}

func (s *BuildProjectStep) Execute(ctx context.Context, client VercelClient, stepResults map[string]any) (any, error) {
	// Check if source path exists
	if _, err := os.Stat(s.sourcePath); err != nil {
		return nil, errors.Errorf("source path does not exist: %s", s.sourcePath)
	}

	// Change to source directory to run build
	originalDir, err := os.Getwd()
	if err != nil {
		return nil, errors.Errorf("failed to get current directory: %w", err)
	}

	if s.sourcePath != "." && s.sourcePath != "" {
		if err := os.Chdir(s.sourcePath); err != nil {
			return nil, errors.Errorf("failed to change to source directory: %w", err)
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
		// Try npm install first with streaming output
		installCmd := exec.CommandContext(buildCtx, "npm", "install")
		installCmd.Env = env
		installCmd.Stdout = s.writer
		installCmd.Stderr = s.writer
		installErr := installCmd.Run()

		if installErr != nil {
			// Check if it was a timeout
			if buildCtx.Err() == context.DeadlineExceeded {
				return nil, errors.Errorf("npm install timed out after %v", defaultBuildTimeout)
			}

			// If npm install fails, try yarn install
			fmt.Fprintf(s.writer, "  📦 npm install failed, trying yarn install...\n")
			yarnCmd := exec.CommandContext(buildCtx, "yarn", "install")
			yarnCmd.Env = env
			yarnCmd.Stdout = s.writer
			yarnCmd.Stderr = s.writer
			yarnErr := yarnCmd.Run()

			if yarnErr != nil {
				// Check if yarn also timed out
				if buildCtx.Err() == context.DeadlineExceeded {
					return nil, errors.Errorf("yarn install timed out after %v", defaultBuildTimeout)
				}
				fmt.Fprintf(s.writer, "  ❌ Dependency installation failed\n")
				return nil, errors.Errorf("failed to install dependencies: npm error: %w, yarn error: %w", installErr, yarnErr)
			} else {
				fmt.Fprintf(s.writer, "  ✅ Dependencies installed with yarn\n")
			}
		} else {
			fmt.Fprintf(s.writer, "  ✅ Dependencies installed with npm\n")
		}
	} else {
		fmt.Fprintf(s.writer, "  ⚠️  No package.json found, skipping dependency installation\n")
	}

	// Run migrations if specified (before build)
	if s.migrationCommand != "" {
		if err := s.runMigration(buildCtx, env); err != nil {
			return nil, err
		}
	}

	// Convert deployment.EnvVar to vercel.EnvVar
	vercelEnvVars := make([]EnvVar, len(s.envVars))
	for i, env := range s.envVars {
		vercelEnvVars[i] = EnvVar{Name: env.Name, Value: env.Value}
	}

	// Use Vercel build
	fmt.Fprintf(s.writer, "  🏗️  Running Vercel build...\n")
	if err := client.BuildProject(vercelEnvVars, s.production); err != nil {
		return nil, errors.Errorf("build failed: %w", err)
	}

	fmt.Fprintf(s.writer, "  ✅ Build completed successfully\n")
	return map[string]string{"status": "built"}, nil
}

func (s *BuildProjectStep) runMigration(ctx context.Context, env []string) error {
	fmt.Fprintf(s.writer, "  🔄 Running database migrations...\n")
	fmt.Fprintf(s.writer, "     Command: %s\n", s.migrationCommand)

	// Parse the migration command (support shell commands)
	cmd := exec.CommandContext(ctx, "sh", "-c", s.migrationCommand)
	cmd.Env = env
	cmd.Stdout = s.writer
	cmd.Stderr = s.writer

	if err := cmd.Run(); err != nil {
		fmt.Fprintf(s.writer, "  ❌ Migration failed: %v\n", err)
		return fmt.Errorf("migration failed: %w", err)
	}

	fmt.Fprintf(s.writer, "  ✅ Migrations completed successfully\n")
	return nil
}

func (s *BuildProjectStep) Rollback(ctx context.Context, client VercelClient, stepResults map[string]any) error {
	// Build artifacts can be left in place - they don't need rollback
	return nil
}

// DeployVercelProjectStep deploys the project
type DeployVercelProjectStep struct {
	BaseStep
	projectDependency  string
	buildDependency    string
	sourcePath         string
	deployToProduction bool
}

func NewDeployVercelProjectStep(projectDependency, buildDependency, sourcePath string, deployToProduction bool) *DeployVercelProjectStep {
	dependencies := []string{projectDependency}
	if buildDependency != "" {
		dependencies = append(dependencies, buildDependency)
	}

	return &DeployVercelProjectStep{
		BaseStep: BaseStep{
			ID:          "deploy-project",
			Description: "Deploy project to Vercel",
			DependsOn:   dependencies,
		},
		projectDependency:  projectDependency,
		buildDependency:    buildDependency,
		sourcePath:         sourcePath,
		deployToProduction: deployToProduction,
	}
}

func (s *DeployVercelProjectStep) Execute(ctx context.Context, client VercelClient, stepResults map[string]any) (any, error) {
	// Get project ID from dependency
	projectResult, ok := stepResults[s.projectDependency]
	if !ok {
		return nil, errors.Errorf("project dependency %s not found in results", s.projectDependency)
	}

	resource, ok := projectResult.(deployment.CreatedResource)
	if !ok {
		return nil, errors.Errorf("project dependency result is not a CreatedResource")
	}

	// Change to source directory for deployment
	originalDir, err := os.Getwd()
	if err != nil {
		return nil, errors.Errorf("failed to get current directory: %w", err)
	}

	if s.sourcePath != "." && s.sourcePath != "" {
		if err := os.Chdir(s.sourcePath); err != nil {
			return nil, errors.Errorf("failed to change to source directory: %w", err)
		}
		defer os.Chdir(originalDir)
	}

	deploy, err := client.DeployProject(resource.Name, s.deployToProduction)
	if err != nil {
		return nil, errors.Errorf("failed to deploy project: %w", err)
	}

	// Store both URLs:
	// - url: Production alias for liveness checks
	// - deployment_url: Deployment-specific URL with hash for promotion
	metadata := map[string]any{
		"url":            deploy.URL,           // Production alias for liveness checks
		"deployment_url": deploy.DeploymentURL, // For promotion
		"project_id":     deploy.ProjectID,
		"ready":          deploy.Ready,
		"created_at":     deploy.CreatedAt,
	}

	return deployment.CreatedResource{
		ID:       deploy.ID,
		Type:     "vercel_deployment",
		Name:     fmt.Sprintf("deployment-%s", deploy.ID),
		Metadata: metadata,
	}, nil
}

func (s *DeployVercelProjectStep) Rollback(ctx context.Context, client VercelClient, stepResults map[string]any) error {
	// Vercel deployments are immutable - no rollback needed
	return nil
}

type PromoteDeploymentStep struct {
	BaseStep
	deploymentDependency string
	projectDependency    string
	sourcePath           string
}

func NewPromoteDeploymentStep(deploymentDependency, projectDependency, sourcePath string) *PromoteDeploymentStep {
	return &PromoteDeploymentStep{
		BaseStep: BaseStep{
			ID:          "promote-deployment",
			Description: "Promote deployment to production",
			DependsOn:   []string{deploymentDependency, projectDependency},
		},
		deploymentDependency: deploymentDependency,
		projectDependency:    projectDependency,
		sourcePath:           sourcePath,
	}
}

func (s *PromoteDeploymentStep) Execute(ctx context.Context, client VercelClient, stepResults map[string]any) (any, error) {
	// Change to source directory so vercel CLI can find .vercel config
	originalDir, err := os.Getwd()
	if err != nil {
		return nil, errors.Errorf("failed to get current directory: %w", err)
	}

	if s.sourcePath != "." && s.sourcePath != "" {
		if err := os.Chdir(s.sourcePath); err != nil {
			return nil, errors.Errorf("failed to change to source directory: %w", err)
		}
		defer os.Chdir(originalDir)
	}

	deploymentResult, ok := stepResults[s.deploymentDependency]
	if !ok {
		return nil, errors.Errorf("deployment dependency %s not found in results", s.deploymentDependency)
	}

	resource, ok := deploymentResult.(deployment.CreatedResource)
	if !ok {
		return nil, errors.Errorf("deployment dependency result is not a CreatedResource")
	}

	// Use the deployment-specific URL (with hash) for promotion, not the production alias
	deploymentURL, ok := resource.Metadata["deployment_url"].(string)
	if !ok || deploymentURL == "" {
		// Fallback to "url" for backwards compatibility
		deploymentURL, ok = resource.Metadata["url"].(string)
		if !ok || deploymentURL == "" {
			return nil, errors.Errorf("deployment URL not found in metadata")
		}
	}

	slog.Info("Promoting deployment to production", "deployment_url", deploymentURL, "deployment_id", resource.ID)

	// Get project name from project dependency
	projectResult, ok := stepResults[s.projectDependency]
	if !ok {
		return nil, errors.Errorf("project dependency %s not found in results", s.projectDependency)
	}

	projectResource, ok := projectResult.(deployment.CreatedResource)
	if !ok {
		return nil, errors.Errorf("project dependency result is not a CreatedResource")
	}

	err = client.PromoteDeployment(deploymentURL, projectResource.Name)
	if err != nil {
		return nil, errors.Errorf("failed to promote deployment: %w", err)
	}

	// Use the original production alias URL from deployment metadata for liveness checks
	// This was already set correctly by the deploy step
	productionURL, ok := resource.Metadata["url"].(string)
	if !ok || productionURL == "" {
		return nil, errors.Errorf("production URL not found in deployment metadata")
	}

	slog.Info("Promotion completed, using production alias for liveness checks", "url", productionURL)

	// Return the production alias URL as a CreatedResource so it gets used for health checks
	return deployment.CreatedResource{
		ID:   resource.ID,
		Type: "vercel_deployment",
		Name: resource.Name,
		Metadata: map[string]any{
			"url":        productionURL,
			"project_id": resource.Metadata["project_id"],
			"ready":      true,
			"created_at": resource.Metadata["created_at"],
			"promoted":   true,
		},
	}, nil
}

func (s *PromoteDeploymentStep) Rollback(ctx context.Context, client VercelClient, stepResults map[string]any) error {
	return nil
}
