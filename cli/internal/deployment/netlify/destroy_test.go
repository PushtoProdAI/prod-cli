package netlify

import (
	"context"
	"testing"

	"github.com/go-errors/errors"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
)

// destroyMockClient is a minimal NetlifyClient that records DeleteSite calls and
// serves ListSites results, so the destroy path can be exercised without a live
// Netlify account. Embedding the interface means any unimplemented method panics
// if the destroy path ever calls it.
type destroyMockClient struct {
	NetlifyClient

	deletedID   string
	deleteCalls int
	deleteErr   error
	sites       []NetlifySite
	listCalled  bool
}

func (m *destroyMockClient) DeleteSite(siteID string) error {
	m.deleteCalls++
	m.deletedID = siteID
	return m.deleteErr
}

func (m *destroyMockClient) ListSites() ([]NetlifySite, error) {
	m.listCalled = true
	return m.sites, nil
}

// A Netlify NetlifyQueuedDeployment must satisfy deployment.Destroyer so the destroy
// dispatch (agent/deployment.go) recognizes Netlify as tearable-down instead of
// reporting "Teardown isn't supported for Netlify yet".
func TestNetlifyQueuedDeploymentImplementsDestroyer(t *testing.T) {
	var _ deployment.Destroyer = (*NetlifyQueuedDeployment)(nil)
}

func TestDestroyUsesExistingProjectID(t *testing.T) {
	mock := &destroyMockClient{}
	nqd := &NetlifyQueuedDeployment{
		client: mock,
		spec: &deployment.DeploymentSpec{
			Name:              "agent",
			ExistingProjectID: "site-123",
		},
	}

	if err := nqd.Destroy(context.Background()); err != nil {
		t.Fatalf("Destroy() error = %v", err)
	}
	if mock.deleteCalls != 1 {
		t.Fatalf("DeleteSite called %d times, want 1", mock.deleteCalls)
	}
	if mock.deletedID != "site-123" {
		t.Errorf("deleted site id = %q, want %q", mock.deletedID, "site-123")
	}
	if mock.listCalled {
		t.Error("ListSites should not be called when the id is known")
	}
}

func TestDestroyResolvesSiteByName(t *testing.T) {
	mock := &destroyMockClient{
		sites: []NetlifySite{
			{ID: "other-site", Name: "not-agent"},
			{ID: "site-from-name", Name: "agent"},
		},
	}
	nqd := &NetlifyQueuedDeployment{
		client: mock,
		spec: &deployment.DeploymentSpec{
			Name: "agent", // no ExistingProjectID → fall back to name lookup
		},
	}

	if err := nqd.Destroy(context.Background()); err != nil {
		t.Fatalf("Destroy() error = %v", err)
	}
	if !mock.listCalled {
		t.Error("ListSites should be called when the id is unknown")
	}
	if mock.deletedID != "site-from-name" {
		t.Errorf("deleted site id = %q, want %q", mock.deletedID, "site-from-name")
	}
}

func TestDestroyIsIdempotentOnNotFound(t *testing.T) {
	mock := &destroyMockClient{
		// Mirror the CLI-wrapped error DeleteSite would return for a gone site.
		deleteErr: errors.Errorf("failed to delete site: exit status 1\nOutput: Site not found"),
	}
	nqd := &NetlifyQueuedDeployment{
		client: mock,
		spec: &deployment.DeploymentSpec{
			Name:              "agent",
			ExistingProjectID: "site-123",
		},
	}

	if err := nqd.Destroy(context.Background()); err != nil {
		t.Fatalf("Destroy() should be idempotent on a not-found site, got error = %v", err)
	}
	if mock.deleteCalls != 1 {
		t.Errorf("DeleteSite called %d times, want 1", mock.deleteCalls)
	}
}

func TestDestroyErrorsWhenNoSiteFound(t *testing.T) {
	mock := &destroyMockClient{sites: []NetlifySite{}}
	nqd := &NetlifyQueuedDeployment{
		client: mock,
		spec:   &deployment.DeploymentSpec{Name: "agent"},
	}

	if err := nqd.Destroy(context.Background()); err == nil {
		t.Fatal("Destroy() expected an error when no site is found, got nil")
	}
	if mock.deleteCalls != 0 {
		t.Errorf("DeleteSite should not be called when no site resolves; got %d calls", mock.deleteCalls)
	}
}
