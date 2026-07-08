package agent

import (
	"testing"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
)

func TestApplyEnvOverrides(t *testing.T) {
	a := &Agent{}
	a.SetEnvOverrides(map[string]string{
		"DATABASE_URL":   "postgres://real", // overrides a detected var
		"OPENAI_API_KEY": "sk-xyz",          // undetected, secret-looking
		"LOG_LEVEL":      "debug",           // undetected, not secret
	})
	in := []deployment.EnvVar{
		{Name: "DATABASE_URL", Role: deployment.EnvRoleFullURI, Sensitive: true},
		{Name: "PORT", Value: "8080"},
	}
	out, applied := a.applyEnvOverrides(in)

	byName := map[string]deployment.EnvVar{}
	for _, e := range out {
		byName[e.Name] = e
	}
	if byName["DATABASE_URL"].Value != "postgres://real" || !applied["DATABASE_URL"] {
		t.Errorf("DATABASE_URL should be overridden + applied: %+v", byName["DATABASE_URL"])
	}
	if !byName["DATABASE_URL"].Sensitive {
		t.Error("overriding a value must not clear the categorized Sensitive flag")
	}
	// Undetected secret-looking var → appended + sensitive (routes to secrets).
	if byName["OPENAI_API_KEY"].Value != "sk-xyz" || !byName["OPENAI_API_KEY"].Sensitive {
		t.Errorf("OPENAI_API_KEY should be appended sensitive: %+v", byName["OPENAI_API_KEY"])
	}
	// Undetected non-secret var → appended, not sensitive (plaintext ok).
	if byName["LOG_LEVEL"].Sensitive {
		t.Error("LOG_LEVEL should not be flagged sensitive")
	}
	// Untouched var is not marked applied.
	if applied["PORT"] || byName["PORT"].Value != "8080" {
		t.Error("PORT should be untouched")
	}
}

func TestLooksSensitive(t *testing.T) {
	for _, n := range []string{"OPENAI_API_KEY", "AUTH_SECRET", "DB_PASSWORD", "GITHUB_TOKEN", "AWS_ACCESS_KEY_ID", "APIKEY", "PRIVATE_KEY"} {
		if !looksSensitive(n) {
			t.Errorf("%s should look sensitive", n)
		}
	}
	for _, n := range []string{"LOG_LEVEL", "PORT", "NODE_ENV", "REGION", "DEBUG", "TIMEOUT"} {
		if looksSensitive(n) {
			t.Errorf("%s should NOT look sensitive", n)
		}
	}
}
