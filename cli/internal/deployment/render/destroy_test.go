package render

import (
	"context"
	"testing"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
)

// destroyMockClient is a minimal RenderClient that records DeleteService calls and
// serves ListServices results, so the destroy path can be exercised without a live
// Render account. Only the methods the Destroy path touches are implemented.
type destroyMockClient struct {
	RenderClient // embed the interface: unimplemented methods panic if ever called

	deletedID    string
	deleteCalls  int
	deleteErr    error
	listByName   map[string][]RenderService
	listCalledBy string
}

func (m *destroyMockClient) DeleteService(_ context.Context, serviceID string) error {
	m.deleteCalls++
	m.deletedID = serviceID
	return m.deleteErr
}

func (m *destroyMockClient) ListServices(_ context.Context, name string) ([]RenderService, error) {
	m.listCalledBy = name
	return m.listByName[name], nil
}

// A Render QueuedDeployment must satisfy deployment.Destroyer so the destroy
// dispatch (agent/deployment.go) recognizes Render as tearable-down instead of
// reporting "Teardown isn't supported for Render yet".
func TestQueuedDeploymentImplementsDestroyer(t *testing.T) {
	var _ deployment.Destroyer = (*QueuedDeployment)(nil)
}

func TestDestroyUsesExistingProjectID(t *testing.T) {
	mock := &destroyMockClient{}
	qd := &QueuedDeployment{
		client: mock,
		spec: &deployment.DeploymentSpec{
			Name:              "agent",
			ExistingProjectID: "srv-123",
		},
	}

	if err := qd.Destroy(context.Background()); err != nil {
		t.Fatalf("Destroy() error = %v", err)
	}
	if mock.deleteCalls != 1 {
		t.Fatalf("DeleteService called %d times, want 1", mock.deleteCalls)
	}
	if mock.deletedID != "srv-123" {
		t.Errorf("deleted service id = %q, want %q", mock.deletedID, "srv-123")
	}
	if mock.listCalledBy != "" {
		t.Errorf("ListServices should not be called when the id is known; got lookup for %q", mock.listCalledBy)
	}
}

func TestDestroyResolvesServiceByName(t *testing.T) {
	mock := &destroyMockClient{
		listByName: map[string][]RenderService{
			"agent-web": {{ID: "srv-from-name", Name: "agent-web", Type: "web_service"}},
		},
	}
	qd := &QueuedDeployment{
		client: mock,
		spec: &deployment.DeploymentSpec{
			Name: "agent", // no ExistingProjectID → fall back to name lookup
		},
	}

	if err := qd.Destroy(context.Background()); err != nil {
		t.Fatalf("Destroy() error = %v", err)
	}
	if mock.listCalledBy != "agent-web" {
		t.Errorf("ListServices called with %q, want %q", mock.listCalledBy, "agent-web")
	}
	if mock.deletedID != "srv-from-name" {
		t.Errorf("deleted service id = %q, want %q", mock.deletedID, "srv-from-name")
	}
}

func TestDestroyErrorsWhenNoServiceFound(t *testing.T) {
	mock := &destroyMockClient{listByName: map[string][]RenderService{}}
	qd := &QueuedDeployment{
		client: mock,
		spec:   &deployment.DeploymentSpec{Name: "agent"},
	}

	if err := qd.Destroy(context.Background()); err == nil {
		t.Fatal("Destroy() expected an error when no service is found, got nil")
	}
	if mock.deleteCalls != 0 {
		t.Errorf("DeleteService should not be called when no service resolves; got %d calls", mock.deleteCalls)
	}
}
