package gcprun

import (
	"strings"
	"testing"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
	"google.golang.org/api/googleapi"
	secretmanager "google.golang.org/api/secretmanager/v1"
)

func TestSecretID(t *testing.T) {
	if got := secretID("my.app", "DB/URL"); got != "my-app-DB-URL" {
		t.Errorf("secretID = %q, want my-app-DB-URL (invalid chars → -)", got)
	}
	if got := secretID("api", "DATABASE_URL"); got != "api-DATABASE_URL" {
		t.Errorf("secretID = %q, want api-DATABASE_URL (valid chars kept)", got)
	}
	if got := secretID(strings.Repeat("a", 300), "X"); len(got) > 255 {
		t.Errorf("secretID length = %d, must be ≤255", len(got))
	}
}

func TestAddMember(t *testing.T) {
	member := "serviceAccount:123-compute@developer.gserviceaccount.com"

	// Empty policy → new binding.
	got := addMember(nil, secretAccessorRole, member)
	if len(got) != 1 || got[0].Role != secretAccessorRole || len(got[0].Members) != 1 {
		t.Fatalf("expected a new accessor binding, got %+v", got)
	}

	// Idempotent: adding the same member again is a no-op.
	got = addMember(got, secretAccessorRole, member)
	if len(got[0].Members) != 1 {
		t.Errorf("re-adding a member should be a no-op, got %v", got[0].Members)
	}

	// Preserves an unrelated binding (read-modify-write, not blind replace).
	existing := []*secretmanager.Binding{{Role: "roles/other", Members: []string{"user:a@b.c"}}}
	got = addMember(existing, secretAccessorRole, member)
	if len(got) != 2 {
		t.Errorf("existing bindings must be preserved, got %+v", got)
	}
}

func TestPartitionEnvVars(t *testing.T) {
	plain, sensitive := partitionEnvVars([]deployment.EnvVar{
		{Name: "PUBLIC", Value: "1"},
		{Name: "DATABASE_URL", Value: "postgres://x", Sensitive: true},
	})
	if plain["PUBLIC"] != "1" || len(plain) != 1 {
		t.Errorf("plain = %+v", plain)
	}
	if sensitive["DATABASE_URL"] != "postgres://x" || len(sensitive) != 1 {
		t.Errorf("sensitive = %+v", sensitive)
	}
}

func TestBuildServiceSecretRef(t *testing.T) {
	svc := buildService(ServiceConfig{
		Name:      "app",
		Port:      8080,
		CPU:       "1000m",
		Memory:    "512Mi",
		EnvVars:   map[string]string{"PORT": "8080"},
		SecretEnv: map[string]string{"DATABASE_URL": "projects/p/secrets/app-DATABASE_URL"},
	})
	var plain, secret int
	for _, e := range svc.Template.Containers[0].Env {
		if e.ValueSource != nil && e.ValueSource.SecretKeyRef != nil {
			secret++
			if e.Name != "DATABASE_URL" || e.ValueSource.SecretKeyRef.Version != "latest" ||
				e.ValueSource.SecretKeyRef.Secret != "projects/p/secrets/app-DATABASE_URL" {
				t.Errorf("bad SecretKeyRef: %+v", e.ValueSource.SecretKeyRef)
			}
			if e.Value != "" {
				t.Errorf("a secret-ref env must not also carry an inline value: %q", e.Value)
			}
		} else {
			plain++
		}
	}
	if plain != 1 || secret != 1 {
		t.Errorf("want 1 plain + 1 secret env, got %d + %d", plain, secret)
	}
}

func TestIsAlreadyExists(t *testing.T) {
	if !isAlreadyExists(&googleapi.Error{Code: 409}) {
		t.Error("409 should be treated as already-exists")
	}
	if isAlreadyExists(&googleapi.Error{Code: 500}) {
		t.Error("500 must not be treated as already-exists")
	}
}
