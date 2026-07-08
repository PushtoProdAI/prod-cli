package flyio

import (
	"context"
	"strings"
	"testing"

	"github.com/go-errors/errors"
)

// collisionClient simulates a global Fly.io name collision: the app isn't in your org (GetApp
// fails) and the first CreateApp is rejected as "already been taken"; a later suffixed retry
// succeeds. It embeds MockFlyioClient for the rest of the interface.
type collisionClient struct {
	MockFlyioClient
	createNames []string
}

func (m *collisionClient) GetApp(ctx context.Context, appID string) (*FlyioApp, error) {
	return nil, errors.New("not found in org")
}

func (m *collisionClient) CreateApp(ctx context.Context, req CreateAppRequest) (*FlyioApp, error) {
	m.createNames = append(m.createNames, req.Name)
	if len(m.createNames) == 1 {
		return nil, errors.New("Name has already been taken")
	}
	return &FlyioApp{ID: "id", Name: req.Name}, nil
}

func TestCreateApp_ExplicitNameFailsLoudNoSuffix(t *testing.T) {
	client := &collisionClient{}
	step := &CreateFlyioAppStep{appName: "myapp-pr-7", region: "iad", explicitName: true}

	_, err := step.Execute(context.Background(), client, map[string]any{})
	if err == nil {
		t.Fatal("an explicit --name collision must fail loudly, got nil error")
	}
	if !strings.Contains(err.Error(), "won't silently rename") {
		t.Errorf("error should explain the no-rename policy, got: %v", err)
	}
	if len(client.createNames) != 1 {
		t.Errorf("must NOT retry with a suffix on an explicit name; CreateApp calls: %v", client.createNames)
	}
}

func TestCreateApp_InferredNameSuffixesOnCollision(t *testing.T) {
	client := &collisionClient{}
	step := &CreateFlyioAppStep{appName: "myapp", region: "iad", explicitName: false}

	_, err := step.Execute(context.Background(), client, map[string]any{})
	if err != nil {
		t.Fatalf("an inferred name should keep the suffix-retry convenience, got: %v", err)
	}
	if len(client.createNames) != 2 || client.createNames[1] == "myapp" {
		t.Errorf("expected a suffixed retry, CreateApp calls: %v", client.createNames)
	}
}
