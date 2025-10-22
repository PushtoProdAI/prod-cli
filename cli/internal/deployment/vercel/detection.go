package vercel

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/go-errors/errors"
)

type ExistingProject struct {
	ProjectID string
	Name      string
}

type VercelProjectJSON struct {
	ProjectID string `json:"projectId"`
	OrgID     string `json:"orgId"`
}

func DetectExistingProject(client VercelClient, projectName string, sourcePath string) (*ExistingProject, error) {
	if sourcePath == "" {
		sourcePath = "."
	}

	vercelDir := filepath.Join(sourcePath, ".vercel")
	projectFile := filepath.Join(vercelDir, "project.json")

	slog.Info("Checking for existing Vercel project", "sourcePath", sourcePath, "projectFile", projectFile)

	if _, err := os.Stat(projectFile); os.IsNotExist(err) {
		slog.Info("No .vercel/project.json found - no existing project detected")
		return nil, nil
	}

	data, err := os.ReadFile(projectFile)
	if err != nil {
		slog.Error("Failed to read .vercel/project.json", "error", err)
		return nil, errors.Errorf("failed to read .vercel/project.json: %w", err)
	}

	var projectData VercelProjectJSON
	if err := json.Unmarshal(data, &projectData); err != nil {
		slog.Error("Failed to parse .vercel/project.json", "error", err)
		return nil, errors.Errorf("failed to parse .vercel/project.json: %w", err)
	}

	slog.Info("Found .vercel/project.json", "projectID", projectData.ProjectID, "orgID", projectData.OrgID)

	if projectData.ProjectID == "" {
		slog.Warn(".vercel/project.json exists but projectID is empty")
		return nil, nil
	}

	slog.Info("Attempting to get project details from Vercel API", "projectID", projectData.ProjectID)
	project, err := client.GetProject(projectData.ProjectID)
	if err != nil {
		slog.Warn("Failed to get project from Vercel API - returning nil", "projectID", projectData.ProjectID, "error", err)
		// If API call fails but we have a valid project.json file,
		// return the project info from the file. This allows:
		// 1. Rollback operations to proceed with the local project ID
		// 2. UI to correctly show "update" instead of "new" deployment
		// 3. Detection to work even when API is temporarily unavailable
		return &ExistingProject{
			ProjectID: projectData.ProjectID,
			Name:      projectName,
		}, nil
	}

	slog.Info("Successfully detected existing Vercel project", "projectID", project.ID, "name", project.Name)
	return &ExistingProject{
		ProjectID: project.ID,
		Name:      project.Name,
	}, nil
}
