package vercel

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"
)

// CLIVercelClient implements the VercelClient interface using Vercel CLI
type CLIVercelClient struct{}

// NewCLIVercelClient creates a new CLI-based Vercel client
func NewCLIVercelClient() *CLIVercelClient {
	return &CLIVercelClient{}
}

// ensureVercelCLI checks if vercel CLI is installed
func (c *CLIVercelClient) ensureVercelCLI() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "vercel", "--version")
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("vercel CLI version check timed out")
		}
		return fmt.Errorf("vercel CLI is not installed. Install with: npm install -g vercel")
	}
	return nil
}

// CreateProject creates a new Vercel project using CLI
func (c *CLIVercelClient) CreateProject(req CreateProjectRequest) (*VercelProject, error) {
	if err := c.ensureVercelCLI(); err != nil {
		return nil, err
	}

	// For now, we'll defer project creation to the link step
	// since vercel deploy will create the project automatically
	project := &VercelProject{
		ID:        req.Name, // Use name as ID for now since actual ID isn't available until after creation
		Name:      req.Name,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	return project, nil
}

// GetProject retrieves project information using CLI
func (c *CLIVercelClient) GetProject(projectID string) (*VercelProject, error) {
	if err := c.ensureVercelCLI(); err != nil {
		return nil, err
	}

	// Vercel CLI doesn't have a direct get project command
	// We'll return a basic response for now
	return &VercelProject{
		ID:        projectID,
		Name:      projectID,
		UpdatedAt: time.Now(),
	}, nil
}

// DeleteProject deletes a Vercel project using CLI
func (c *CLIVercelClient) DeleteProject(projectID string) error {
	if err := c.ensureVercelCLI(); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), linkTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "vercel", "remove", projectID, "--yes")
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("project deletion timed out after %v", linkTimeout)
		}
		return fmt.Errorf("failed to delete project: %w\nOutput: %s", err, string(output))
	}

	return nil
}

// LinkProject links the current directory to a Vercel project using CLI
func (c *CLIVercelClient) LinkProject(projectID string) error {
	if err := c.ensureVercelCLI(); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), linkTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "vercel", "link", "--yes", "--project", projectID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("project linking timed out after %v", linkTimeout)
		}
		return fmt.Errorf("failed to link project: %w\nOutput: %s", err, string(output))
	}

	slog.Info("Successfully linked project to current directory", "projectID", projectID)
	return nil
}

// PullProject pulls the project configuration from Vercel using CLI
func (c *CLIVercelClient) PullProject() error {
	if err := c.ensureVercelCLI(); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), pullTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "vercel", "pull", "--yes")
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("vercel pull timed out after %v", pullTimeout)
		}
		return fmt.Errorf("failed to pull project configuration: %w\nOutput: %s", err, string(output))
	}

	slog.Info("Successfully pulled project configuration")
	return nil
}

// BuildProject builds the project locally with environment variables
func (c *CLIVercelClient) BuildProject(envVars []EnvVar) error {
	if err := c.ensureVercelCLI(); err != nil {
		return err
	}

	// Set up environment variables for the build
	env := os.Environ()
	for _, envVar := range envVars {
		env = append(env, fmt.Sprintf("%s=%s", envVar.Name, envVar.Value))
	}

	// Run vercel build
	ctx, cancel := context.WithTimeout(context.Background(), buildTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "vercel", "build")
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("build timed out after %v", buildTimeout)
		}
		return fmt.Errorf("build failed: %w\nOutput: %s", err, string(output))
	}

	slog.Info("Build completed successfully")
	return nil
}

// DeployProject deploys a project to Vercel using CLI
func (c *CLIVercelClient) DeployProject(projectID string) (*VercelDeployment, error) {
	if err := c.ensureVercelCLI(); err != nil {
		return nil, err
	}

	// Build deploy command - using prebuilt archive as specified
	args := []string{"deploy", "--prebuilt", "--archive=tgz"}

	// Run deployment with timeout
	ctx, cancel := context.WithTimeout(context.Background(), deployTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "vercel", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("deployment timed out after %v", deployTimeout)
		}
		return nil, fmt.Errorf("deployment failed: %w\nOutput: %s", err, string(output))
	}

	// Get the latest production URL using vercel projects command
	deployURL, err := c.getLatestProductionURL(projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to get deployment URL: %w", err)
	}

	// Extract deployment ID from URL (format: https://<id>-<team>.vercel.app)
	deploymentID := ""
	if strings.Contains(deployURL, "://") {
		parts := strings.Split(deployURL, "://")
		if len(parts) > 1 {
			domain := strings.Split(parts[1], ".")[0]
			deploymentID = strings.Split(domain, "-")[0]
		}
	}

	// Convert to our VercelDeployment struct
	deployment := &VercelDeployment{
		ID:        deploymentID,
		URL:       deployURL,
		ProjectID: projectID,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Ready:     true,
	}

	return deployment, nil
}

// getLatestProductionURL retrieves the latest production URL for a project using vercel projects command
func (c *CLIVercelClient) getLatestProductionURL(projectName string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "vercel", "projects")
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("vercel projects command timed out")
		}
		return "", fmt.Errorf("failed to get projects: %w\nOutput: %s", err, string(output))
	}

	// Parse the output to find the project URL
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) < 2 {
		return "", fmt.Errorf("unexpected vercel projects output format: %s", string(output))
	}

	// Skip the header line and find the project
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Check if this line starts with our project name
		if strings.HasPrefix(line, projectName) {
			// Find the URL by looking for https:// in the line
			// Use a more robust approach by splitting on multiple spaces
			parts := strings.Split(line, "  ")
			for _, part := range parts {
				part = strings.TrimSpace(part)
				if strings.HasPrefix(part, "https://") {
					return part, nil
				}
			}

			// Fallback: use regex to find URL in the line
			if urlStart := strings.Index(line, "https://"); urlStart != -1 {
				urlEnd := strings.Index(line[urlStart:], " ")
				if urlEnd == -1 {
					urlEnd = len(line)
				} else {
					urlEnd += urlStart
				}
				return line[urlStart:urlEnd], nil
			}
		}
	}

	return "", fmt.Errorf("could not find production URL for project %s in output: %s", projectName, string(output))
}

// GetDeployment retrieves deployment information
func (c *CLIVercelClient) GetDeployment(deploymentID string) (*VercelDeployment, error) {
	// Vercel CLI doesn't have a get deployment command easily accessible
	// Return a basic response indicating it's ready since CLI deploys synchronously
	return &VercelDeployment{
		ID:    deploymentID,
		Ready: true,
	}, nil
}

// SetEnvironmentVariables sets environment variables for a project
func (c *CLIVercelClient) SetEnvironmentVariables(projectID string, vars map[string]string) error {
	if err := c.ensureVercelCLI(); err != nil {
		return err
	}

	for key, value := range vars {
		if err := c.setEnvVar(key, value); err != nil {
			return fmt.Errorf("failed to set env var %s: %w", key, err)
		}
	}

	return nil
}

// setEnvVar sets a single environment variable
func (c *CLIVercelClient) setEnvVar(key, value string) error {
	ctx, cancel := context.WithTimeout(context.Background(), envVarTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "vercel", "env", "add", key, "production")
	cmd.Stdin = strings.NewReader(value + "\n")
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("setting env var %s timed out after %v", key, envVarTimeout)
		}
		slog.Error("Failed to set env var", "key", key, "error", err, "output", string(output))
		return fmt.Errorf("failed to set env var: %w\nOutput: %s", err, string(output))
	}
	return nil
}
