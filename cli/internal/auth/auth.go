package auth

import "context"

type AuthProvider interface {
	CheckAuthentication(ctx context.Context) (bool, error)
	ValidateAPIKey(ctx context.Context, token string) (bool, error)
	PerformOAuthLogin(ctx context.Context) error
	APIKeyPrompt() string
}
