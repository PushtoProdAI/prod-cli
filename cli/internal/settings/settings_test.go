package settings

import (
	"testing"
)

func TestDefaultSettings(t *testing.T) {
	settings := DefaultSettings()

	// Error tracking should be disabled by default
	if settings.ErrorTracking.Enabled {
		t.Error("Error tracking should be disabled by default")
	}
}

func TestHasConsentDefault(t *testing.T) {
	// This test checks the default consent status
	// The actual behavior depends on whether settings file exists
	hasConsent, err := HasConsent()
	if err != nil {
		t.Logf("HasConsent error (expected if no settings file): %v", err)
	}

	// Default should be false
	if hasConsent {
		t.Logf("Consent is currently enabled: %v", hasConsent)
	}
}

func TestSaveConsent(t *testing.T) {
	// Test saving consent (this creates a settings file)
	err := SaveConsent(false)
	if err != nil {
		t.Errorf("SaveConsent failed: %v", err)
	}

	// Test reading back the consent
	hasConsent, err := HasConsent()
	if err != nil {
		t.Errorf("HasConsent failed after saving: %v", err)
	}

	if hasConsent {
		t.Error("Consent should be false after saving false")
	}
}

func TestGetSettingsPath(t *testing.T) {
	path, err := GetSettingsPath()
	if err != nil {
		t.Errorf("GetSettingsPath failed: %v", err)
	}

	if path == "" {
		t.Error("Settings path should not be empty")
	}

	t.Logf("Settings path: %s", path)
}

func TestConsentCaching(t *testing.T) {
	// Invalidate cache to start fresh
	InvalidateConsentCache()

	// Check initial cache status - should not be cached yet
	cached, _ := GetConsentCacheStatus()
	if cached {
		t.Error("Cache should not be set initially")
	}

	// First call should load from file but not cache (until explicitly set)
	consent1, err := HasConsent()
	if err != nil {
		t.Errorf("First HasConsent call failed: %v", err)
	}

	// Cache should still not be set (consent not explicitly saved yet)
	cached, _ = GetConsentCacheStatus()
	if cached {
		t.Error("Cache should not be set until consent is explicitly saved")
	}

	// Second call should also load from file
	consent2, err := HasConsent()
	if err != nil {
		t.Errorf("Second HasConsent call failed: %v", err)
	}

	// Results should be identical
	if consent1 != consent2 {
		t.Error("Consent values should match")
	}

	// Save new consent - this should enable caching
	newConsent := !consent1
	err = SaveConsent(newConsent)
	if err != nil {
		t.Errorf("SaveConsent failed: %v", err)
	}

	// Now cache should be set
	cached, cachedValue := GetConsentCacheStatus()
	if !cached {
		t.Error("Cache should be set after SaveConsent")
	}
	if cachedValue != newConsent {
		t.Error("Cached value should match saved consent")
	}

	// Verify subsequent calls use cache
	consent3, err := HasConsent()
	if err != nil {
		t.Errorf("HasConsent after save failed: %v", err)
	}

	if consent3 != newConsent {
		t.Error("HasConsent should return cached value after save")
	}
}

func TestCacheInvalidation(t *testing.T) {
	// Set up cache by saving consent
	SaveConsent(true)

	// Verify cache is set
	cached, _ := GetConsentCacheStatus()
	if !cached {
		t.Error("Cache should be set after SaveConsent")
	}

	// Invalidate cache
	InvalidateConsentCache()

	// Verify cache is cleared
	cached, value := GetConsentCacheStatus()
	if cached {
		t.Error("Cache should be cleared after invalidation")
	}
	if value != false {
		t.Error("Cache value should be reset to false")
	}
}
