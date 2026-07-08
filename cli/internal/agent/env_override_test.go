package agent

import (
	"testing"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
)

func TestApplyEnvOverrides(t *testing.T) {
	a := &Agent{}
	a.SetEnvOverrides(map[string]string{
		"DATABASE_URL":   "postgres://real", // overrides a detected var
		"OPENAI_API_KEY": "sk-xyz",          // undetected
		"LOG_LEVEL":      "debug",           // undetected, not secret-named — still routes safe
	})
	in := []deployment.EnvVar{
		{Name: "DATABASE_URL", Role: deployment.EnvRoleFullURI, Sensitive: true},
		{Name: "PUBLIC_FLAG", Role: deployment.EnvRoleNotDBRelated, Sensitive: false, Value: "keep"}, // detected non-sensitive, not overridden
	}
	out, applied := a.applyEnvOverrides(in)

	byName := map[string]deployment.EnvVar{}
	for _, e := range out {
		byName[e.Name] = e
	}
	// Detected sensitive var: value overridden, Sensitive preserved.
	if byName["DATABASE_URL"].Value != "postgres://real" || !applied["DATABASE_URL"] || !byName["DATABASE_URL"].Sensitive {
		t.Errorf("DATABASE_URL should be overridden, applied, and still sensitive: %+v", byName["DATABASE_URL"])
	}
	// Undetected vars are appended and default to sensitive (fail safe) — even a benign name.
	if !byName["OPENAI_API_KEY"].Sensitive || byName["OPENAI_API_KEY"].Value != "sk-xyz" {
		t.Errorf("OPENAI_API_KEY should be appended sensitive: %+v", byName["OPENAI_API_KEY"])
	}
	if !byName["LOG_LEVEL"].Sensitive {
		t.Error("an undetected --env var must default to sensitive so a non-obvious secret can't leak to plaintext")
	}
	// A detected var that wasn't overridden keeps its (non-sensitive) categorization and is untouched.
	if applied["PUBLIC_FLAG"] || byName["PUBLIC_FLAG"].Sensitive || byName["PUBLIC_FLAG"].Value != "keep" {
		t.Errorf("PUBLIC_FLAG should be untouched: %+v", byName["PUBLIC_FLAG"])
	}
}

func TestApplyEnvOverridesNoop(t *testing.T) {
	a := &Agent{}
	in := []deployment.EnvVar{{Name: "PORT", Value: "8080"}}
	out, applied := a.applyEnvOverrides(in)
	if len(out) != 1 || applied != nil {
		t.Errorf("no overrides should be a no-op, got out=%d applied=%v", len(out), applied)
	}
}
