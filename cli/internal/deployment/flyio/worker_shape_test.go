package flyio

import (
	"testing"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
)

// 2B: a worker/cron shape must produce a portless Fly app — NO [[services]] block — so
// Fly runs the container CMD without an HTTP health check that a non-listening worker
// would fail (which fails the whole deploy). Web/mcp-server keep the HTTP service + PORT.
func TestGenerateFlyConfigServicesByShape(t *testing.T) {
	cases := []struct {
		shape          deployment.DeployShape
		wantHTTPServic bool
	}{
		{deployment.ShapeWeb, true},
		{deployment.ShapeMCPServer, true},
		{deployment.ShapeWorker, false},
		{deployment.ShapeCron, false},
	}
	for _, tc := range cases {
		fqd := &FlyioQueuedDeployment{
			spec: &deployment.DeploymentSpec{
				Name:         "agent",
				Language:     "python",
				StartCommand: "python worker.py",
				Shape:        tc.shape,
			},
		}
		cfg := fqd.generateFlyConfig()

		if got := len(cfg.Services) > 0; got != tc.wantHTTPServic {
			t.Errorf("shape %q: has HTTP service = %v, want %v", tc.shape, got, tc.wantHTTPServic)
		}
		_, hasPort := cfg.EnvVars["PORT"]
		if hasPort != tc.wantHTTPServic {
			t.Errorf("shape %q: PORT env set = %v, want %v (only HTTP shapes bind a port)", tc.shape, hasPort, tc.wantHTTPServic)
		}
	}
}
