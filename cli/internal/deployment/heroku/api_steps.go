package heroku

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-errors/errors"

	heroku "github.com/heroku/heroku-go/v6"
	"github.com/meroxa/prod/cli/internal/deployment"
)

// HerokuAPIStep interface for all deployment steps
type HerokuAPIStep interface {
	Execute(ctx context.Context, client *HerokuClient, stepResults map[string]interface{}) (interface{}, error)
	Rollback(ctx context.Context, client *HerokuClient, stepResults map[string]interface{}) error
	GetID() string
	GetDescription() string
	GetDependencies() []string
}

// BaseStep provides common functionality for all steps
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
	return b.DependsOn
}

// CreateHerokuAppStep creates a new Heroku app
type CreateHerokuAppStep struct {
	BaseStep
	AppName string `json:"appName,omitempty"` // Optional, Heroku can auto-generate
	Region  string `json:"region"`
}

func NewCreateHerokuAppStep(id, description string, appName, region string) *CreateHerokuAppStep {
	return &CreateHerokuAppStep{
		BaseStep: BaseStep{
			ID:          id,
			Description: description,
			DependsOn:   []string{},
		},
		AppName: appName,
		Region:  region,
	}
}

func (s *CreateHerokuAppStep) Execute(ctx context.Context, client *HerokuClient, stepResults map[string]interface{}) (interface{}, error) {
	// Create the app (empty name will auto-generate)
	app, err := client.CreateApp(ctx, s.AppName, s.Region)
	if err != nil {
		return nil, errors.Errorf("failed to create app: %w", err)
	}

	// Return as CreatedResource for consistency
	return deployment.CreatedResource{
		ID:   app.ID,
		Type: "heroku_app",
		Name: app.Name,
		Metadata: map[string]interface{}{
			"url":     app.WebURL, // Use 'url' to match workflow expectations
			"git_url": app.GitURL,
			"region":  app.Region.Name,
			"app":     app, // Store full app object for later steps
		},
	}, nil
}

func (s *CreateHerokuAppStep) Rollback(ctx context.Context, client *HerokuClient, stepResults map[string]interface{}) error {
	if appResult, ok := stepResults[s.GetID()]; ok {
		if resource, ok := appResult.(deployment.CreatedResource); ok {
			_, err := client.DeleteApp(ctx, resource.Name)
			return err
		}
	}
	return errors.Errorf("could not find app for rollback")
}

// CreateHerokuAddonStep creates an addon (database, redis, etc.)
type CreateHerokuAddonStep struct {
	BaseStep
	AppID    string            `json:"appId"`
	Plan     string            `json:"plan"`
	Provider string            `json:"provider"`
	Config   map[string]string `json:"config,omitempty"`
}

func NewCreateHerokuAddonStep(id, description, appID, provider, plan string, config map[string]string, deps []string) *CreateHerokuAddonStep {
	return &CreateHerokuAddonStep{
		BaseStep: BaseStep{
			ID:          id,
			Description: description,
			DependsOn:   deps,
		},
		AppID:    appID,
		Plan:     plan,
		Provider: provider,
		Config:   config,
	}
}

func (s *CreateHerokuAddonStep) Execute(ctx context.Context, client *HerokuClient, stepResults map[string]interface{}) (interface{}, error) {
	// Resolve app name from dependencies
	appName := s.resolveAppName(stepResults)
	if appName == "" {
		return nil, errors.Errorf("could not resolve app name from step %s", s.AppID)
	}

	addon, err := client.CreateAddon(ctx, appName, s.Plan, s.Config)
	if err != nil {
		// Handle addon not found errors
		if strings.Contains(err.Error(), "Couldn't find") || strings.Contains(err.Error(), "not found") {
			if s.Provider == "postgresql" {
				// Try next tier PostgreSQL plan
				addon, err = client.CreateAddon(ctx, appName, "heroku-postgresql:essential-1", s.Config)
				if err != nil {
					return nil, errors.Errorf("failed to create PostgreSQL addon (tried essential-0 and essential-1 plans): %w", err)
				}
			} else if s.Provider == "mongodb" {
				return deployment.CreatedResource{
					ID:   "mongodb-external",
					Type: "external_service",
					Name: "MongoDB (External)",
					Metadata: map[string]interface{}{
						"message": "MongoDB addon not available. Please use MongoDB Atlas and set DATABASE_URL manually.",
					},
				}, nil
			} else {
				return nil, errors.Errorf("addon plan %s not found for %s: %w", s.Plan, s.Provider, err)
			}
		}
		if addon == nil {
			return nil, errors.Errorf("failed to create %s addon: %w", s.Provider, err)
		}
	}

	// Wait for addon to provision
	if err := s.waitForAddonReady(ctx, client, addon.ID); err != nil {
		return nil, errors.Errorf("%s addon created but failed to provision: %w", s.Provider, err)
	}

	return deployment.CreatedResource{
		ID:   addon.ID,
		Type: "heroku_addon",
		Name: addon.Name,
		Metadata: map[string]interface{}{
			"plan":   addon.Plan.Name,
			"state":  addon.State,
			"webURL": addon.WebURL,
		},
	}, nil
}

