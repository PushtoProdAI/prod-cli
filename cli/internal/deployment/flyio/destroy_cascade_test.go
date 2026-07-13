package flyio

import (
	"context"
	"testing"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
)

// cascadeSpy embeds FlyioClient (all methods nil) and records the teardown calls Destroy
// makes. Only DestroyApp/DestroyPostgres/DestroyRedis are exercised, so the nil embed is safe.
type cascadeSpy struct {
	FlyioClient
	destroyedApp   string
	destroyedPG    []string
	destroyedRedis []string
}

func (s *cascadeSpy) DestroyApp(_ context.Context, appID string) error {
	s.destroyedApp = appID
	return nil
}

func (s *cascadeSpy) DestroyPostgres(_ context.Context, id string) error {
	s.destroyedPG = append(s.destroyedPG, id)
	return nil
}

func (s *cascadeSpy) DestroyRedis(_ context.Context, name string) error {
	s.destroyedRedis = append(s.destroyedRedis, name)
	return nil
}

func backingResources() []deployment.CreatedResource {
	return []deployment.CreatedResource{
		{Type: "postgres_cluster", ID: "pg-123", Name: "myapp-postgres"},
		{Type: "redis", ID: "rd-456", Name: "myapp-redis"},
		{Type: "app", ID: "myapp", Name: "myapp"}, // must NOT be deleted as a DB
	}
}

// With --delete-data, Destroy removes the app AND the recorded backing databases (Postgres by
// id, Redis by name), and nothing else.
func TestDestroyCascadesBackingDataWhenOptedIn(t *testing.T) {
	spy := &cascadeSpy{}
	spec := &deployment.DeploymentSpec{Name: "myapp", DeleteBackingData: true, BackingResources: backingResources()}
	d := NewFlyioQueuedDeployment(spy, spec, nil, nil)

	if err := d.Destroy(context.Background()); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if spy.destroyedApp == "" {
		t.Error("the app itself should always be destroyed")
	}
	if len(spy.destroyedPG) != 1 || spy.destroyedPG[0] != "pg-123" {
		t.Errorf("Postgres deletes = %v, want [pg-123] (by id)", spy.destroyedPG)
	}
	if len(spy.destroyedRedis) != 1 || spy.destroyedRedis[0] != "myapp-redis" {
		t.Errorf("Redis deletes = %v, want [myapp-redis] (by name)", spy.destroyedRedis)
	}
}

// Without the opt-in (the default), Destroy removes only the app — backing databases are kept,
// so a user never loses data by accident.
func TestDestroyKeepsBackingDataByDefault(t *testing.T) {
	spy := &cascadeSpy{}
	spec := &deployment.DeploymentSpec{Name: "myapp", DeleteBackingData: false, BackingResources: backingResources()}
	d := NewFlyioQueuedDeployment(spy, spec, nil, nil)

	if err := d.Destroy(context.Background()); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if len(spy.destroyedPG) != 0 || len(spy.destroyedRedis) != 0 {
		t.Errorf("default destroy deleted data: pg=%v redis=%v (must keep by default)", spy.destroyedPG, spy.destroyedRedis)
	}
}
