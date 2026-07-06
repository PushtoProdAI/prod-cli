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

// CheckAuthentication reports whether Modal credentials are available locally.
func (m *ModalAuth) CheckAuthentication(_ context.Context) (bool, error) {
	if os.Getenv("MODAL_TOKEN_ID") != "" && os.Getenv("MODAL_TOKEN_SECRET") != "" {
		return true, nil
	}
	if home, err := os.UserHomeDir(); err == nil {
		if _, err := os.Stat(filepath.Join(home, ".modal.toml")); err == nil {
			return true, nil
		}
	}
	return false, nil
}

// ValidateAPIKey accepts any non-empty token — Modal validates it on first call.
func (m *ModalAuth) ValidateAPIKey(_ context.Context, token string) (bool, error) {
	return strings.TrimSpace(token) != "", nil
}

// PerformOAuthLogin points the user at Modal's own auth flow.
func (m *ModalAuth) PerformOAuthLogin(_ context.Context) error {
	return errors.Errorf("run `modal token new` to authenticate, or set MODAL_TOKEN_ID and MODAL_TOKEN_SECRET")
}

// APIKeyPrompt is shown when credentials are missing.
func (m *ModalAuth) APIKeyPrompt() string {
	return "Set MODAL_TOKEN_ID and MODAL_TOKEN_SECRET (or run `modal token new`)"
}
