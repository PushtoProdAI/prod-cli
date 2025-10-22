package render

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

type ExistingProject struct {
	ServiceID string
	Name      string
	Type      string
}

func DetectExistingProject(ctx context.Context, client RenderClient, projectName string, sourcePath string) (*ExistingProject, error) {
	// Check for local render.yaml file first
	if sourcePath == "" {
		sourcePath = "."
	}

	hasLocalConfig := false
	renderYamlPath := filepath.Join(sourcePath, "render.yaml")
	if _, err := os.Stat(renderYamlPath); err == nil {
		hasLocalConfig = true
	}

	// Try to list services via API
	webServiceName := fmt.Sprintf("%s-web", projectName)
	allServices, err := client.ListServices(ctx, "")
	if err != nil {
		// If we have local config but API fails, can't verify
		// If no local config and API fails, propagate error
		if !hasLocalConfig {
			return nil, err
		}
		return nil, nil
	}

	for _, service := range allServices {
		if service.Name == webServiceName {
			return &ExistingProject{
				ServiceID: service.ID,
				Name:      service.Name,
				Type:      service.Type,
			}, nil
		}
	}

	return nil, nil
}
