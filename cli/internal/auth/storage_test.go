package auth

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTokenStore(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "prod-auth-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// Create store with test directory
	store := &TokenStore{
		configDir: tempDir,
	}

	// Test saving a session
	session := &Session{
		AccessToken:  "test-access-token",
		RefreshToken: "test-refresh-token",
		ExpiresAt:    time.Now().Add(time.Hour),
		User: &User{
			ID:    "test-user-id",
			Email: "test@example.com",
		},
	}

	if err := store.SaveSession(session); err != nil {
		t.Fatalf("Failed to save session: %v", err)
	}

	// Verify file was created with correct permissions
	sessionFile := filepath.Join(tempDir, "auth.json")
	info, err := os.Stat(sessionFile)
	if err != nil {
		t.Fatalf("Session file not created: %v", err)
	}

	// Check permissions (should be 0600)
	mode := info.Mode()
	if mode.Perm() != 0600 {
		t.Errorf("Session file has wrong permissions: %v, want 0600", mode.Perm())
	}

	// Test loading the session
	loaded, err := store.LoadSession()
	if err != nil {
		t.Fatalf("Failed to load session: %v", err)
	}

	if loaded.AccessToken != session.AccessToken {
		t.Errorf("Access token mismatch: got %s, want %s", loaded.AccessToken, session.AccessToken)
	}

	if loaded.User.Email != session.User.Email {
		t.Errorf("User email mismatch: got %s, want %s", loaded.User.Email, session.User.Email)
	}

	// Test deleting the session
	if err := store.DeleteSession(); err != nil {
		t.Fatalf("Failed to delete session: %v", err)
	}

	// Verify session is gone
	loaded, err = store.LoadSession()
	if err != nil {
		t.Fatalf("Error loading deleted session: %v", err)
	}
	if loaded != nil {
		t.Error("Session should be nil after deletion")
	}
}