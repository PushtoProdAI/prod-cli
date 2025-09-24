package agent

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/go-errors/errors"
)

const (
	settingsFileName = "settings.json"
)

type Settings struct {
	ErrorTracking ErrorTrackingSettings `json:"error_tracking"`
}

type ErrorTrackingSettings struct {
	Enabled bool `json:"enabled"`
}

func DefaultSettings() Settings {
	return Settings{
		ErrorTracking: ErrorTrackingSettings{
			Enabled: false, // Default to disabled
		},
	}
}

func getSettingsFilePath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", errors.WrapPrefix(err, "failed to get home directory", 0)
	}

	dirPath := filepath.Join(homeDir, ".prod")
	return filepath.Join(dirPath, settingsFileName), nil
}

func loadSettings() (Settings, error) {
	filePath, err := getSettingsFilePath()
	if err != nil {
		return DefaultSettings(), err
	}

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return DefaultSettings(), nil
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		return DefaultSettings(), errors.WrapPrefix(err, "failed to read settings file", 0)
	}

	var settings Settings
	if err := json.Unmarshal(content, &settings); err != nil {
		return DefaultSettings(), errors.WrapPrefix(err, "failed to parse settings JSON", 0)
	}

	return settings, nil
}

func saveSettings(settings Settings) error {
	filePath, err := getSettingsFilePath()
	if err != nil {
		return err
	}

	jsonData, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return errors.WrapPrefix(err, "failed to marshal settings to JSON", 0)
	}

	err = os.WriteFile(filePath, jsonData, 0644)
	if err != nil {
		return errors.WrapPrefix(err, "failed to save settings file", 0)
	}

	return nil
}

func hasConsent() (bool, error) {
	settings, err := loadSettings()
	if err != nil {
		return false, err
	}
	return settings.ErrorTracking.Enabled, nil
}

func saveConsent(consent bool) error {
	settings, err := loadSettings()
	if err != nil {
		// If we can't load settings, start with defaults
		settings = DefaultSettings()
	}

	settings.ErrorTracking.Enabled = consent
	return saveSettings(settings)
}

func GetSettingsPath() (string, error) {
	return getSettingsFilePath()
}
