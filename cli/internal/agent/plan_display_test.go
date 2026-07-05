package agent

import (
	"strings"
	"testing"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
)

// The real console-deploy path is sendPlan's plaintext fallback (a non-TUI
// writer). It must show a non-default shape and the estimated cost, so a spend
// decision is made with the numbers in view.
func TestSendPlanConsoleShowsShapeAndCost(t *testing.T) {
	a := &Agent{}
	var out strings.Builder
	a.sendPlan(&out, DeployPlan{
		Action:   Deploy,
		Platform: FlyIO,
		Summary:  "deploy to fly",
		Shape:    deployment.ShapeWorker,
		Pricing:  deployment.CostEstimate{Total: 7.0},
	})

	s := out.String()
	if !strings.Contains(s, "worker") {
		t.Errorf("console plan should show the shape, got %q", s)
	}
	if !strings.Contains(s, "7.00") {
		t.Errorf("console plan should show the estimated cost, got %q", s)
	}
}

// The default "web" shape is suppressed as noise.
func TestSendPlanConsoleSuppressesWebShape(t *testing.T) {
	a := &Agent{}
	var out strings.Builder
	a.sendPlan(&out, DeployPlan{
		Action:   Deploy,
		Platform: FlyIO,
		Summary:  "deploy to fly",
		Shape:    deployment.ShapeWeb,
	})

	if strings.Contains(out.String(), "Shape:") {
		t.Errorf("web is the default and should be suppressed, got %q", out.String())
	}
}
