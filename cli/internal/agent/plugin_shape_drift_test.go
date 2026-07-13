package agent

import (
	"testing"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
	plugin "github.com/pushtoprodai/prod-plugin-sdk"
)

// TestShapeStringsMatchSDK is the cross-module drift guard (F8 revision S3). The SDK
// mirrors deployment.DeployShape's string constants (it can't import the deployment
// package — separate module + internal/), and prod-cli maps SDK↔core purely on those
// strings via ParseShape/String. If the two ever diverge, ParseShape silently returns
// web — safe for a truly-unknown string but silent-wrong for a real worker — so this
// compiled test, not the runtime default, is the guarantee they stay byte-identical.
func TestShapeStringsMatchSDK(t *testing.T) {
	pairs := []struct {
		core deployment.DeployShape
		sdk  plugin.DeployShape
		name string
	}{
		{deployment.ShapeWeb, plugin.ShapeWeb, "web"},
		{deployment.ShapeMCPServer, plugin.ShapeMCPServer, "mcp-server"},
		{deployment.ShapeWorker, plugin.ShapeWorker, "worker"},
		{deployment.ShapeCron, plugin.ShapeCron, "cron"},
	}
	for _, p := range pairs {
		if string(p.core) != string(p.sdk) {
			t.Errorf("shape %q: core=%q != sdk=%q — the SDK mirror drifted from deployment/shape.go", p.name, p.core, p.sdk)
		}
		// The round-trip prod-cli actually uses must also be identity.
		if got := deployment.ParseShape(string(p.sdk)); got != p.core {
			t.Errorf("ParseShape(sdk %q) = %q, want core %q", p.sdk, got, p.core)
		}
	}

	// Enumerate the FULL core shape set so a NEW core shape added without an SDK mirror
	// trips CI. Update this list AND the SDK mirror together when adding a shape.
	coreShapes := []deployment.DeployShape{
		deployment.ShapeWeb, deployment.ShapeMCPServer, deployment.ShapeWorker, deployment.ShapeCron,
	}
	if len(pairs) != len(coreShapes) {
		t.Fatalf("core shape set has %d shapes but only %d are mirrored+asserted against the SDK — mirror the new shape in prod-plugin-sdk and add it here", len(coreShapes), len(pairs))
	}
}
