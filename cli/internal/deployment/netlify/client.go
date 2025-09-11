package netlify

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

// CLINetlifyClient implements the NetlifyClient interface using Netlify CLI
type CLINetlifyClient struct{}

// NewCLINetlifyClient creates a new CLI-based Netlify client
func NewCLINetlifyClient() *CLINetlifyClient {
	return &CLINetlifyClient{}
}

// ensureNetlifyCLI checks if netlify CLI is installed
func (c *CLINetlifyClient) ensureNetlifyCLI() error {
	cmd := exec.Command("netlify", "--version")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("netlify CLI is not installed. Install with: npm install -g netlify-cli")
	}
	return nil
}

// CreateSite creates a new Netlify site using CLI
func (c *CLINetlifyClient) CreateSite(req CreateSiteRequest) (*NetlifySite, error) {
	if err := c.ensureNetlifyCLI(); err != nil {
		return nil, err
	}

	// Use the Netlify API instead of CLI to avoid interactive prompts
	// The CLI sites:create command is interactive and asks for team selection
	apiData := map[string]interface{}{
		"name": req.Name,
	}

	// Convert to JSON
	jsonData, err := json.Marshal(apiData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal API data: %w", err)
	}

	// Create site using the API
	args := []string{"api", "createSite", "--data", string(jsonData)}

	cmd := exec.Command("netlify", args...)
	log.Printf("Creating Netlify site with name: %s", req.Name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Check if name is taken and provide helpful error
		outputStr := string(output)
		if strings.Contains(outputStr, "already taken") || strings.Contains(outputStr, "already exists") || strings.Contains(outputStr, "already in use") {
			return nil, fmt.Errorf("site name '%s' is already taken", req.Name)
		}
		return nil, fmt.Errorf("failed to create site: %w\nOutput: %s", err, outputStr)
	}

	// Parse the JSON response from the API
	outputStr := string(output)
	log.Printf("NetlifyCreateSite output: %s", outputStr)

	var site NetlifySite
	if err := json.Unmarshal(output, &site); err != nil {
		return nil, fmt.Errorf("failed to parse site response: %w\nOutput: %s", err, outputStr)
	}

	// Ensure we have a site ID
	if site.ID == "" {
		return nil, fmt.Errorf("site created but no ID returned")
	}

	// Note: Environment variables should be set separately after site creation
	// The CLI doesn't support setting env vars during creation

	return &site, nil
}

// listSites retrieves all sites using CLI (helper method)
func (c *CLINetlifyClient) listSites() ([]NetlifySite, error) {
	cmd := exec.Command("netlify", "sites:list", "--json")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to list sites: %w", err)
	}

	var sites []NetlifySite
	if err := json.Unmarshal(output, &sites); err != nil {
		return nil, fmt.Errorf("failed to parse sites: %w", err)
	}

	return sites, nil
}

// GetSite retrieves site information using CLI
func (c *CLINetlifyClient) GetSite(siteID string) (*NetlifySite, error) {
	if err := c.ensureNetlifyCLI(); err != nil {
		return nil, err
	}

	// List sites and find the one we want
	cmd := exec.Command("netlify", "sites:list", "--json")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to list sites: %w", err)
	}

	var sites []NetlifySite
	if err := json.Unmarshal(output, &sites); err != nil {
		return nil, fmt.Errorf("failed to parse sites: %w", err)
	}

	for _, site := range sites {
		if site.ID == siteID {
			return &site, nil
		}
	}

	return nil, fmt.Errorf("site %s not found", siteID)
}

// UpdateSite updates a Netlify site (limited support via CLI)
func (c *CLINetlifyClient) UpdateSite(siteID string, req UpdateSiteRequest) (*NetlifySite, error) {
	// Netlify CLI doesn't have a direct update command
	// We'd need to use the API for this, so return the current site
	return c.GetSite(siteID)
}

// DeleteSite deletes a Netlify site using CLI
func (c *CLINetlifyClient) DeleteSite(siteID string) error {
	if err := c.ensureNetlifyCLI(); err != nil {
		return err
	}

	cmd := exec.Command("netlify", "sites:delete", siteID, "--force")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to delete site: %w\nOutput: %s", err, string(output))
	}

	return nil
}

