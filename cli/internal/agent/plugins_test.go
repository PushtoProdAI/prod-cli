package agent

import (
	"testing"

	"github.com/pushtoprodai/prod-cli/internal/analyzer"
	"github.com/pushtoprodai/prod-cli/internal/pluginhost"
)

func TestPluginPlatformIdentity(t *testing.T) {
	p := pluginPlatform("Acme Cloud")
	if !IsPlugin(p) {
		t.Errorf("a plugin platform must satisfy IsPlugin, got %d", p)
	}
	// Deterministic per name (so a persisted DeployPlan resumes to the same plugin).
	if pluginPlatform("Acme Cloud") != p {
		t.Error("pluginPlatform must be deterministic for a name")
	}
	if pluginPlatform("Other Cloud") == p {
		t.Error("different plugin names should not share a Platform value")
	}
	// Built-ins are never plugins.
	if IsPlugin(AWS) || IsPlugin(GoogleCloudRun) || IsPlugin(UnknownPlatform) {
		t.Error("built-in platforms must not satisfy IsPlugin")
	}
	// DisplayName falls back to String() for an unregistered plugin value.
	if got := pluginPlatform("nope").DisplayName(); got == "" {
		t.Error("DisplayName should fall back to String(), not empty")
	}
}

// snapshotCatalog restores the package-global catalog after a test that registers a
// plugin, so tests don't depend on file ordering.
func snapshotCatalog(t *testing.T) {
	cat := append([]PlatformSpec(nil), platformCatalog...)
	idx := make(map[Platform]PlatformSpec, len(platformByEnum))
	for k, v := range platformByEnum {
		idx[k] = v
	}
	t.Cleanup(func() { platformCatalog, platformByEnum = cat, idx })
}

func TestRegisterPluginAndDispatch(t *testing.T) {
	snapshotCatalog(t)
	e := pluginhost.Entry{
		Name:             "L3 Test Cloud",
		Aliases:          []string{"l3test", "l3-test-cloud"},
		DomainSuffix:     ".l3test.dev",
		SupportsRollback: false,
		Path:             "/nonexistent/prod-provider-l3test",
		Checksum:         "abc123", // required
	}
	if err := registerPlugin(e); err != nil {
		t.Fatalf("registerPlugin: %v", err)
	}

	p := pluginPlatform(e.Name)
	spec, ok := LookupPlatform(p)
	if !ok {
		t.Fatal("plugin not registered in the catalog")
	}
	if spec.Name != e.Name || !spec.ManagedContainer || spec.DomainSuffix != ".l3test.dev" {
		t.Errorf("spec = %+v", spec)
	}
	if spec.NewDeployable == nil || spec.NewAuthProvider == nil {
		t.Error("plugin spec must have deployable + auth factories")
	}
	// Resolves by name and alias through the catalog, so NL + menu selection work.
	if got, ok := PlatformByString("l3test"); !ok || got != p {
		t.Errorf("PlatformByString(alias) = %v, %v", got, ok)
	}
	if got, ok := PlatformByString("L3 Test Cloud"); !ok || got != p {
		t.Errorf("PlatformByString(name) = %v, %v", got, ok)
	}
	// The catalog-driven rollback gate reflects the plugin's capability.
	if _, gated := unsupportedLocalPlatform(p); !gated {
		t.Error("a plugin with SupportsRollback=false should be gated")
	}

	// Re-registering the same name collides (deterministic Platform value).
	if err := registerPlugin(e); err == nil {
		t.Error("re-registering the same plugin should collide")
	}
	// An alias that shadows a built-in is rejected.
	if err := registerPlugin(pluginhost.Entry{Name: "Bad Alias Cloud", Aliases: []string{"aws"}, Path: "/x"}); err == nil {
		t.Error("a plugin aliasing a built-in must be rejected")
	}
}

func TestCanonicalFrameworkPlatform(t *testing.T) {
	// A plugin maps to a node-container built-in, so the JS-framework switches treat it
	// like App Runner/Cloud Run/Azure (AC10) instead of hitting "unsupported platform".
	if got := canonicalFrameworkPlatform(pluginPlatform("Some Cloud")); got != AWS {
		t.Errorf("plugin should map to AWS (a node-container built-in), got %v", got)
	}
	// Built-in platforms pass through unchanged (no behavior change for them).
	for _, p := range []Platform{FlyIO, Render, Vercel, Netlify, Heroku, AWS, GoogleCloudRun, Azure} {
		if got := canonicalFrameworkPlatform(p); got != p {
			t.Errorf("built-in %v must pass through, got %v", p, got)
		}
	}
	// Concretely: SvelteKit package.json patching for a plugin equals its AWS output
	// (the node-server branch), not the default.
	h := &SvelteKitHandler{}
	pkg := []byte(`{"name":"app","dependencies":{}}`)
	awsOut, _, _ := h.PatchPackageJSON(pkg, AWS)
	plugOut, _, err := h.PatchPackageJSON(pkg, canonicalFrameworkPlatform(pluginPlatform("Some Cloud")))
	if err != nil {
		t.Fatalf("SvelteKit on a plugin errored: %v", err)
	}
	if string(plugOut) != string(awsOut) {
		t.Errorf("plugin package.json should match the AWS (node-server) output")
	}

	// PrepareDeployment must set the framework start command for a plugin too — the
	// gap where a plugin would otherwise fall through and lose "node build".
	sv := &SvelteKitHandler{}
	plugP := canonicalFrameworkPlatform(pluginPlatform("Some Cloud"))
	awsPlan := sv.PrepareDeployment(DeployPlan{Platform: AWS, Spec: analyzer.ProjectSpec{Name: "app"}})
	plugPlan := sv.PrepareDeployment(DeployPlan{Platform: plugP, Spec: analyzer.ProjectSpec{Name: "app"}})
	if awsPlan.Spec.StartCommand == "" || plugPlan.Spec.StartCommand != awsPlan.Spec.StartCommand {
		t.Errorf("plugin PrepareDeployment StartCommand = %q, want AWS's %q", plugPlan.Spec.StartCommand, awsPlan.Spec.StartCommand)
	}
}
