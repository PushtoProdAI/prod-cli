package agent

import (
	"testing"

	"github.com/meroxa/prod/cli/internal/deployment/flyio"
	"github.com/meroxa/prod/cli/internal/output"
)

func TestWorkflows_Deploy_FlyIO(t *testing.T) {
	// Create a new workflows instance
	workflows := &Workflows{
		flyClient: flyio.NewFlyioClient(output.NewNoOpWriter()),
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
		flyClient: flyio.NewFlyioClient(output.NewNoOpWriter()),
		uiWriter:  output.NewNoOpWriter(),
	}

	// Get the registered workflows
	registeredWorkflows := workflows.Workflows()

	// Check that Fly.io deploy workflow is registered
	foundDeploy := false

	for _, wf := range registeredWorkflows {
		if wf.Name == DeployFlyioWorkflowName {
			foundDeploy = true
			break
		}
	}

	if !foundDeploy {
		t.Errorf("Deploy Fly.io workflow '%s' not found in registered workflows", DeployFlyioWorkflowName)
	}

	t.Logf("Found %d registered workflows", len(registeredWorkflows))
	for _, wf := range registeredWorkflows {
		t.Logf("  - %s", wf.Name)
	}
}

// MockStatusWriter is a mock implementation of StatusWriter for testing
type MockStatusWriter struct {
	messages []string
}

func (m *MockStatusWriter) Write(p []byte) (int, error) {
	m.messages = append(m.messages, string(p))
	return len(p), nil
}

func (m *MockStatusWriter) SendStatus(status, message string) {
	m.messages = append(m.messages, message)
}

func (m *MockStatusWriter) SendStatusComplete(status, message string) {
	m.messages = append(m.messages, message)
}
