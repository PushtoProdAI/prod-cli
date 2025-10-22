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

func listVercelDeployments(ctx context.Context, sourcePath string) ([]deployment.DeploymentInfo, error) {
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

	cmdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "vercel", "list")
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
	for _, line := range lines {
		line = strings.TrimSpace(line)

		if strings.Contains(line, "Age") && strings.Contains(line, "Deployment") {
			inDeploymentTable = true
			continue
		}

		if line == "" || strings.HasPrefix(line, "Vercel CLI") || strings.HasPrefix(line, "Retrieving") || strings.HasPrefix(line, "Fetching") || strings.HasPrefix(line, ">") {
			continue
		}

		if strings.HasPrefix(line, "https://") {
			inDeploymentTable = false
			continue
		}

		if !inDeploymentTable {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) >= 4 && strings.HasPrefix(fields[1], "https://") {
			deployURL := fields[1]

			parts := strings.Split(deployURL, "://")
			if len(parts) > 1 {
				domain := strings.Split(parts[1], ".")[0]
				deployments = append(deployments, deployment.DeploymentInfo{
					ID:        domain,
					Status:    "Ready",
					CreatedAt: fields[0],
					URL:       deployURL,
				})
			}
		}
	}

	return deployments, nil
}

func GetCurrentVercelDeployment(ctx context.Context, client VercelClient, projectName string, sourcePath string) (*deployment.DeploymentInfo, error) {
	deployments, err := listVercelDeployments(ctx, sourcePath)
	if err != nil {
		return nil, err
	}

	if len(deployments) == 0 {
		return nil, errors.Errorf("no deployments found for project %s", projectName)
	}

	return &deployments[0], nil
}

func GetPreviousVercelDeployment(ctx context.Context, client VercelClient, projectName string, sourcePath string) (*deployment.DeploymentInfo, error) {
	deployments, err := listVercelDeployments(ctx, sourcePath)
	if err != nil {
		return nil, err
	}

	if len(deployments) < 2 {
		return nil, errors.Errorf("no previous deployment found for project %s (only %d deployments exist)", projectName, len(deployments))
	}

	return &deployments[1], nil
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
