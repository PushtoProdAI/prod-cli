package auth

import (
	"context"
	"testing"
)

func TestModalTokenValidation(t *testing.T) {
	m := NewModalAuth(nil)

	// well-formed pair via env
	t.Setenv("MODAL_TOKEN_ID", "ak-abc123")
	t.Setenv("MODAL_TOKEN_SECRET", "as-def456")
	if ok, err := m.CheckAuthentication(context.Background()); !ok || err != nil {
		t.Errorf("valid pair: ok=%v err=%v", ok, err)
	}

	// swapped id/secret is caught locally
	t.Setenv("MODAL_TOKEN_ID", "as-def456")
	t.Setenv("MODAL_TOKEN_SECRET", "ak-abc123")
	if ok, err := m.CheckAuthentication(context.Background()); ok || err == nil {
		t.Errorf("swapped pair should be rejected: ok=%v err=%v", ok, err)
	}

	// only one of the pair set
	t.Setenv("MODAL_TOKEN_ID", "ak-abc123")
	t.Setenv("MODAL_TOKEN_SECRET", "")
	if ok, err := m.CheckAuthentication(context.Background()); ok || err == nil {
		t.Errorf("half-set pair should be rejected: ok=%v err=%v", ok, err)
	}
}

func TestModalValidateAPIKey(t *testing.T) {
	m := NewModalAuth(nil)
	for _, tok := range []string{"ak-abc", "as-def"} {
		if ok, err := m.ValidateAPIKey(context.Background(), tok); !ok || err != nil {
			t.Errorf("%q should validate: ok=%v err=%v", tok, ok, err)
		}
	}
	if ok, _ := m.ValidateAPIKey(context.Background(), ""); ok {
		t.Error("empty token should not validate")
	}
	if ok, err := m.ValidateAPIKey(context.Background(), "totally-bogus"); ok || err == nil {
		t.Error("a non-Modal-shaped token should be rejected with a helpful error")
	}
}
