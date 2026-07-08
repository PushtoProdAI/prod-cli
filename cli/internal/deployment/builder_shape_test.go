package deployment

import (
	"testing"

	"github.com/pushtoprodai/prod-cli/internal/analyzer"
)

// F1: the resolved deploy shape must reach the built DeploymentSpec so adapters can
// generate the right artifact (a portless worker vs a web service), not just liveness.
func TestDeploymentBuilderCarriesShape(t *testing.T) {
	ps := &analyzer.ProjectSpec{Name: "agent", Language: "python"}

	for _, shape := range []DeployShape{ShapeWeb, ShapeMCPServer, ShapeWorker, ShapeCron} {
		spec, err := NewDeploymentBuilder(ps, nil, shape).Build()
		if err != nil {
			t.Fatalf("Build(%q) errored: %v", shape, err)
		}
		if spec.Shape != shape {
			t.Errorf("spec.Shape = %q, want %q", spec.Shape, shape)
		}
	}

	// An unset shape defaults to web (HTTPShaped) so an adapter's worker branch can't
	// misfire on a zero value and existing web deploys are unchanged.
	spec, err := NewDeploymentBuilder(ps, nil, "").Build()
	if err != nil {
		t.Fatalf("Build(\"\") errored: %v", err)
	}
	if spec.Shape != ShapeWeb {
		t.Errorf("unset shape should default to ShapeWeb, got %q", spec.Shape)
	}
	if !spec.Shape.HTTPShaped() {
		t.Errorf("defaulted web shape must be HTTPShaped so web deploys are unchanged")
	}
}
