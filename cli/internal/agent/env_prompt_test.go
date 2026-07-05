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

// The awaitingSecret flag is what the console one-shot driver reads to decide
// whether to mask the next input; it must track the current var's sensitivity.
func TestAwaitingSecretFlagTracksSensitivity(t *testing.T) {
	t.Setenv("PROD_JSON_MODE", "")

	// Sensitive var → flag set so the console driver reads masked.
	a := &Agent{envVars: []EnvVarWithStatus{
		{EnvVar: deployment.EnvVar{Name: "SECRET_KEY", Sensitive: true}, Status: "pending"},
	}}
	if _, err := a.promptForEnvVarValue(context.Background(), "", &promptRecorder{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !a.awaitingSecret {
		t.Error("awaitingSecret must be set when prompting for a sensitive var")
	}

	// Non-sensitive var → flag cleared even if a previous prompt had set it.
	a2 := &Agent{
		awaitingSecret: true,
		envVars: []EnvVarWithStatus{
			{EnvVar: deployment.EnvVar{Name: "PORT"}, Status: "pending"},
		},
	}
	if _, err := a2.promptForEnvVarValue(context.Background(), "", &promptRecorder{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a2.awaitingSecret {
		t.Error("awaitingSecret must be cleared when prompting for a non-sensitive var")
	}
}
