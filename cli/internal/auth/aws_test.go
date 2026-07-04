package auth

import (
	"context"
	"io"
	"strings"
	"testing"
)

// AWS uses the local credential chain — there is nothing to interactively log
// into, and no API key to prompt for.
func TestAWSAuthNoInteractiveLogin(t *testing.T) {
	a := NewAWSAuth(io.Discard)

	if err := a.PerformOAuthLogin(context.Background()); err == nil {
		t.Error("PerformOAuthLogin should return an error (AWS uses local credentials)")
	} else if !strings.Contains(err.Error(), "local AWS credentials") {
		t.Errorf("unexpected error message: %v", err)
	}

	if p := a.APIKeyPrompt(); p != "" {
		t.Errorf("APIKeyPrompt = %q, want empty", p)
	}

	if ok, err := a.ValidateAPIKey(context.Background(), "anything"); ok || err != nil {
		t.Errorf("ValidateAPIKey = (%v, %v), want (false, nil)", ok, err)
	}
}
