package render

import (
	"context"
	"fmt"

	"github.com/go-errors/errors"
)

type ExistingProject struct {
	ServiceID string
	Name      string
	Type      string
}

func DetectExistingProject(ctx context.Context, client RenderClient, projectName string) (*ExistingProject, error) {
	webServiceName := fmt.Sprintf("%s-web", projectName)

	// First try with the name filter (may or may not work depending on API implementation)
	services, err := client.ListServices(ctx, webServiceName)
	if err != nil {
		return nil, errors.Errorf("failed to list services: %w", err)
	}

	// Check if we found a match with the filtered result
	for _, service := range services {
		if service.Name == webServiceName {
			return &ExistingProject{
				ServiceID: service.ID,
				Name:      service.Name,
				Type:      service.Type,
			}, nil
		}
	}

	// If filtered search didn't work, try listing all services
	// This handles the case where the API name parameter doesn't work as expected
	allServices, err := client.ListServices(ctx, "")
	if err != nil {
		return nil, errors.Errorf("failed to list all services: %w", err)
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
