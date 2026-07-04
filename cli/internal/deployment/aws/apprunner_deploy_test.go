package aws

import (
	"testing"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
)

func TestSplitEnvVars(t *testing.T) {
	plain, secrets := splitEnvVars([]deployment.EnvVar{
		{Name: "FOO", Value: "bar"},
		{Name: "DATABASE_URL", Value: "postgres://x", Sensitive: true},
	})

	if plain["FOO"] != "bar" {
		t.Errorf("plain FOO = %q", plain["FOO"])
	}
	if plain["PORT"] != defaultPort {
		t.Errorf("PORT should be forced to %q, got %q", defaultPort, plain["PORT"])
	}
	if secrets["DATABASE_URL"] != "postgres://x" {
		t.Errorf("secret DATABASE_URL = %q", secrets["DATABASE_URL"])
	}
	if _, ok := plain["DATABASE_URL"]; ok {
		t.Error("sensitive var leaked into the plain map")
	}
}

func TestSplitEnvVarsNoSecrets(t *testing.T) {
	plain, secrets := splitEnvVars([]deployment.EnvVar{{Name: "FOO", Value: "bar"}})
	if secrets != nil {
		t.Errorf("secrets should be nil when there are none, got %v", secrets)
	}
	if plain["PORT"] != defaultPort {
		t.Error("PORT should always be set")
	}
}

// A user PORT (even marked sensitive) must never land in both maps — App Runner
// rejects a key present in both RuntimeEnvironmentVariables and *Secrets.
func TestSplitEnvVarsPortNeverDuplicated(t *testing.T) {
	plain, secrets := splitEnvVars([]deployment.EnvVar{{Name: "PORT", Value: "3000", Sensitive: true}})
	if _, ok := secrets["PORT"]; ok {
		t.Error("PORT must not remain in the secrets map")
	}
	if plain["PORT"] != defaultPort {
		t.Errorf("PORT = %q, want forced %q", plain["PORT"], defaultPort)
	}
}
