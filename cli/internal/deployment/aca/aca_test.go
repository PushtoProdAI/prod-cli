package aca

import (
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/pushtoprodai/prod-cli/internal/deployment"
)

func TestDeriveACRName(t *testing.T) {
	// Hyphens and other non-alphanumerics are stripped (ACR names are alphanumeric).
	if got := deriveACRName("prod-apps"); got != "prodacrprodapps" {
		t.Errorf("deriveACRName = %q, want prodacrprodapps", got)
	}
	// Result stays within the 50-char ACR limit.
	long := deriveACRName("this-is-a-very-long-resource-group-name-well-beyond-fifty-characters")
	if len(long) > 50 {
		t.Errorf("derived name %q exceeds 50 chars (%d)", long, len(long))
	}
}

func TestContainerAppName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"my-app", "my-app"},
		{"3d-app", "app-3d-app"}, // digit start → letter-prefixed
		{"", "app"},
	}
	for _, c := range cases {
		if got := containerAppName(c.in); got != c.want {
			t.Errorf("containerAppName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// A too-long name is clamped to ≤32, starts with a letter, no trailing hyphen.
	long := containerAppName("a-really-long-project-name-that-exceeds-thirty-two-characters-easily")
	if len(long) > 32 {
		t.Errorf("clamped name %q exceeds 32 chars (%d)", long, len(long))
	}
	if long[0] < 'a' || long[0] > 'z' {
		t.Errorf("clamped name %q must start with a letter", long)
	}
	if long[len(long)-1] == '-' {
		t.Errorf("clamped name %q must not end with a hyphen", long)
	}
}

func TestIngressFqdn(t *testing.T) {
	app := armappcontainers.ContainerApp{
		Properties: &armappcontainers.ContainerAppProperties{
			Configuration: &armappcontainers.Configuration{
				Ingress: &armappcontainers.Ingress{Fqdn: to.Ptr("myapp.eastus.azurecontainerapps.io")},
			},
		},
	}
	if got := ingressFqdn(app); got != "myapp.eastus.azurecontainerapps.io" {
		t.Errorf("ingressFqdn = %q", got)
	}
	// Nil-safe when ingress isn't populated.
	if got := ingressFqdn(armappcontainers.ContainerApp{}); got != "" {
		t.Errorf("ingressFqdn on empty app = %q, want empty", got)
	}
}

func acaRevision(name string, created time.Time, active bool, weight int32) *armappcontainers.Revision {
	return &armappcontainers.Revision{
		Name: to.Ptr(name),
		Properties: &armappcontainers.RevisionProperties{
			Active:        to.Ptr(active),
			CreatedTime:   to.Ptr(created),
			TrafficWeight: to.Ptr(weight),
		},
	}
}

func TestACAServingAndPreviousRevision(t *testing.T) {
	t1 := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC)

	revs := []*armappcontainers.Revision{
		acaRevision("app--r3", t3, true, 100), // serving
		acaRevision("app--r2", t2, true, 0),
		acaRevision("app--r1", t1, true, 0),
	}

	if s := servingRevisionName(revs); s != "app--r3" {
		t.Errorf("servingRevisionName = %q, want app--r3", s)
	}
	if got := previousActiveRevision(revs, "app--r3"); got != "app--r2" {
		t.Errorf("previousActiveRevision = %q, want app--r2", got)
	}
	// Walk back: after a rollback pins r2, a further rollback goes to r1.
	if got := previousActiveRevision(revs, "app--r2"); got != "app--r1" {
		t.Errorf("walk-back = %q, want app--r1", got)
	}

	// An inactive previous revision is skipped.
	withInactive := []*armappcontainers.Revision{
		acaRevision("app--r3", t3, true, 100),
		acaRevision("app--r2", t2, false, 0),
		acaRevision("app--r1", t1, true, 0),
	}
	if got := previousActiveRevision(withInactive, "app--r3"); got != "app--r1" {
		t.Errorf("previousActiveRevision(inactive r2) = %q, want app--r1", got)
	}

	// Nothing older to roll back to.
	if got := previousActiveRevision(revs[2:], "app--r1"); got != "" {
		t.Errorf("previousActiveRevision(only one) = %q, want empty", got)
	}
	// No explicit traffic weight → the newest active revision serves (route-to-latest).
	if s := servingRevisionName([]*armappcontainers.Revision{acaRevision("x", t1, true, 0)}); s != "x" {
		t.Errorf("servingRevisionName(no weight) = %q, want x (newest active)", s)
	}
}

func TestPartitionEnv(t *testing.T) {
	plain, secret := partitionEnv([]deployment.EnvVar{
		{Name: "PUBLIC_URL", Value: "https://x"},
		{Name: "DATABASE_URL", Value: "postgres://secret", Sensitive: true},
	})
	if plain["PUBLIC_URL"] != "https://x" {
		t.Errorf("PUBLIC_URL should be plain, got %q", plain["PUBLIC_URL"])
	}
	if plain["PORT"] != "8080" {
		t.Errorf("PORT should be forced into plain env, got %q", plain["PORT"])
	}
	if _, ok := plain["DATABASE_URL"]; ok {
		t.Error("a sensitive var must NOT appear in plain env")
	}
	if secret["DATABASE_URL"] != "postgres://secret" {
		t.Errorf("sensitive var should be in the secret set, got %q", secret["DATABASE_URL"])
	}
}

func TestSecretName(t *testing.T) {
	cases := map[string]string{
		"DATABASE_URL": "database-url",
		"API.KEY":      "api-key",
		"_weird_":      "weird", // leading/trailing separators trimmed
	}
	for in, want := range cases {
		if got := secretName(in); got != want {
			t.Errorf("secretName(%q) = %q, want %q", in, got, want)
		}
	}
}
