package vercel

import (
	"context"
	"testing"

	"github.com/go-errors/errors"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
)

// destroyMockClient is a minimal VercelClient that records DeleteProjectByName
// calls, so the destroy path can be exercised without a live Vercel account.
// Embedding the interface means any unimplemented method panics if the destroy
// path ever calls it.
type destroyMockClient struct {
	VercelClient

	deletedName string
	deleteCalls int
	deleteErr   error
}

func (m *destroyMockClient) DeleteProjectByName(name string) error {
	m.deleteCalls++
	m.deletedName = name
	return m.deleteErr
}

// A Vercel VercelQueuedDeployment must satisfy deployment.Destroyer so the destroy
// dispatch (agent/deployment.go) recognizes Vercel as tearable-down instead of
// reporting "Teardown isn't supported for Vercel yet".
func TestVercelQueuedDeploymentImplementsDestroyer(t *testing.T) {
	var _ deployment.Destroyer = (*VercelQueuedDeployment)(nil)
}

// Destroy must delete by project NAME, never by the `prj_…` id (the semantic trap):
// `vercel project rm` takes a name, and `vercel remove <prj_id>` targets deployments.
func TestDestroyUsesProjectName(t *testing.T) {
	mock := &destroyMockClient{}
	vqd := &VercelQueuedDeployment{
		client: mock,
		spec: &deployment.DeploymentSpec{
			Name:              "agent",
			ExistingProjectID: "prj_abc123", // present, but must NOT be used for teardown
		},
	}

	if err := vqd.Destroy(context.Background()); err != nil {
		t.Fatalf("Destroy() error = %v", err)
	}
	if mock.deleteCalls != 1 {
		t.Fatalf("DeleteProjectByName called %d times, want 1", mock.deleteCalls)
	}
	if mock.deletedName != "agent" {
		t.Errorf("deleted project = %q, want the project name %q (not the prj_ id)", mock.deletedName, "agent")
	}
}

func TestDestroyIsIdempotentOnNotFound(t *testing.T) {
	mock := &destroyMockClient{
		// Mirror the CLI error `vercel project rm` returns for a gone project.
		deleteErr: errors.Errorf("failed to delete project %q: exit status 1\nOutput: Error: No such project exists", "agent"),
	}
	vqd := &VercelQueuedDeployment{
		client: mock,
		spec:   &deployment.DeploymentSpec{Name: "agent"},
	}

	if err := vqd.Destroy(context.Background()); err != nil {
		t.Fatalf("Destroy() should be idempotent on a not-found project, got error = %v", err)
	}
	if mock.deleteCalls != 1 {
		t.Errorf("DeleteProjectByName called %d times, want 1", mock.deleteCalls)
	}
}

func TestDestroyErrorsWhenNoName(t *testing.T) {
	mock := &destroyMockClient{}
	vqd := &VercelQueuedDeployment{
		client: mock,
		spec:   &deployment.DeploymentSpec{}, // no Name
	}

	if err := vqd.Destroy(context.Background()); err == nil {
		t.Fatal("Destroy() expected an error when no project name is available, got nil")
	}
	if mock.deleteCalls != 0 {
		t.Errorf("DeleteProjectByName should not be called without a name; got %d calls", mock.deleteCalls)
	}
}
