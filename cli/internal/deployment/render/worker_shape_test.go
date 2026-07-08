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
		name     string
		shape    deployment.DeployShape
		schedule string
		wantType string
	}{
		{"web", deployment.ShapeWeb, "", "web_service"},
		{"mcp-server", deployment.ShapeMCPServer, "", "web_service"},
		{"worker", deployment.ShapeWorker, "", "background_worker"},
		// A cron WITH a schedule is a real cron_job; without one it falls back to a worker
		// (planning degrades a scheduleless cron before it reaches here, but defend anyway).
		{"cron with schedule", deployment.ShapeCron, "0 2 * * *", "cron_job"},
		{"cron without schedule", deployment.ShapeCron, "", "background_worker"},
	}
	for _, tc := range cases {
		qd := &QueuedDeployment{
			spec: &deployment.DeploymentSpec{
				Name:         "agent",
				Language:     "python",
				StartCommand: "python worker.py",
				Shape:        tc.shape,
				Schedule:     tc.schedule,
			},
		}

		if got := qd.renderServiceType(); got != tc.wantType {
			t.Errorf("%s: renderServiceType() = %q, want %q", tc.name, got, tc.wantType)
		}

		// The live deploy path threads the type + schedule through createWebServiceStep, so
		// assert the actual step carries them (not just the helper).
		step := qd.createWebServiceStep("owner-1", nil, nil, &deploymentConfig{}, 1)
		ws, ok := step.(*CreateWebServiceStep)
		if !ok {
			t.Fatalf("%s: expected *CreateWebServiceStep, got %T", tc.name, step)
		}
		if ws.Type != tc.wantType {
			t.Errorf("%s: step Type = %q, want %q", tc.name, ws.Type, tc.wantType)
		}
		if ws.Schedule != tc.schedule {
			t.Errorf("%s: step Schedule = %q, want %q", tc.name, ws.Schedule, tc.schedule)
		}
	}
}