func (s *CreateHerokuAddonStep) waitForAddonReady(ctx context.Context, client *HerokuClient, addonID string) error {
	const (
		maxRetries   = 30
		initialDelay = 2 * time.Second
		maxDelay     = 30 * time.Second
		totalTimeout = 5 * time.Minute
	)

	timeoutCtx, cancel := context.WithTimeout(ctx, totalTimeout)
	defer cancel()

	delay := initialDelay
	for attempt := 1; attempt <= maxRetries; attempt++ {
		select {
		case <-timeoutCtx.Done():
			return errors.Errorf("timeout waiting for addon to provision after %v", totalTimeout)
		default:
		}

		addon, err := client.GetAddon(timeoutCtx, addonID)
		if err != nil {
			if attempt == maxRetries {
				return errors.Errorf("failed to get addon status after %d attempts: %w", maxRetries, err)
			}
		} else if addon.State == "provisioned" {
			return nil
		}

		time.Sleep(delay)
		// Exponential backoff
		delay = time.Duration(float64(delay) * 1.5)
		if delay > maxDelay {
			delay = maxDelay
		}
	}

	return errors.Errorf("addon did not provision within %d attempts", maxRetries)
}

func (s *CreateHerokuAddonStep) resolveAppName(stepResults map[string]interface{}) string {
	if appResult, ok := stepResults[s.AppID]; ok {
		if resource, ok := appResult.(deployment.CreatedResource); ok {
			return resource.Name
		}
	}
	return ""
}

func (s *CreateHerokuAddonStep) Rollback(ctx context.Context, client *HerokuClient, stepResults map[string]interface{}) error {
	if addonResult, ok := stepResults[s.GetID()]; ok {
		if resource, ok := addonResult.(deployment.CreatedResource); ok {
			// Skip rollback for external services
			if resource.Type == "external_service" {
				return nil
			}
			appName := s.resolveAppName(stepResults)
			if appName != "" {
				_, err := client.DeleteAddon(ctx, appName, resource.ID)
				return err
			}
		}
	}
	return errors.Errorf("could not find addon for rollback")
}

// ConfigureHerokuEnvStep sets environment variables
type ConfigureHerokuEnvStep struct {
	BaseStep
	AppID   string            `json:"appId"`
	EnvVars map[string]string `json:"envVars"`
}

func NewConfigureHerokuEnvStep(id, description, appID string, envVars map[string]string, deps []string) *ConfigureHerokuEnvStep {
	return &ConfigureHerokuEnvStep{
		BaseStep: BaseStep{
			ID:          id,
			Description: description,
			DependsOn:   deps,
		},
		AppID:   appID,
		EnvVars: envVars,
	}
}

func (s *ConfigureHerokuEnvStep) Execute(ctx context.Context, client *HerokuClient, stepResults map[string]interface{}) (interface{}, error) {
	appName := s.resolveAppName(stepResults, s.AppID)
	if appName == "" {
		return nil, errors.Errorf("could not resolve app name")
	}

	// Convert to pointer map for Heroku API
	configVars := make(map[string]*string)
	for k, v := range s.EnvVars {
		value := v
		configVars[k] = &value
	}

	_, err := client.UpdateConfigVars(ctx, appName, configVars)
	if err != nil {
		return nil, errors.Errorf("failed to set environment variables: %w", err)
	}

	return nil, nil
}

func (s *ConfigureHerokuEnvStep) resolveAppName(stepResults map[string]interface{}, appID string) string {
	if appResult, ok := stepResults[appID]; ok {
		if resource, ok := appResult.(deployment.CreatedResource); ok {
			return resource.Name
		}
	}
	return ""
}

