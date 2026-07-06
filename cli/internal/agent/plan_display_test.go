package agent

import (
	"strings"
	"testing"

	"github.com/pushtoprodai/prod-cli/internal/analyzer"
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

func TestConfirmMessage(t *testing.T) {
	cases := []struct {
		name string
		plan *DeployPlan
		want string
	}{
		{
			"deploy with cost",
			&DeployPlan{Action: Deploy, Platform: FlyIO, Spec: analyzer.ProjectSpec{Name: "myapp"}, Pricing: deployment.CostEstimate{Total: 7}},
			"Deploy myapp to Fly.io (~$7.00/mo)?",
		},
		{
			"deploy without cost",
			&DeployPlan{Action: Deploy, Platform: Render, Spec: analyzer.ProjectSpec{Name: "api"}},
			"Deploy api to Render?",
		},
		{
			"rollback (no cost even if set)",
			&DeployPlan{Action: Rollback, Platform: FlyIO, Spec: analyzer.ProjectSpec{Name: "myapp"}},
			"Roll back myapp to Fly.io?",
		},
		{
			"missing name",
			&DeployPlan{Action: Deploy, Platform: Heroku},
			"Deploy this project to Heroku?",
		},
	}
	for _, c := range cases {
		a := &Agent{DeployPlan: c.plan}
		if got := a.confirmMessage(); got != c.want {
			t.Errorf("%s: confirmMessage() = %q, want %q", c.name, got, c.want)
		}
	}

	// nil plan must not panic
	if (&Agent{}).confirmMessage() == "" {
		t.Error("nil-plan confirmMessage should return a fallback")
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
