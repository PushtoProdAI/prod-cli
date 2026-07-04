package config

import "testing"

func TestBackendConfiguredAndMode(t *testing.T) {
	// Ensure ldflags vars don't leak into the test.
	SupabaseURL = ""
	SupabaseAnonKey = ""

	t.Run("local by default (no env, no ldflags)", func(t *testing.T) {
		t.Setenv("PROD_BACKEND_URL", "")
		t.Setenv("SUPABASE_URL", "")
		t.Setenv("SUPABASE_ANON_KEY", "")
		if BackendConfigured() {
			t.Error("BackendConfigured() = true, want false")
		}
		if Mode() != "local" {
			t.Errorf("Mode() = %q, want local", Mode())
		}
	})

	t.Run("managed when url + key present", func(t *testing.T) {
		t.Setenv("PROD_BACKEND_URL", "https://backend.example.com")
		t.Setenv("SUPABASE_ANON_KEY", "anon-key")
		if !BackendConfigured() {
			t.Error("BackendConfigured() = false, want true")
		}
		if Mode() != "managed" {
			t.Errorf("Mode() = %q, want managed", Mode())
		}
	})

	t.Run("url without key stays local", func(t *testing.T) {
		t.Setenv("PROD_BACKEND_URL", "https://backend.example.com")
		t.Setenv("SUPABASE_ANON_KEY", "")
		if BackendConfigured() {
			t.Error("BackendConfigured() = true with no anon key, want false")
		}
	})
}

func TestGetSupabaseURLPrecedence(t *testing.T) {
	SupabaseURL = "ldflags-url"

	t.Run("PROD_BACKEND_URL wins", func(t *testing.T) {
		t.Setenv("PROD_BACKEND_URL", "env-prod-url")
		t.Setenv("SUPABASE_URL", "env-supabase-url")
		if got := GetSupabaseURL(); got != "env-prod-url" {
			t.Errorf("GetSupabaseURL() = %q, want env-prod-url", got)
		}
	})

	t.Run("falls back to ldflags", func(t *testing.T) {
		t.Setenv("PROD_BACKEND_URL", "")
		t.Setenv("SUPABASE_URL", "")
		if got := GetSupabaseURL(); got != "ldflags-url" {
			t.Errorf("GetSupabaseURL() = %q, want ldflags-url", got)
		}
	})
}