func (s *ConfigureHerokuEnvStep) Rollback(ctx context.Context, client *HerokuClient, stepResults map[string]interface{}) error {
	// Environment variable rollback would require storing previous values
	// For now, we'll skip this
	return nil
}

// SetHerokuBuildpacksStep configures buildpacks for the app
type SetHerokuBuildpacksStep struct {
	BaseStep
	AppID      string   `json:"appId"`
	Buildpacks []string `json:"buildpacks"`
}

func NewSetHerokuBuildpacksStep(id, description, appID string, buildpacks []string, deps []string) *SetHerokuBuildpacksStep {
	return &SetHerokuBuildpacksStep{
		BaseStep: BaseStep{
			ID:          id,
			Description: description,
			DependsOn:   deps,
		},
		AppID:      appID,
		Buildpacks: buildpacks,
	}
}

func (s *SetHerokuBuildpacksStep) Execute(ctx context.Context, client *HerokuClient, stepResults map[string]interface{}) (interface{}, error) {
	appName := s.resolveAppName(stepResults, s.AppID)
	if appName == "" {
		return nil, errors.Errorf("could not resolve app name")
	}

	if len(s.Buildpacks) > 0 {
		_, err := client.SetBuildpacks(ctx, appName, s.Buildpacks)
		if err != nil {
			// Non-fatal - Heroku can auto-detect in many cases
			// Just log the error
			return nil, nil
		}
	}

	return nil, nil
}

func (s *SetHerokuBuildpacksStep) resolveAppName(stepResults map[string]interface{}, appID string) string {
	if appResult, ok := stepResults[appID]; ok {
		if resource, ok := appResult.(deployment.CreatedResource); ok {
			return resource.Name
		}
	}
	return ""
}

func (s *SetHerokuBuildpacksStep) Rollback(ctx context.Context, client *HerokuClient, stepResults map[string]interface{}) error {
	// Buildpack rollback would require storing previous buildpacks
	return nil
}

// GitDeployStep handles git-based deployment to Heroku
type GitDeployStep struct {
	BaseStep
	AppID            string `json:"appId"`
	BuildContext     string `json:"buildContext"`
	StartCommand     string `json:"startCommand,omitempty"`
	MigrationCommand string `json:"migrationCommand,omitempty"`
}

func NewGitDeployStep(id, description, appID, buildContext, startCommand, migrationCommand string, deps []string) *GitDeployStep {
	return &GitDeployStep{
		BaseStep: BaseStep{
			ID:          id,
			Description: description,
			DependsOn:   deps,
		},
		AppID:            appID,
		BuildContext:     buildContext,
		StartCommand:     startCommand,
		MigrationCommand: migrationCommand,
	}
}

func (s *GitDeployStep) Execute(ctx context.Context, client *HerokuClient, stepResults map[string]interface{}) (interface{}, error) {
	// Get app details from step results
	var app *heroku.App
	if appResult, ok := stepResults[s.AppID]; ok {
		if resource, ok := appResult.(deployment.CreatedResource); ok {
			if appObj, ok := resource.Metadata["app"].(*heroku.App); ok {
				app = appObj
			}
		}
	}

	if app == nil {
		return nil, errors.Errorf("could not find app details for deployment")
	}

	// Create Procfile FIRST if needed (before git operations)
	if s.StartCommand != "" {
		if err := s.createProcfile(); err != nil {
			return nil, errors.Errorf("failed to create Procfile: %w", err)
		}
	}

	// Ensure git repo and commit changes (this will now include the Procfile)
	if err := s.prepareGitRepo(); err != nil {
		return nil, errors.Errorf("failed to prepare git repository: %w", err)
	}

	// Add Heroku remote and push with the provided context
	if err := s.deployViaGit(ctx, app.GitURL, client); err != nil {
		return nil, errors.Errorf("failed to deploy via git: %w", err)
	}

	return nil, nil
}

func (s *GitDeployStep) createProcfile() error {
	procfilePath := filepath.Join(s.BuildContext, "Procfile")
	if _, err := os.Stat(procfilePath); os.IsNotExist(err) {
		var procfileContent string
		if s.MigrationCommand != "" {
			// Include release phase for migrations
			procfileContent = fmt.Sprintf("release: %s\nweb: %s", s.MigrationCommand, s.StartCommand)
		} else {
			procfileContent = fmt.Sprintf("web: %s", s.StartCommand)
		}
		if err := os.WriteFile(procfilePath, []byte(procfileContent), 0644); err != nil {
			return err
		}
	}
	return nil
}

