package vercel

import (
	"encoding/json"
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

	if _, err := os.Stat(projectFile); os.IsNotExist(err) {
		return nil, nil
	}

	data, err := os.ReadFile(projectFile)
	if err != nil {
		return nil, errors.Errorf("failed to read .vercel/project.json: %w", err)
	}

	var projectData VercelProjectJSON
	if err := json.Unmarshal(data, &projectData); err != nil {
		return nil, errors.Errorf("failed to parse .vercel/project.json: %w", err)
	}

	if projectData.ProjectID == "" {
		return nil, nil
	}

	project, err := client.GetProject(projectData.ProjectID)
	if err != nil {
		return nil, nil
	}

	return &ExistingProject{
		ProjectID: project.ID,
		Name:      project.Name,
	}, nil
}
