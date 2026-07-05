package agent

import (
	"context"
	"testing"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
)

// promptRecorder is a TUIWriter that records which prompt kind was used. The
// embedded interface satisfies the type; only the methods the test exercises are
// overridden (others would panic if called, which they aren't here).
type promptRecorder struct {
	TUIWriter
	secret bool
	text   bool
}

func (p *promptRecorder) Write(b []byte) (int, error)           { return len(b), nil }
func (p *promptRecorder) SendSecretPrompt(string)               { p.secret = true }
func (p *promptRecorder) SendTextPrompt(string)                 { p.text = true }
func (p *promptRecorder) SendTextPromptWithDefault(_, _ string) { p.text = true }

func TestSensitiveEnvVarUsesMaskedPrompt(t *testing.T) {
	t.Setenv("PROD_JSON_MODE", "") // force the interactive TUI branch

	a := &Agent{envVars: []EnvVarWithStatus{
		{EnvVar: deployment.EnvVar{Name: "SECRET_KEY", Sensitive: true}, Status: "pending"},
	}}
	rec := &promptRecorder{}
	if _, err := a.promptForEnvVarValue(context.Background(), "", rec); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !rec.secret {
		t.Error("a sensitive env var must use the masked secret prompt")
	}
	if rec.text {
		t.Error("a sensitive env var must not use the plain text prompt")
	}
}

func TestNonSensitiveEnvVarUsesTextPrompt(t *testing.T) {
	t.Setenv("PROD_JSON_MODE", "")

	a := &Agent{envVars: []EnvVarWithStatus{
		{EnvVar: deployment.EnvVar{Name: "PORT"}, Status: "pending"},
	}}
	rec := &promptRecorder{}
	if _, err := a.promptForEnvVarValue(context.Background(), "", rec); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if rec.secret {
		t.Error("a non-sensitive env var must not be masked")
	}
	if !rec.text {
		t.Error("a non-sensitive env var should use the plain text prompt")
	}
}