// LinkSite links the current directory to a Netlify site using CLI
func (c *CLINetlifyClient) LinkSite(siteID string) error {
	if err := c.ensureNetlifyCLI(); err != nil {
		return err
	}

	log.Println(os.Getwd())
	cmd := exec.Command("netlify", "link", "--id", siteID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to link site: %w\nOutput: %s", err, string(output))
	}

	log.Printf("Successfully linked site %s to current directory", siteID)
	return nil
}

// DeploySite deploys a site to Netlify using CLI
func (c *CLINetlifyClient) DeploySite(siteID string, path string, functionsPath string) (*NetlifyDeploy, error) {
	if err := c.ensureNetlifyCLI(); err != nil {
		return nil, err
	}

	// Build deploy command
	args := []string{"deploy", "--prod", "--json"}

	// Add directory to deploy
	if path != "" {
		args = append(args, "--dir", path)
	}

	// Add functions directory if specified
	if functionsPath != "" {
		args = append(args, "--functions", functionsPath)
	}

	// Run deployment with timeout
	// Note: using a longer timeout as deployments can take time
	ctx, cancel := context.WithTimeout(context.Background(), deployTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "netlify", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("deployment timed out after %v", deployTimeout)
		}
		return nil, fmt.Errorf("deployment failed: %w\nOutput: %s", err, string(output))
	}

	// Parse deployment response
	// Netlify CLI outputs JSON when --json flag is used
	var deployResult struct {
		DeployID  string `json:"deploy_id"`
		SiteID    string `json:"site_id"`
		SiteName  string `json:"site_name"`
		URL       string `json:"url"`
		SiteURL   string `json:"site_url"`
		DeployURL string `json:"deploy_url"`
		Logs      string `json:"logs"`
	}

	if err := json.Unmarshal(output, &deployResult); err != nil {
		// Try to parse as multiple JSON objects (sometimes CLI outputs progress then result)
		lines := strings.Split(string(output), "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			line := strings.TrimSpace(lines[i])
			if line != "" && strings.HasPrefix(line, "{") {
				if err := json.Unmarshal([]byte(line), &deployResult); err == nil {
					break
				}
			}
		}

		if deployResult.DeployID == "" {
			return nil, fmt.Errorf("failed to parse deployment response: %w\nOutput: %s", err, string(output))
		}
	}

	// Convert to our NetlifyDeploy struct
	deploy := &NetlifyDeploy{
		ID:        deployResult.DeployID,
		SiteID:    deployResult.SiteID,
		State:     "ready", // CLI waits until ready
		URL:       deployResult.URL,
		DeployURL: deployResult.DeployURL,
		Name:      deployResult.SiteName,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	return deploy, nil
}

// GetDeploy retrieves deployment information
func (c *CLINetlifyClient) GetDeploy(siteID, deployID string) (*NetlifyDeploy, error) {
	// Netlify CLI doesn't have a get deploy command
	// Return a basic response indicating it's ready since CLI deploys synchronously
	return &NetlifyDeploy{
		ID:     deployID,
		SiteID: siteID,
		State:  "ready",
	}, nil
}

// SetEnvironmentVariables sets environment variables for a site
func (c *CLINetlifyClient) SetEnvironmentVariables(siteID string, vars map[string]string) error {
	if err := c.ensureNetlifyCLI(); err != nil {
		return err
	}

	for key, value := range vars {
		if err := c.setEnvVar(siteID, key, value); err != nil {
			return fmt.Errorf("failed to set env var %s: %w", key, err)
		}
	}

	return nil
}

// setEnvVar sets a single environment variable
func (c *CLINetlifyClient) setEnvVar(siteID, key, value string) error {
	cmd := exec.Command("netlify", "env:set", key, value)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to set env var: %w\nOutput: %s", err, string(output))
	}
	return nil
}

// UpdateBuildSettings updates build settings for a site
func (c *CLINetlifyClient) UpdateBuildSettings(siteID string, settings BuildSettings) error {
	// Netlify CLI doesn't support updating build settings directly
	// These are typically set in netlify.toml or during site creation
	// For CLI-based deployments, build settings aren't used anyway
	return nil
}
