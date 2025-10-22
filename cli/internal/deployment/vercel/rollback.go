package vercel

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/go-errors/errors"
	"github.com/meroxa/prod/cli/internal/deployment"
)

func listVercelDeployments(ctx context.Context, sourcePath string, projectName string) ([]deployment.DeploymentInfo, error) {
	originalDir, err := os.Getwd()
	if err != nil {
		return nil, errors.Errorf("failed to get current directory: %w", err)
	}

	if sourcePath != "." && sourcePath != "" {
		if err := os.Chdir(sourcePath); err != nil {
			return nil, errors.Errorf("failed to change to source directory: %w", err)
		}
		defer os.Chdir(originalDir)
	}

	currentDir, _ := os.Getwd()
	slog.Info("Listing Vercel deployments", "currentDir", currentDir, "sourcePath", sourcePath, "projectName", projectName)

	cmdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "vercel", "list", projectName, "--yes")
	output, err := cmd.CombinedOutput()
	if err != nil {
		if cmdCtx.Err() == context.DeadlineExceeded {
			return nil, errors.Errorf("listing deployments timed out")
		}
		return nil, errors.Errorf("failed to list deployments: %w\nOutput: %s", err, string(output))
	}

	slog.Info("Vercel list output", "output", string(output))

	var deployments []deployment.DeploymentInfo
	lines := strings.Split(string(output), "\n")

	inDeploymentTable := false
	for i, line := range lines {
		line = strings.TrimSpace(line)
		slog.Debug("Parsing line", "lineNum", i, "line", line, "inTable", inDeploymentTable)

		if strings.Contains(line, "Age") && strings.Contains(line, "Deployment") {
			slog.Info("Found deployment table header")
			inDeploymentTable = true
			continue
		}

		if line == "" || strings.HasPrefix(line, "Vercel CLI") || strings.HasPrefix(line, "Retrieving") || strings.HasPrefix(line, "Fetching") || strings.HasPrefix(line, ">") {
			continue
		}

		if strings.HasPrefix(line, "https://") {
			slog.Info("Found https:// prefix, exiting table")
			inDeploymentTable = false
			continue
		}

		if !inDeploymentTable {
			continue
		}

		fields := strings.Fields(line)
		slog.Info("Parsing deployment line", "fields", fields, "fieldCount", len(fields))
		if len(fields) >= 4 && strings.HasPrefix(fields[1], "https://") {
			deployURL := fields[1]

			parts := strings.Split(deployURL, "://")
			if len(parts) > 1 {
				domain := strings.Split(parts[1], ".")[0]
				slog.Info("Found deployment", "domain", domain, "url", deployURL)
				deployments = append(deployments, deployment.DeploymentInfo{
					ID:        deployURL,
					Status:    "Ready",
					CreatedAt: fields[0],
					URL:       deployURL,
				})
			}
		}
	}

	slog.Info("Finished parsing deployments", "count", len(deployments))
	return deployments, nil
}

func GetCurrentVercelDeployment(ctx context.Context, client VercelClient, projectName string, sourcePath string) (*deployment.DeploymentInfo, error) {
	deployments, err := listVercelDeployments(ctx, sourcePath, projectName)
	if err != nil {
		return nil, err
	}

	if len(deployments) == 0 {
		return nil, errors.Errorf("no deployments found for project %s", projectName)
	}

	currentDeploy := &deployments[0]

	// Get the production alias URL instead of the deployment-specific URL
	// This is the URL users actually visit (e.g., https://project-name.vercel.app)
	if cliClient, ok := client.(*CLIVercelClient); ok {
		productionURL, err := cliClient.getProductionURLFromAliases(projectName)
		if err != nil {
			slog.Warn("Failed to get production URL from aliases, using deployment URL", "error", err, "deploymentURL", currentDeploy.URL)
		} else {
			slog.Info("Using production alias URL", "productionURL", productionURL, "deploymentURL", currentDeploy.URL)
			currentDeploy.URL = productionURL
		}
	}

	return currentDeploy, nil
}

func GetPreviousVercelDeployment(ctx context.Context, client VercelClient, projectName string, sourcePath string) (*deployment.DeploymentInfo, error) {
	deployments, err := listVercelDeployments(ctx, sourcePath, projectName)
	if err != nil {
		return nil, err
	}

	if len(deployments) < 2 {
		return nil, errors.Errorf("no previous deployment found for project %s (only %d deployments exist)", projectName, len(deployments))
	}

	previousDeploy := &deployments[1]

	// Get the production alias URL instead of the deployment-specific URL
	// This is the URL users actually visit (e.g., https://project-name.vercel.app)
	if cliClient, ok := client.(*CLIVercelClient); ok {
		productionURL, err := cliClient.getProductionURLFromAliases(projectName)
		if err != nil {
			slog.Warn("Failed to get production URL from aliases, using deployment URL", "error", err, "deploymentURL", previousDeploy.URL)
		} else {
			slog.Info("Using production alias URL for rollback result", "productionURL", productionURL, "deploymentURL", previousDeploy.URL)
			previousDeploy.URL = productionURL
		}
	}

	return previousDeploy, nil
}

// RollbackVercelDeployment rolls back to a previous deployment
func RollbackVercelDeployment(ctx context.Context, client VercelClient, targetDeploymentURL string, sourcePath string) error {
	originalDir, err := os.Getwd()
	if err != nil {
		return errors.Errorf("failed to get current directory: %w", err)
	}

	if sourcePath != "." && sourcePath != "" {
		if err := os.Chdir(sourcePath); err != nil {
			return errors.Errorf("failed to change to source directory: %w", err)
		}
		defer os.Chdir(originalDir)
	}

	slog.Info("Rolling back Vercel deployment", "target_url", targetDeploymentURL)

	cmdCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "vercel", "rollback", targetDeploymentURL, "--yes")
	output, err := cmd.CombinedOutput()
	if err != nil {
		if cmdCtx.Err() == context.DeadlineExceeded {
			return errors.Errorf("rollback timed out after 3 minutes")
		}
		return errors.Errorf("rollback failed: %w\nOutput: %s", err, string(output))
	}

	slog.Info("Vercel rollback completed successfully", "output", string(output))
	return nil
}
