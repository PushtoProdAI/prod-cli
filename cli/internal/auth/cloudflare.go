package auth

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/go-errors/errors"
)

// CloudflareAuth checks that the user's Cloudflare credentials are available for the Pages
// direct-upload flow. Cloudflare uses an API token (not OAuth), so auth is entirely env-based:
// CLOUDFLARE_API_TOKEN + CLOUDFLARE_ACCOUNT_ID (the user's own credentials, held locally).
type CloudflareAuth struct {
	out io.Writer
}

// NewCloudflareAuth creates a Cloudflare authentication handler.
func NewCloudflareAuth(out io.Writer) *CloudflareAuth {
	return &CloudflareAuth{out: out}
}

// CheckAuthentication reports whether both the API token and account id are set.
func (a *CloudflareAuth) CheckAuthentication(_ context.Context) (bool, error) {
	if os.Getenv("CLOUDFLARE_API_TOKEN") == "" {
		return false, nil
	}
	if os.Getenv("CLOUDFLARE_ACCOUNT_ID") == "" {
		return false, errors.Errorf("CLOUDFLARE_API_TOKEN is set but CLOUDFLARE_ACCOUNT_ID is missing — set your account id")
	}
	return true, nil
}

// ValidateAPIKey treats a non-empty token as valid (a real network check happens on first use).
func (a *CloudflareAuth) ValidateAPIKey(_ context.Context, token string) (bool, error) {
	return token != "", nil
}

// PerformOAuthLogin isn't applicable — Cloudflare uses API tokens. Point the user at the token.
func (a *CloudflareAuth) PerformOAuthLogin(_ context.Context) error {
	_, _ = fmt.Fprintln(a.out, "Cloudflare uses an API token, not a browser login.")
	_, _ = fmt.Fprintln(a.out, "Create one at https://dash.cloudflare.com/profile/api-tokens (permission: Account → Cloudflare Pages → Edit),")
	_, _ = fmt.Fprintln(a.out, "then set CLOUDFLARE_API_TOKEN and CLOUDFLARE_ACCOUNT_ID.")
	return errors.Errorf("set CLOUDFLARE_API_TOKEN and CLOUDFLARE_ACCOUNT_ID to deploy to Cloudflare Pages")
}

// APIKeyPrompt is the message shown when prompting for the token.
func (a *CloudflareAuth) APIKeyPrompt() string {
	return "Enter your Cloudflare API token (Account → Cloudflare Pages → Edit):"
}
