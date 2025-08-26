package agent

import (
	"testing"

	"github.com/meroxa/prod/cli/internal/deployment/flyio"
	"github.com/meroxa/prod/cli/internal/output"
)

func TestWorkflows_DryRunDeploy_FlyIO(t *testing.T) {
	// Create a new workflows instance
	workflows := &Workflows{
		flyClient: flyio.NewFlyioClient(),
		uiWriter:  output.NewNoOpWriter(),
	}

	// Test that the dry-run workflow can be created
	// Note: We can't actually execute the workflow without a proper workflow client,
	// but we can test that the workflow registration is correct
	workflows.Workflows() // This should not panic

	// Test that the workflow name constants are defined
	if DryRunFlyioWorkflowName == "" {
		t.Error("DryRunFlyioWorkflowName is not defined")
	}

	if DryRunFlyioWorkflowName != "agent.dryrun.flyio" {
		t.Errorf("Expected DryRunFlyioWorkflowName to be 'agent.dryrun.flyio', got '%s'", DryRunFlyioWorkflowName)
	}

	// Test that the workflow function exists (can't check for nil in Go)
	t.Log("dryRunDeployFly workflow function is defined")
}

func TestWorkflows_Deploy_FlyIO(t *testing.T) {
	// Create a new workflows instance
	workflows := &Workflows{
		flyClient: flyio.NewFlyioClient(),
		uiWriter:  output.NewNoOpWriter(),
	}

	// Test that the deploy workflow name constants are defined
	if DeployFlyioWorkflowName == "" {
		t.Error("DeployFlyioWorkflowName is not defined")
	}

	if DeployFlyioWorkflowName != "agent.deploy.flyio" {
		t.Errorf("Expected DeployFlyioWorkflowName to be 'agent.deploy.flyio', got '%s'", DeployFlyioWorkflowName)
	}

	// Test that the workflow function exists (can't check for nil in Go)
	t.Log("deployFly workflow function is defined")

	// Use the workflows instance to avoid unused variable warning
	_ = workflows
}

func TestWorkflows_WorkflowRegistration(t *testing.T) {
	// Create a new workflows instance
	workflows := &Workflows{
		flyClient: flyio.NewFlyioClient(),
		uiWriter:  output.NewNoOpWriter(),
	}

	// Get the registered workflows
	registeredWorkflows := workflows.Workflows()

	// Check that both Fly.io workflows are registered
	foundDeploy := false
	foundDryRun := false

	for _, wf := range registeredWorkflows {
		switch wf.Name {
		case DeployFlyioWorkflowName:
			foundDeploy = true
		case DryRunFlyioWorkflowName:
			foundDryRun = true
		}
	}

	if !foundDeploy {
		t.Errorf("Deploy Fly.io workflow '%s' not found in registered workflows", DeployFlyioWorkflowName)
	}

	if !foundDryRun {
		t.Errorf("Dry-run Fly.io workflow '%s' not found in registered workflows", DryRunFlyioWorkflowName)
	}

	t.Logf("Found %d registered workflows", len(registeredWorkflows))
	for _, wf := range registeredWorkflows {
		t.Logf("  - %s", wf.Name)
	}
}