func (s *GitDeployStep) prepareGitRepo() error {
	// Check if .git directory exists
	gitDir := filepath.Join(s.BuildContext, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		// Initialize git repo
		cmd := exec.Command("git", "init")
		cmd.Dir = s.BuildContext
		if err := cmd.Run(); err != nil {
			return errors.Errorf("failed to initialize git: %w", err)
		}
	}

	// Ensure git user config is set (local to this repo)
	// Check if user.email is set
	cmd := exec.Command("git", "config", "user.email")
	cmd.Dir = s.BuildContext
	output, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) == "" {
		// Set a default email for Heroku deployment
		cmd = exec.Command("git", "config", "user.email", "deploy@prod-cli.local")
		cmd.Dir = s.BuildContext
		if err := cmd.Run(); err != nil {
			return errors.Errorf("failed to set git user.email: %w", err)
		}
	}

	// Check if user.name is set
	cmd = exec.Command("git", "config", "user.name")
	cmd.Dir = s.BuildContext
	output, err = cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) == "" {
		// Set a default name for Heroku deployment
		cmd = exec.Command("git", "config", "user.name", "Prod CLI Deploy")
		cmd.Dir = s.BuildContext
		if err := cmd.Run(); err != nil {
			return errors.Errorf("failed to set git user.name: %w", err)
		}
	}

	// Add and commit all files
	cmd = exec.Command("git", "add", ".")
	cmd.Dir = s.BuildContext
	if err := cmd.Run(); err != nil {
		return errors.Errorf("failed to add files: %w", err)
	}

	cmd = exec.Command("git", "commit", "-m", "Deploy to Heroku")
	cmd.Dir = s.BuildContext
	output, err = cmd.CombinedOutput()
	if err != nil {
		// Check if there's nothing to commit
		if !strings.Contains(string(output), "nothing to commit") {
			return errors.Errorf("failed to commit: %w", err)
		}
	}

	return nil
}

func (s *GitDeployStep) deployViaGit(ctx context.Context, gitURL string, client *HerokuClient) error {
	writer := client.GetWriter()

	// Add or update Heroku remote
	if err := s.setupGitRemote(gitURL); err != nil {
		return err
	}

	// Push to Heroku with timeout
	fmt.Fprintln(writer, "🚀 Pushing to Heroku (this may take a few minutes)...")
	fmt.Fprintln(writer, "📦 Building and deploying application...")

	// Create a context with 10-minute timeout for the git push
	// Use the provided context as parent so cancellation propagates
	pushCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	// Try pushing to main branch first
	var buildOutput bytes.Buffer
	cmd := exec.CommandContext(pushCtx, "git", "push", "heroku", "HEAD:main", "--force")
	cmd.Dir = s.BuildContext
	cmd.Stdout = &buildOutput
	cmd.Stderr = &buildOutput

	pushErr := cmd.Run()
	outputStr := buildOutput.String()

	// Handle timeout
	if pushCtx.Err() == context.DeadlineExceeded {
		fmt.Fprintln(writer, "❌ Deployment timed out after 10 minutes")
		return &HTTPError{
			StatusCode: 408, // Request Timeout
			Message:    "Deployment timed out after 10 minutes",
		}
	}

	// Check if push succeeded
	if pushErr == nil {
		return s.reportSuccess(writer, outputStr)
	}

	// Check for specific error conditions that should not be retried
	if s.isPermanentError(outputStr) {
		s.reportError(writer, outputStr)
		return &HTTPError{
			StatusCode: 400, // Bad Request - permanent failure
			Message:    s.extractErrorMessage(outputStr),
		}
	}

	// For other errors, return them as temporary (will be retried by workflow)
	s.reportError(writer, outputStr)
	return errors.Errorf("git push failed: %w", pushErr)
}

