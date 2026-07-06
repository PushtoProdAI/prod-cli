package aca

import (
	"testing"

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

func TestEnvMapForcesPort(t *testing.T) {
	m := envMap([]deployment.EnvVar{
		{Name: "FOO", Value: "bar"},
		{Name: "SECRET", Value: "s", Sensitive: true},
	})
	if m["FOO"] != "bar" {
		t.Errorf("FOO = %q", m["FOO"])
	}
	if m["PORT"] != "8080" {
		t.Errorf("PORT should be forced to the container port, got %q", m["PORT"])
	}
	if m["SECRET"] != "s" {
		t.Errorf("SECRET should be present (plain env for v1), got %q", m["SECRET"])
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
