package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/pushtoprodai/prod-cli/internal/analyzer"
	"github.com/pushtoprodai/prod-cli/internal/deployment"
)

// A dry-run deploy must show the plan + cost and STOP — no confirm, no deploy.
func TestProceedWithPlanDryRun(t *testing.T) {
	a := &Agent{dryRun: true, interactive: false}
	var out strings.Builder

	plan := DeployPlan{
		Action:   Deploy,
		Platform: FlyIO, // not gated, so it reaches the dry-run branch
		Spec:     analyzer.ProjectSpec{Name: "myapp", Language: "node"},
		Pricing:  deployment.CostEstimate{Total: 7.0},
	}

	next, err := a.proceedWithPlan(context.Background(), plan, "", &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Non-interactive dry run stops the state machine (nil next state) rather than
	// advancing to confirm/deploy.
	if next != nil {
		t.Error("dry run should stop (nil next state), not proceed to confirm")
	}

	s := out.String()
	if !strings.Contains(s, "Dry run") {
		t.Errorf("expected a dry-run notice, got: %q", s)
	}
	if !strings.Contains(s, "7.00") {
		t.Errorf("expected the estimated cost, got: %q", s)
	}
}

// Without dry-run, the same plan advances (non-nil next state) — proving the
// dry-run branch is what stops it, not the plan being invalid.
func TestProceedWithPlanDeploysWhenNotDryRun(t *testing.T) {
	a := &Agent{dryRun: false, interactive: false}
	var out strings.Builder

	plan := DeployPlan{
		Action:   Deploy,
		Platform: FlyIO,
		Spec:     analyzer.ProjectSpec{Name: "myapp", Language: "node"},
	}

	next, err := a.proceedWithPlan(context.Background(), plan, "", &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if next == nil {
		t.Error("a valid non-dry-run plan should advance (non-nil next state)")
	}
}