func (s *GitDeployStep) setupGitRemote(gitURL string) error {
	// First, try to remove existing remote if it exists
	cmd := exec.Command("git", "remote", "remove", "heroku")
	cmd.Dir = s.BuildContext
	cmd.Run() // Ignore error - remote might not exist

	// Add the remote
	cmd = exec.Command("git", "remote", "add", "heroku", gitURL)
	cmd.Dir = s.BuildContext
	if err := cmd.Run(); err != nil {
		return errors.Errorf("failed to add git remote: %w", err)
	}

	return nil
}

func (s *GitDeployStep) isPermanentError(output string) bool {
	// These errors indicate configuration issues that won't be fixed by retry
	permanentErrors := []string{
		"No such app",
		"You do not have access",
		"Invalid credentials",
		"Authentication failed",
		"Permission denied",
		"does not appear to be a git repository",
		"Could not resolve host",
		"buildpack",
		"failed to compile",
	}

	for _, errStr := range permanentErrors {
		if strings.Contains(output, errStr) {
			return true
		}
	}

	return false
}

func (s *GitDeployStep) extractErrorMessage(output string) string {
	lines := strings.Split(output, "\n")
	// Look for the actual error message in the output
	for _, line := range lines {
		if strings.Contains(line, "error:") || strings.Contains(line, "Error:") ||
			strings.Contains(line, "remote:") && strings.Contains(line, "!") {
			return strings.TrimSpace(line)
		}
	}
	// If no specific error found, return last non-empty line
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return strings.TrimSpace(lines[i])
		}
	}
	return "Deployment failed"
}

func (s *GitDeployStep) reportSuccess(writer io.Writer, output string) error {
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, "https://") && strings.Contains(line, ".herokuapp.com") {
			fmt.Fprintf(writer, "🌐 App URL: %s\n", strings.TrimSpace(line))
		} else if strings.Contains(line, "deployed to Heroku") {
			fmt.Fprintf(writer, "✅ %s\n", strings.TrimSpace(line))
		}
	}
	return nil
}

func (s *GitDeployStep) reportError(writer io.Writer, output string) {
	lines := strings.Split(output, "\n")
	startIdx := 0
	if len(lines) > 20 {
		startIdx = len(lines) - 20
	}
	fmt.Fprintf(writer, "\n❌ Deployment failed. Last %d lines:\n", len(lines)-startIdx)
	for i := startIdx; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "" {
			fmt.Fprintf(writer, "  %s\n", lines[i])
		}
	}
}

func (s *GitDeployStep) Rollback(ctx context.Context, client *HerokuClient, stepResults map[string]interface{}) error {
	// Git deployment rollback would require reverting to a previous release
	// Heroku supports this via releases API, but we'll skip for now
	return nil
}

// ScaleHerokuDynosStep scales the web dynos
type ScaleHerokuDynosStep struct {
	BaseStep
	AppID    string `json:"appId"`
	Quantity int    `json:"quantity"`
	Size     string `json:"size"`
}

func NewScaleHerokuDynosStep(id, description, appID string, quantity int, size string, deps []string) *ScaleHerokuDynosStep {
	return &ScaleHerokuDynosStep{
		BaseStep: BaseStep{
			ID:          id,
			Description: description,
			DependsOn:   deps,
		},
		AppID:    appID,
		Quantity: quantity,
		Size:     size,
	}
}

func (s *ScaleHerokuDynosStep) Execute(ctx context.Context, client *HerokuClient, stepResults map[string]interface{}) (interface{}, error) {
	appName := s.resolveAppName(stepResults, s.AppID)
	if appName == "" {
		return nil, errors.Errorf("could not resolve app name")
	}

	quantity := s.Quantity
	size := s.Size
	_, err := client.UpdateFormation(ctx, appName, "web", &quantity, &size)
	if err != nil {
		return nil, errors.Errorf("failed to scale dynos: %w", err)
	}

	return nil, nil
}

func (s *ScaleHerokuDynosStep) resolveAppName(stepResults map[string]interface{}, appID string) string {
	if appResult, ok := stepResults[appID]; ok {
		if resource, ok := appResult.(deployment.CreatedResource); ok {
			return resource.Name
		}
	}
	return ""
}

func (s *ScaleHerokuDynosStep) Rollback(ctx context.Context, client *HerokuClient, stepResults map[string]interface{}) error {
	// Scaling rollback would scale down to 0
	appName := s.resolveAppName(stepResults, s.AppID)
	if appName != "" {
		zero := 0
		_, err := client.UpdateFormation(ctx, appName, "web", &zero, nil)
		return err
	}
	return nil
}
