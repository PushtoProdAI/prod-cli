package flyio

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/go-errors/errors"
)

type ExistingProject struct {
	AppID    string
	Name     string
	Hostname string
}

func DetectExistingProject(ctx context.Context, client FlyioClient, projectName string) (*ExistingProject, error) {
	flyctlClient, ok := client.(*FlyctlClient)
	if !ok {
		return nil, errors.Errorf("client is not a FlyctlClient")
	}

	if err := flyctlClient.ensureFlyctl(ctx); err != nil {
		return nil, err
	}

	// Normalize the project name to match what would be used during deployment
	normalizedName := NormalizeFlyAppName(projectName)

	output, err := flyctlClient.executor.Execute(ctx, "flyctl", "apps", "list", "--json")
	if err != nil {
		return nil, errors.Errorf("failed to list apps: %w", err)
	}

	var apps []FlyioApp
	if err := json.Unmarshal(output, &apps); err != nil {
		return nil, errors.Errorf("failed to parse apps list: %w", err)
	}

	for _, app := range apps {
		if strings.EqualFold(app.Name, normalizedName) {
			hostname := app.Hostname
			if hostname != "" && !strings.HasPrefix(hostname, "http") {
				hostname = "https://" + hostname
			}
			return &ExistingProject{
				AppID:    app.ID,
				Name:     app.Name,
				Hostname: hostname,
			}, nil
		}
	}

	return nil, nil
}
