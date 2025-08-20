package auth

import (
	"context"
	"fmt"
	"os"
	"time"
)

type AuthProvider interface {
	CheckAuthentication(ctx context.Context) (bool, error)
	ValidateAPIKey(ctx context.Context, token string) (bool, error)
	PerformOAuthLogin(ctx context.Context) error
	APIKeyPrompt() string
}

// SupabaseAuth handles authentication with Supabase
type SupabaseAuth struct {
	config *Config
	store  *TokenStore
}

// NewSupabaseAuth creates a new Supabase auth client
func NewSupabaseAuth() (*SupabaseAuth, error) {
	// Get Supabase configuration from environment
	supabaseURL := os.Getenv("SUPABASE_URL")
	if supabaseURL == "" {
		return nil, fmt.Errorf("SUPABASE_URL environment variable not set")
	}

	supabaseAnonKey := os.Getenv("SUPABASE_ANON_KEY")
	if supabaseAnonKey == "" {
		return nil, fmt.Errorf("SUPABASE_ANON_KEY environment variable not set")
	}

	store, err := NewTokenStore()
	if err != nil {
		return nil, fmt.Errorf("failed to create token store: %w", err)
	}

	return &SupabaseAuth{
		config: &Config{
			SupabaseURL:     supabaseURL,
			SupabaseAnonKey: supabaseAnonKey,
		},
		store: store,
	}, nil
}

// IsAuthenticated checks if the user is currently authenticated
func (sa *SupabaseAuth) IsAuthenticated() bool {
	session, err := sa.GetSession()
	if err != nil || session == nil {
		return false
	}

	if session.ExpiresAt.Before(time.Now()) {
		return false
	}

	return true
}

// GetSession returns the current session if valid
func (sa *SupabaseAuth) GetSession() (*Session, error) {
	session, err := sa.store.LoadSession()
	if err != nil {
		return nil, fmt.Errorf("failed to load session: %w", err)
	}

	if session == nil {
		return nil, nil
	}

	if session.ExpiresAt.Before(time.Now()) {
		// TODO: Implement refresh token logic
		return nil, fmt.Errorf("session expired")
	}

	return session, nil
}

// Logout removes the stored session
func (sa *SupabaseAuth) Logout(ctx context.Context) error {
	if err := sa.store.DeleteSession(); err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}

	fmt.Println("✅ Successfully logged out")
	return nil
}

// GetAuthHeader returns the authorization header value for API requests
func (sa *SupabaseAuth) GetAuthHeader() (string, error) {
	session, err := sa.GetSession()
	if err != nil {
		return "", err
	}

	if session == nil {
		return "", fmt.Errorf("not authenticated")
	}

	return fmt.Sprintf("Bearer %s", session.AccessToken), nil
}
