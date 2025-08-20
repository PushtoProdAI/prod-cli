package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// TokenStore handles secure storage of authentication tokens
type TokenStore struct {
	configDir string
}

// NewTokenStore creates a new token store
func NewTokenStore() (*TokenStore, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	configDir := filepath.Join(homeDir, ".prod")
	
	// Create directory with restricted permissions
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create config directory: %w", err)
	}

	return &TokenStore{
		configDir: configDir,
	}, nil
}

// SaveSession saves the session to disk
func (ts *TokenStore) SaveSession(session *Session) error {
	if session == nil {
		return fmt.Errorf("session is nil")
	}

	sessionFile := filepath.Join(ts.configDir, "auth.json")
	
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal session: %w", err)
	}

	// Write with restricted permissions (owner read/write only)
	if err := os.WriteFile(sessionFile, data, 0600); err != nil {
		return fmt.Errorf("failed to write session file: %w", err)
	}

	return nil
}

// LoadSession loads the session from disk
func (ts *TokenStore) LoadSession() (*Session, error) {
	sessionFile := filepath.Join(ts.configDir, "auth.json")
	
	data, err := os.ReadFile(sessionFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No session found
		}
		return nil, fmt.Errorf("failed to read session file: %w", err)
	}

	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, fmt.Errorf("failed to unmarshal session: %w", err)
	}

	return &session, nil
}

// DeleteSession removes the stored session
func (ts *TokenStore) DeleteSession() error {
	sessionFile := filepath.Join(ts.configDir, "auth.json")
	
	err := os.Remove(sessionFile)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete session file: %w", err)
	}

	return nil
}

// GetConfigDir returns the configuration directory path
func (ts *TokenStore) GetConfigDir() string {
	return ts.configDir
}