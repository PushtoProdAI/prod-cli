package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/go-errors/errors"
)

// Settings management
const (
	settingsFileName = "settings.json"
)

// Consent caching - only cache after consent has been explicitly set
// This ensures we don't cache the default "false" before user consent flow
var (
	consentValue bool
	consentSet   bool
	consentMutex sync.RWMutex
)

// Settings represents the CLI settings stored in JSON format
type Settings struct {
	ErrorTracking ErrorTrackingSettings `json:"error_tracking"`
}

// ErrorTrackingSettings contains error tracking configuration
type ErrorTrackingSettings struct {
	Enabled bool `json:"enabled"`
}

// DefaultSettings returns the default settings configuration
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

	// If file doesn't exist, return default settings
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

// HasConsent checks if the user has consented to error tracking
//
// Performance optimization: Caches consent after it's been explicitly set by user.
// This avoids caching default "false" values before consent flow while maintaining
// performance benefits once consent is established.
func HasConsent() (bool, error) {
	// Check if consent has been explicitly set (fast path)
	consentMutex.RLock()
	if consentSet {
		value := consentValue
		consentMutex.RUnlock()
		return value, nil
	}
	consentMutex.RUnlock()

	// Consent not yet cached - load from file each time until explicitly set
	settings, err := loadSettings()
	if err != nil {
		return false, err
	}

	return settings.ErrorTracking.Enabled, nil
}

// SaveConsent saves the user's consent preference for error tracking
func SaveConsent(consent bool) error {
	settings, err := loadSettings()
	if err != nil {
		// If we can't load settings, start with defaults
		settings = DefaultSettings()
	}

	settings.ErrorTracking.Enabled = consent
	err = saveSettings(settings)
	if err != nil {
		return err
	}

	// Cache the value now that consent has been explicitly set by user
	consentMutex.Lock()
	consentValue = consent
	consentSet = true
	consentMutex.Unlock()

	return nil
}

// GetSettingsPath returns the path to the settings file (useful for debugging/management)
func GetSettingsPath() (string, error) {
	return getSettingsFilePath()
}

// InvalidateConsentCache forces the consent cache to be cleared
// This is useful for testing or when settings might have been changed externally
func InvalidateConsentCache() {
	consentMutex.Lock()
	consentSet = false
	consentValue = false
	consentMutex.Unlock()
}

// GetConsentCacheStatus returns cache status for debugging/testing
func GetConsentCacheStatus() (cached bool, value bool) {
	consentMutex.RLock()
	defer consentMutex.RUnlock()

	return consentSet, consentValue
}
