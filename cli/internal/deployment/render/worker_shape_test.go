package render

import (
	"testing"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
)

// 2B (Render): a worker/cron shape must produce a portless Render service —
// a background_worker, not a web_service — so Render runs the container's start
// command with no ports and no HTTP health check that a non-listening worker
// would fail (which would fail the whole deploy). Web/mcp-server keep web_service.
//
// A cron shape falls back to background_worker: Render's cron_job requires a
// schedule (a cron expression) the DeploymentSpec doesn't carry yet, and
// inventing one would be wrong.
func TestRenderServiceTypeByShape(t *testing.T) {
	cases := []struct {
		shape    deployment.DeployShape
		wantType string
	}{
		{deployment.ShapeWeb, "web_service"},
		{deployment.ShapeMCPServer, "web_service"},
		{deployment.ShapeWorker, "background_worker"},
		{deployment.ShapeCron, "background_worker"},
	}
	for _, tc := range cases {
		qd := &QueuedDeployment{
			spec: &deployment.DeploymentSpec{
				Name:         "agent",
				Language:     "python",
				StartCommand: "python worker.py",
				Shape:        tc.shape,
			},
		}

		if got := qd.renderServiceType(); got != tc.wantType {
			t.Errorf("shape %q: renderServiceType() = %q, want %q", tc.shape, got, tc.wantType)
		}

		// The live deploy path threads the type through createWebServiceStep, so
		// assert the actual step carries it (not just the helper).
		step := qd.createWebServiceStep("owner-1", nil, nil, &deploymentConfig{}, 1)
		ws, ok := step.(*CreateWebServiceStep)
		if !ok {
			t.Fatalf("shape %q: expected *CreateWebServiceStep, got %T", tc.shape, step)
		}
		if ws.Type != tc.wantType {
			t.Errorf("shape %q: web service step Type = %q, want %q", tc.shape, ws.Type, tc.wantType)
		}
	}
}
