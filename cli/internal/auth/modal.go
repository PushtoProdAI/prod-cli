package auth

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-errors/errors"
)

// ModalAuth checks for Modal credentials: the MODAL_TOKEN_ID / MODAL_TOKEN_SECRET env
// pair, or a ~/.modal.toml written by `modal token new`.
type ModalAuth struct {
	out io.Writer
}

var _ AuthProvider = (*ModalAuth)(nil)

// NewModalAuth builds a Modal credential provider.
func NewModalAuth(out io.Writer) *ModalAuth { return &ModalAuth{out: out} }

// CheckAuthentication reports whether Modal credentials are available locally, rejecting an
// obviously-malformed or half-set token pair early (the env pair used to be accepted as-is,
// so a swapped id/secret or a garbage value only surfaced as a confusing failure on the
// first deploy). Modal still does the authoritative check on first call.
func (m *ModalAuth) CheckAuthentication(_ context.Context) (bool, error) {
	id, secret := os.Getenv("MODAL_TOKEN_ID"), os.Getenv("MODAL_TOKEN_SECRET")
	switch {
	case id != "" && secret != "":
		if err := validateModalTokenPair(id, secret); err != nil {
			return false, err
		}
		return true, nil
	case id != "" || secret != "":
		return false, errors.Errorf("Modal needs BOTH MODAL_TOKEN_ID and MODAL_TOKEN_SECRET — only one is set")
	}
	if home, err := os.UserHomeDir(); err == nil {
		if _, err := os.Stat(filepath.Join(home, ".modal.toml")); err == nil {
			return true, nil
		}
	}
	return false, nil
}

// validateModalTokenPair checks the documented Modal token shape — id "ak-…", secret "as-…"
// — so a swapped or garbage pair is caught locally rather than on first deploy.
func validateModalTokenPair(id, secret string) error {
	if !strings.HasPrefix(strings.TrimSpace(id), "ak-") {
		return errors.Errorf(`MODAL_TOKEN_ID should start with "ak-" (did you swap it with the secret?)`)
	}
	if !strings.HasPrefix(strings.TrimSpace(secret), "as-") {
		return errors.Errorf(`MODAL_TOKEN_SECRET should start with "as-" (did you swap it with the token id?)`)
	}
	return nil
}

// ValidateAPIKey checks a Modal token has the documented shape (an "ak-"/"as-" prefix)
// instead of accepting any non-empty string; Modal still validates authoritatively on first
// call.
func (m *ModalAuth) ValidateAPIKey(_ context.Context, token string) (bool, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return false, nil
	}
	if strings.HasPrefix(token, "ak-") || strings.HasPrefix(token, "as-") {
		return true, nil
	}
	return false, errors.Errorf(`that doesn't look like a Modal token (expected an "ak-" or "as-" prefix)`)
}

// PerformOAuthLogin points the user at Modal's own auth flow.
func (m *ModalAuth) PerformOAuthLogin(_ context.Context) error {
	return errors.Errorf("run `modal token new` to authenticate, or set MODAL_TOKEN_ID and MODAL_TOKEN_SECRET")
}

// APIKeyPrompt is shown when credentials are missing.
func (m *ModalAuth) APIKeyPrompt() string {
	return "Set MODAL_TOKEN_ID and MODAL_TOKEN_SECRET (or run `modal token new`)"
}
