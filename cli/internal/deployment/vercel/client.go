package vercel

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/go-errors/errors"
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
			return errors.Errorf("vercel CLI version check timed out")
		}
		return errors.Errorf("vercel CLI is not installed. Install with: npm install -g vercel")
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
			return errors.Errorf("project deletion timed out after %v", linkTimeout)
		}
		return errors.Errorf("failed to delete project: %w\nOutput: %s", err, string(output))
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
			return errors.Errorf("project linking timed out after %v", linkTimeout)
		}
		return errors.Errorf("failed to link project: %w\nOutput: %s", err, string(output))
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
			return errors.Errorf("vercel pull timed out after %v", pullTimeout)
		}
		return errors.Errorf("failed to pull project configuration: %w\nOutput: %s", err, string(output))
	}

	slog.Info("Successfully pulled project configuration")
	return nil
}

// BuildProject builds the project locally with environment variables
func (c *CLIVercelClient) BuildProject(envVars []EnvVar, production bool) error {
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

	args := []string{"build"}
	if production {
		args = append(args, "--prod")
		slog.Info("Building for production")
	} else {
		slog.Info("Building for preview")
	}

	cmd := exec.CommandContext(ctx, "vercel", args...)
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return errors.Errorf("build timed out after %v", buildTimeout)
		}
		return errors.Errorf("build failed: %w\nOutput: %s", err, string(output))
	}

	slog.Info("Build completed successfully")
	return nil
}

// DeployProject deploys a project to Vercel using CLI
func (c *CLIVercelClient) DeployProject(projectID string, production bool) (*VercelDeployment, error) {
	if err := c.ensureVercelCLI(); err != nil {
		return nil, err
	}

	// Build deploy command - using prebuilt archive
	args := []string{"deploy", "--prebuilt", "--archive=tgz"}
	if production {
		args = append(args, "--prod")
		slog.Info("Deploying to production")
	} else {
		slog.Info("Deploying to preview")
	}

	// Run deployment with timeout
	ctx, cancel := context.WithTimeout(context.Background(), deployTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "vercel", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, errors.Errorf("deployment timed out after %v", deployTimeout)
		}
		return nil, errors.Errorf("deployment failed: %w\nOutput: %s", err, string(output))
	}

	slog.Info("Vercel deploy output", "output", string(output))

	// Parse BOTH the deployment-specific URL and production alias from deploy output
	// - Deployment URL (with hash): needed for promotion
	// - Production alias: needed for liveness checks
	var deploymentURL string      // The unique deployment URL with hash
	var productionAliasURL string // The production alias URL

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "vercel.app") && strings.Contains(line, "https://") {
			if idx := strings.Index(line, "https://"); idx >= 0 {
				urlPart := line[idx:]
				potentialURL := strings.Fields(urlPart)[0]

				// Count hyphens to distinguish deployment URLs from aliases
				// Deployment URLs have more hyphens (e.g., project-abc123-team.vercel.app)
				// Production aliases have fewer (e.g., project-name.vercel.app)
				hyphenCount := strings.Count(strings.Split(potentialURL, ".")[0], "-")

				if hyphenCount >= 2 {
					// Likely a deployment-specific URL with hash
					if deploymentURL == "" || hyphenCount > strings.Count(strings.Split(deploymentURL, ".")[0], "-") {
						deploymentURL = potentialURL
					}
				} else {
					// Likely a production alias
					if productionAliasURL == "" {
						productionAliasURL = potentialURL
					}
				}
			}
		}
	}

	// Prefer deployment URL, but fall back to production alias if needed
	deployURL := deploymentURL
	if deployURL == "" {
		deployURL = productionAliasURL
	}

	if deployURL == "" {
		return nil, errors.Errorf("could not find deployment URL in output: %s", string(output))
	}

	// Get production alias if we don't have it from the output
	if productionAliasURL == "" {
		productionAliasURL, err = c.getProductionURLFromAliases(projectID)
		if err != nil {
			slog.Warn("Failed to get production URL from aliases", "error", err)
			// Fallback to using the deployment URL
			productionAliasURL = deployURL
		}
	}

	slog.Info("Parsed URLs from deployment", "deployment_url", deploymentURL, "production_alias", productionAliasURL)

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
	// URL = production alias (for liveness checks)
	// DeploymentURL = deployment-specific URL with hash (for promotion)
	deployment := &VercelDeployment{
		ID:            deploymentID,
		URL:           productionAliasURL,
		DeploymentURL: deploymentURL,
		ProjectID:     projectID,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
		Ready:         true,
	}

	slog.Info("Deployment completed", "deployment_id", deploymentID, "deployment_url", deploymentURL, "production_alias", productionAliasURL, "project_id", projectID)

	return deployment, nil
}

