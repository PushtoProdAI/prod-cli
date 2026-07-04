package heroku

import (
	"context"
	"os/exec"
	"strings"

	"github.com/go-errors/errors"
)

type ExistingProject struct {
	AppID  string
	Name   string
	WebURL string
	GitURL string
	Region string
}

func DetectExistingProject(ctx context.Context, client *HerokuClient, projectName string, sourcePath string) (*ExistingProject, error) {
	if sourcePath == "" {
		sourcePath = "."
	}

	// Check if a heroku git remote exists
	cmd := exec.CommandContext(ctx, "git", "remote", "get-url", "heroku")
	cmd.Dir = sourcePath
	output, err := cmd.Output()
	if err != nil {
		return nil, nil
	}

	gitURL := strings.TrimSpace(string(output))

	// Extract app name from git URL
	// Format: https://git.heroku.com/app-name.git or git@heroku.com:app-name.git
	appName := extractAppNameFromGitURL(gitURL)
	if appName == "" {
		return nil, nil
	}

	// Verify the app still exists in Heroku
	apps, err := client.ListApps(ctx)
	if err != nil {
		return nil, errors.Errorf("failed to list apps: %w", err)
	}

	for _, app := range apps {
		if strings.EqualFold(app.Name, appName) {
			return &ExistingProject{
				AppID:  app.ID,
				Name:   app.Name,
				WebURL: "",
				GitURL: gitURL,
				Region: app.Region.Name,
			}, nil
		}
	}

	return nil, nil
}

func extractAppNameFromGitURL(gitURL string) string {
	// Handle https format: https://git.heroku.com/app-name.git
	if strings.HasPrefix(gitURL, "https://git.heroku.com/") {
		appName := strings.TrimPrefix(gitURL, "https://git.heroku.com/")
		appName = strings.TrimSuffix(appName, ".git")
		return appName
	}

	// Handle ssh format: git@heroku.com:app-name.git
	if strings.HasPrefix(gitURL, "git@heroku.com:") {
		appName := strings.TrimPrefix(gitURL, "git@heroku.com:")
		appName = strings.TrimSuffix(appName, ".git")
		return appName
	}

	return ""
}
