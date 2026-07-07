package vercel

import (
	"testing"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
)

func TestHasDatabaseURL(t *testing.T) {
	if hasDatabaseURL([]string{"NODE_ENV=production", "DATABASE_URL="}) {
		t.Error("empty DATABASE_URL must count as no database URL (the Vercel case)")
	}
	if hasDatabaseURL([]string{"FOO=bar"}) {
		t.Error("no DATABASE_URL at all → false")
	}
	if !hasDatabaseURL([]string{"DATABASE_URL=postgres://u:p@host/db"}) {
		t.Error("a real DATABASE_URL → true")
	}
	if !hasDatabaseURL([]string{"POSTGRES_URL=postgres://x"}) {
		t.Error("POSTGRES_URL should count")
	}
}

func TestNonEmptyEnvVars(t *testing.T) {
	in := []deployment.EnvVar{
		{Name: "ANTHROPIC_API_KEY", Value: "sk-ant-x"},
		{Name: "DATABASE_URL", Value: ""},       // empty → must NOT be set (collides with Neon)
		{Name: "BETTER_AUTH_URL", Value: "   "}, // whitespace-only → empty
		{Name: "NODE_ENV", Value: "production"},
	}
	kept, skipped := nonEmptyEnvVars(in)
	if len(kept) != 2 || kept[0].Name != "ANTHROPIC_API_KEY" || kept[1].Name != "NODE_ENV" {
		t.Errorf("kept = %+v, want the two non-empty vars", kept)
	}
	if len(skipped) != 2 || skipped[0] != "DATABASE_URL" || skipped[1] != "BETTER_AUTH_URL" {
		t.Errorf("skipped = %v, want [DATABASE_URL BETTER_AUTH_URL]", skipped)
	}
}
