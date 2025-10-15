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

	// List all services
	allServices, err := client.ListServices(ctx, "")
	if err != nil {
		return nil, errors.Errorf("failed to list services: %w", err)
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