// PromoteDeployment promotes a deployment to production
func (c *CLIVercelClient) PromoteDeployment(deploymentURL, projectName string) error {
	if err := c.ensureVercelCLI(); err != nil {
		return err
	}

	slog.Info("Running vercel promote", "deployment_url", deploymentURL, "project", projectName)

	// Run vercel promote with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "vercel", "promote", deploymentURL, "--yes")
	output, err := cmd.CombinedOutput()
	outputStr := string(output)

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return errors.Errorf("promote timed out after 30s")
		}

		// Check if deployment is already current by inspecting the error message
		// Vercel returns a specific error when trying to promote an already-current deployment
		isAlreadyCurrent := strings.Contains(outputStr, "is already the current") ||
			strings.Contains(outputStr, "already current") ||
			strings.Contains(outputStr, "already promoted")

		if isAlreadyCurrent {
			slog.Info("Deployment is already current, skipping promotion", "deployment_url", deploymentURL)
			return nil // Success - deployment is already live
		}

		return errors.Errorf("promote failed: %w\nOutput: %s", err, outputStr)
	}

	slog.Info("Vercel promote completed successfully", "output", outputStr)
	return nil
}

// getProductionURLFromAliases gets the production URL by reading the first alias
func (c *CLIVercelClient) getProductionURLFromAliases(projectName string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "vercel", "alias", "ls")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", errors.Errorf("failed to list aliases: %w\nOutput: %s", err, string(output))
	}

	// Parse the first alias for this project
	// Format: source    url    age
	// We want the URL field (second field)
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)

		if line == "" || strings.HasPrefix(line, "Vercel CLI") || strings.Contains(line, "Alias") || strings.HasPrefix(line, "source") {
			continue
		}

		// Check if line contains the project name
		if strings.Contains(strings.ToLower(line), strings.ToLower(projectName)) {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				alias := fields[1]
				if strings.HasSuffix(alias, ".vercel.app") {
					return "https://" + alias, nil
				}
			}
		}
	}

	return "", errors.Errorf("no alias found for project %s", projectName)
}

// getLatestProductionURL retrieves the latest production URL for a project
func (c *CLIVercelClient) getLatestProductionURL(projectName string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Use vercel alias ls to get the production domain aliases
	cmd := exec.CommandContext(ctx, "vercel", "alias", "ls")
	output, err := cmd.CombinedOutput()
	if err != nil {
		slog.Warn("Failed to get aliases with vercel alias ls", "error", err, "output", string(output))
	} else {
		slog.Info("Vercel alias ls output", "output", string(output))

		// Parse aliases to find the production domain for this project
		lines := strings.Split(strings.TrimSpace(string(output)), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "Vercel CLI") || strings.Contains(line, "Alias") || strings.Contains(line, "---") {
				continue
			}

			// Look for lines containing the project name
			if !strings.Contains(line, projectName) {
				continue
			}

			// Format: alias     deployment     created
			fields := strings.Fields(line)
			if len(fields) > 0 {
				alias := fields[0]
				// Check if it's a vercel.app domain
				if strings.HasSuffix(alias, ".vercel.app") {
					// Prefer non-deployment-specific URLs (without hash suffixes)
					// Deployment URLs have format: project-hash-team.vercel.app
					// Production URLs have format: project.vercel.app or project-name.vercel.app
					parts := strings.Split(alias, ".")
					if len(parts) > 0 {
						subdomain := parts[0]
						// Count hyphens - deployment URLs typically have multiple hyphens
						hyphenCount := strings.Count(subdomain, "-")
						// If it has fewer hyphens and contains the project name, it's likely the production URL
						if hyphenCount <= 2 && strings.Contains(subdomain, projectName) {
							productionURL := "https://" + alias
							slog.Info("Found production alias", "url", productionURL, "subdomain", subdomain, "hyphens", hyphenCount)
							return productionURL, nil
						}
					}
				}
			}
		}
	}

	// Fallback: construct the standard production URL
	// Vercel's default production URL is https://<project-name>.vercel.app
	productionURL := fmt.Sprintf("https://%s.vercel.app", projectName)
	slog.Info("Using constructed production URL", "url", productionURL)
	return productionURL, nil
}

// getProductionURLFromProjects retrieves the production URL using vercel projects command
func (c *CLIVercelClient) getProductionURLFromProjects(projectName string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "vercel", "projects")
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", errors.Errorf("vercel projects command timed out")
		}
		return "", errors.Errorf("failed to get projects: %w\nOutput: %s", err, string(output))
	}

	// Parse the output to find the project URL
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) < 2 {
		return "", errors.Errorf("unexpected vercel projects output format: %s", string(output))
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

	return "", errors.Errorf("could not find production URL for project %s", projectName)
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
			return errors.Errorf("failed to set env var %s: %w", key, err)
		}
	}

	return nil
}

// setEnvVar sets a single environment variable, overwriting if it already exists
func (c *CLIVercelClient) setEnvVar(key, value string) error {
	ctx, cancel := context.WithTimeout(context.Background(), envVarTimeout)
	defer cancel()

	// Use --force to overwrite existing variables
	cmd := exec.CommandContext(ctx, "vercel", "env", "add", key, "production", "--force")
	cmd.Stdin = strings.NewReader(value + "\n")
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return errors.Errorf("setting env var %s timed out after %v", key, envVarTimeout)
		}
		slog.Error("Failed to set env var", "key", key, "error", err, "output", string(output))
		return errors.Errorf("failed to set env var: %w\nOutput: %s", err, string(output))
	}

	slog.Info("Successfully set environment variable", "key", key)
	return nil
}
