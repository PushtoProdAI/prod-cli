package agent

import (
	"testing"

	"github.com/pushtoprodai/prod-cli/internal/analyzer"
)

func TestApplyNameOverride(t *testing.T) {
	// --name wins over the analyzer/LLM name.
	a := &Agent{}
	a.SetNameOverride("myapp-pr-7")
	plan := DeployPlan{Spec: analyzer.ProjectSpec{Name: "myapp"}}
	a.applyNameOverride(&plan)
	if plan.Spec.Name != "myapp-pr-7" {
		t.Errorf("name = %q, want myapp-pr-7", plan.Spec.Name)
	}

	// No --name → the original name is untouched.
	b := &Agent{}
	b.SetNameOverride("  ") // whitespace-only is treated as unset
	plan2 := DeployPlan{Spec: analyzer.ProjectSpec{Name: "myapp"}}
	b.applyNameOverride(&plan2)
	if plan2.Spec.Name != "myapp" {
		t.Errorf("name = %q, want myapp (unchanged)", plan2.Spec.Name)
	}
}
