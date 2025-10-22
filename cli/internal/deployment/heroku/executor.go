package heroku

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/meroxa/prod/cli/internal/deployment"
)

type StepExecutor = deployment.StepExecutor[*HerokuClient]

func NewStepExecutor(client *HerokuClient, writer io.Writer) *StepExecutor {
	return deployment.NewStepExecutor(client, writer)
}

func InjectExistingApp(se *StepExecutor, client *HerokuClient, appName string) {
	slog.Info("InjectExistingApp called", "appName", appName)
	app, err := client.GetApp(context.Background(), appName)
	if err != nil {
		slog.Warn("Failed to get app details for existing app", "app", appName, "error", err)
		return
	}
	slog.Info("Successfully retrieved app details", "appName", app.Name, "appID", app.ID)

	var webURL string
	if app.WebURL != nil && *app.WebURL != "" {
		webURL = *app.WebURL
	} else {
		webURL = fmt.Sprintf("https://%s.herokuapp.com", app.Name)
	}

	resource := deployment.CreatedResource{
		Name: app.Name,
		Type: "heroku-app",
		ID:   app.ID,
		Metadata: map[string]any{
			"url":     webURL,
			"git_url": app.GitURL,
			"region":  app.Region.Name,
			"app":     app,
		},
	}

	se.InjectStepResult("app", resource)
	slog.Info("Injected app resource into step results", "stepID", "app", "resourceName", resource.Name)
}
