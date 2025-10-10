package netlify

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/go-errors/errors"
)

type ExistingProject struct {
	SiteID string
	Name   string
}

type NetlifyState struct {
	SiteID string `json:"siteId"`
}

func DetectExistingProject(client NetlifyClient, projectName string, sourcePath string) (*ExistingProject, error) {
	if sourcePath == "" {
		sourcePath = "."
	}

	netlifyDir := filepath.Join(sourcePath, ".netlify")
	stateFile := filepath.Join(netlifyDir, "state.json")

	if _, err := os.Stat(stateFile); os.IsNotExist(err) {
		return nil, nil
	}

	data, err := os.ReadFile(stateFile)
	if err != nil {
		return nil, errors.Errorf("failed to read .netlify/state.json: %w", err)
	}

	var state NetlifyState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, errors.Errorf("failed to parse .netlify/state.json: %w", err)
	}

	if state.SiteID == "" {
		return nil, nil
	}

	site, err := client.GetSite(state.SiteID)
	if err != nil {
		return nil, nil
	}

	return &ExistingProject{
		SiteID: site.ID,
		Name:   site.Name,
	}, nil
}
