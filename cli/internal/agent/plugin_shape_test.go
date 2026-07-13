package agent

import (
	"testing"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
	"github.com/pushtoprodai/prod-cli/internal/pluginhost"
)

// TestPluginShapePropagation traces the 4-hop shape chain: a manifest Entry.Shapes →
// registerPlugin → PlatformSpec.Shapes → Platform.SupportsShape, and that each hop
// degrades to web-only when Shapes is empty.
func TestPluginShapePropagation(t *testing.T) {
	snapshotCatalog(t)

	// A worker/agent plugin declares its shapes in the manifest (recorded at install).
	e := pluginhost.Entry{
		Name:     "Sandbox Cloud",
		Aliases:  []string{"sandbox"},
		Shapes:   []string{"worker", "mcp-server"}, // hop 1: manifest strings
		Path:     "/nonexistent/prod-provider-sandbox",
		Checksum: "abc123",
	}
	if err := registerPlugin(e); err != nil { // hop 2: registerPlugin → PlatformSpec.Shapes
		t.Fatalf("registerPlugin: %v", err)
	}
	p := pluginPlatform(e.Name)

	spec, ok := LookupPlatform(p)
	if !ok {
		t.Fatal("plugin not registered")
	}
	// hop 3: the parsed shapes land on the spec as the core enum.
	if len(spec.Shapes) != 2 || spec.Shapes[0] != deployment.ShapeWorker || spec.Shapes[1] != deployment.ShapeMCPServer {
		t.Errorf("spec.Shapes = %v, want [worker mcp-server]", spec.Shapes)
	}

	// hop 4: SupportsShape gates on the declared set.
	if !p.SupportsShape(deployment.ShapeWorker) {
		t.Error("declared worker shape must be supported")
	}
	if !p.SupportsShape(deployment.ShapeMCPServer) {
		t.Error("declared mcp-server shape must be supported")
	}
	// web was NOT declared → not supported (the plugin is a non-web runtime).
	if p.SupportsShape(deployment.ShapeWeb) {
		t.Error("undeclared web shape must not be supported when Shapes is explicit")
	}
	if p.SupportsShape(deployment.ShapeCron) {
		t.Error("undeclared cron shape must not be supported")
	}
}

// TestSupportsShapeDegradesToWeb asserts the whole chain degrades to web-only when a
// plugin declares no shapes (an old plugin / an existing manifest with no `shapes`), and
// that every built-in cloud is web-only.
func TestSupportsShapeDegradesToWeb(t *testing.T) {
	snapshotCatalog(t)

	e := pluginhost.Entry{
		Name:     "Legacy Cloud",
		Aliases:  []string{"legacy"},
		Shapes:   nil, // no declared shapes ⇒ web-only
		Path:     "/nonexistent/prod-provider-legacy",
		Checksum: "abc123",
	}
	if err := registerPlugin(e); err != nil {
		t.Fatalf("registerPlugin: %v", err)
	}
	p := pluginPlatform(e.Name)

	spec, _ := LookupPlatform(p)
	if spec.Shapes != nil {
		t.Errorf("empty manifest shapes must map to nil, got %v", spec.Shapes)
	}
	if !p.SupportsShape(deployment.ShapeWeb) {
		t.Error("a shapeless plugin must still serve web")
	}
	if !p.SupportsShape(deployment.ShapeMCPServer) {
		t.Error("mcp-server is HTTP-shaped, so a web-only default must serve it")
	}
	if p.SupportsShape(deployment.ShapeWorker) || p.SupportsShape(deployment.ShapeCron) {
		t.Error("a shapeless (web-only) plugin must NOT serve worker/cron")
	}

	// Built-in container clouds are web-only (no Shapes declared).
	for _, bp := range []Platform{AWS, GoogleCloudRun, Azure} {
		if bp.SupportsShape(deployment.ShapeWorker) {
			t.Errorf("built-in %v must be web-only, but reported worker support", bp)
		}
		if !bp.SupportsShape(deployment.ShapeWeb) {
			t.Errorf("built-in %v must serve web", bp)
		}
	}
	// An unregistered platform is web-only.
	if UnknownPlatform.SupportsShape(deployment.ShapeWorker) {
		t.Error("an unregistered platform must default to web-only")
	}
}

// TestShouldRecordURLLess pins the container workflow's URL-less gate: a URL-less worker
// plugin takes the early-return (success), while a web plugin and a web-only cloud that
// received a worker shape both fall through to the URL requirement.
func TestShouldRecordURLLess(t *testing.T) {
	snapshotCatalog(t)

	// A worker/agent plugin: worker shape → record URL-less success (no "returned no URL").
	worker := pluginhost.Entry{Name: "Worker Cloud", Aliases: []string{"wc"}, Shapes: []string{"worker"}, Path: "/x", Checksum: "ab"}
	if err := registerPlugin(worker); err != nil {
		t.Fatalf("registerPlugin(worker): %v", err)
	}
	wp := pluginPlatform(worker.Name)

	// A plain web plugin (no declared shapes).
	web := pluginhost.Entry{Name: "Web Cloud", Aliases: []string{"webc"}, Path: "/y", Checksum: "cd"}
	if err := registerPlugin(web); err != nil {
		t.Fatalf("registerPlugin(web): %v", err)
	}
	webp := pluginPlatform(web.Name)

	cases := []struct {
		name     string
		platform Platform
		shape    deployment.DeployShape
		want     bool
	}{
		{"worker plugin + worker shape → URL-less success", wp, deployment.ShapeWorker, true},
		{"worker plugin + web shape → needs URL", wp, deployment.ShapeWeb, false},
		{"worker plugin + mcp-server (HTTP) → needs URL", wp, deployment.ShapeMCPServer, false},
		{"web plugin + worker shape → needs URL (undeclared)", webp, deployment.ShapeWorker, false},
		{"web plugin + web shape → needs URL", webp, deployment.ShapeWeb, false},
		// Defense in depth: a web-only container cloud that somehow got a worker shape.
		{"AWS + worker shape → needs URL (web-only cloud)", AWS, deployment.ShapeWorker, false},
		{"Cloud Run + web shape → needs URL", GoogleCloudRun, deployment.ShapeWeb, false},
	}
	for _, c := range cases {
		if got := shouldRecordURLLess(c.platform, c.shape); got != c.want {
			t.Errorf("%s: shouldRecordURLLess = %v, want %v", c.name, got, c.want)
		}
	}
}
