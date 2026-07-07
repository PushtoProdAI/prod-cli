package pluginhost

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pushtoprodai/prod-cli/pkg/plugin"
)

// TestCurateEnv is the AC9 guard: a plugin's environment must exclude prod's own
// credentials but keep PATH/HOME and the plugin's own cloud creds.
func TestCurateEnv(t *testing.T) {
	parent := []string{
		"PATH=/usr/bin", "HOME=/home/u",
		"FLY_API_TOKEN=secret", "OPENAI_API_KEY=sk-xxx", "PROD_REGISTRY_TOKEN=rt",
		"AWS_SECRET_ACCESS_KEY=aws", "ANTHROPIC_API_KEY=ak", "AZURE_CLIENT_SECRET=z",
		"GITHUB_TOKEN=ghp_x", "DIGITALOCEAN_TOKEN=dop", "DATABASE_URL=postgres://u:p@h/db",
		"REDIS_URL=redis://h", "NPM_TOKEN=npm",
		"ACME_TOKEN=acme", // the plugin's OWN cloud cred — must survive
	}
	got := curateEnv(parent)
	has := func(name string) bool {
		for _, kv := range got {
			if strings.HasPrefix(kv, name+"=") {
				return true
			}
		}
		return false
	}

	for _, blocked := range []string{
		"FLY_API_TOKEN", "OPENAI_API_KEY", "PROD_REGISTRY_TOKEN",
		"AWS_SECRET_ACCESS_KEY", "ANTHROPIC_API_KEY", "AZURE_CLIENT_SECRET",
		"GITHUB_TOKEN", "DIGITALOCEAN_TOKEN", "DATABASE_URL", "REDIS_URL", "NPM_TOKEN",
	} {
		if has(blocked) {
			t.Errorf("%s must be filtered from the plugin environment", blocked)
		}
	}
	for _, kept := range []string{"PATH", "HOME", "ACME_TOKEN"} {
		if !has(kept) {
			t.Errorf("%s must survive for the plugin", kept)
		}
	}
}

// TestLaunchSample builds the reference plugin and drives it over the real subprocess
// transport — the end-to-end proof that the go-plugin harness works.
func TestLaunchSample(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a plugin binary")
	}
	bin := filepath.Join(t.TempDir(), "prod-provider-example")
	build := exec.Command("go", "build", "-o", bin, "github.com/pushtoprodai/prod-cli/examples/prod-provider-example")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Skipf("cannot build the sample plugin: %v", err)
	}

	prov, closeFn, err := Launch(bin, nil)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	defer closeFn()

	ctx := context.Background()
	meta, err := prov.Metadata(ctx)
	if err != nil || meta.Name != "Example" {
		t.Fatalf("Metadata = %+v, %v", meta, err)
	}
	if len(meta.Aliases) == 0 || meta.Aliases[0] != "example" {
		t.Errorf("aliases = %v", meta.Aliases)
	}
	info, err := prov.RegistryInfo(ctx, "my-app")
	if err != nil || info.Host != "registry.example.dev" || info.Repository != "apps/my-app" {
		t.Errorf("RegistryInfo = %+v, %v", info, err)
	}
	res, err := prov.Deploy(ctx, plugin.DeployRequest{Name: "my-app", ImageRef: "registry.example.dev/apps/my-app:1"})
	if err != nil || res.URL != "https://my-app.example.dev" {
		t.Errorf("Deploy = %+v, %v", res, err)
	}
	// A provider without rollback returns an error over the wire.
	if err := prov.Rollback(ctx, "my-app", "old"); err == nil {
		t.Error("example rollback should error across the RPC boundary")
	}
}
