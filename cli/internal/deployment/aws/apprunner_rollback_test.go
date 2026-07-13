package aws

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
	"github.com/pushtoprodai/prod-cli/internal/history"
)

// newHistory builds a temp-backed history store and appends the given records oldest-first
// (Store.List returns them newest-first, which is what GetPreviousDeployment relies on).
func newHistory(t *testing.T, recs ...history.Record) *history.Store {
	t.Helper()
	store := history.Open(filepath.Join(t.TempDir(), "history.json"))
	for i, r := range recs {
		if r.ID == "" {
			r.ID = string(rune('a' + i))
		}
		if r.StartedAt.IsZero() {
			r.StartedAt = time.Unix(int64(1000+i), 0)
		}
		if err := store.Add(r); err != nil {
			t.Fatalf("seed history: %v", err)
		}
	}
	return store
}

func awsRec(status, img, region, account string) history.Record {
	return history.Record{
		OperationType: "deploy",
		ResourceName:  "myapp",
		Platform:      "aws",
		Status:        status,
		Metadata:      map[string]any{"imageRef": img, "region": region, "account": account},
	}
}

func prevImage(t *testing.T, store *history.Store) string {
	t.Helper()
	d := NewAppRunnerDeployment(&deployment.DeploymentSpec{Name: "myapp"}, nil, store, nil)
	info, err := d.GetPreviousDeployment(context.Background())
	if err != nil {
		t.Fatalf("GetPreviousDeployment: %v", err)
	}
	if info == nil {
		return ""
	}
	return info.ID
}

// Manual rollback: the most-recent record is a completed success (the deploy the user wants
// to undo), so we skip it and return the image before it.
func TestGetPreviousDeployment_ManualRollbackSkipsCurrent(t *testing.T) {
	store := newHistory(
		t,
		awsRec("success", "img-1", "us-east-1", "123"),
		awsRec("success", "img-2", "us-east-1", "123"), // most recent = current
	)
	if got := prevImage(t, store); got != "img-1" {
		t.Errorf("manual rollback = %q, want img-1 (the image before the current one)", got)
	}
}

// Auto-rollback after a failed health check: the current deploy isn't a success record yet
// (it's 'started'/'failed'), so the most-recent success IS the previous good image — don't skip.
func TestGetPreviousDeployment_AutoRollbackReturnsLatestGood(t *testing.T) {
	store := newHistory(
		t,
		awsRec("success", "img-1", "us-east-1", "123"),
		awsRec("started", "", "us-east-1", "123"), // current deploy in flight
	)
	if got := prevImage(t, store); got != "img-1" {
		t.Errorf("auto-rollback = %q, want img-1 (the latest good image)", got)
	}
}

// Never roll back into a different AWS account/region that reused the app name: with only a
// different-account image left after skipping the current deploy, there's nothing to return.
func TestGetPreviousDeployment_WontCrossAccounts(t *testing.T) {
	store := newHistory(
		t,
		awsRec("success", "img-other", "us-west-2", "999"),
		awsRec("success", "img-cur", "us-east-1", "123"), // most recent = current
	)
	if got := prevImage(t, store); got != "" {
		t.Errorf("cross-account rollback = %q, want empty (won't roll back to another account)", got)
	}
}

func TestGetPreviousDeployment_NoHistory(t *testing.T) {
	if got := prevImage(t, newHistory(t)); got != "" {
		t.Errorf("empty history = %q, want empty", got)
	}
	// nil store must also degrade to nil, not panic.
	d := NewAppRunnerDeployment(&deployment.DeploymentSpec{Name: "myapp"}, nil, nil, nil)
	info, err := d.GetPreviousDeployment(context.Background())
	if err != nil || info != nil {
		t.Errorf("nil-store GetPreviousDeployment = (%v, %v), want (nil, nil)", info, err)
	}
}

// Only this app's AWS records are considered — a same-named deploy on another platform, or a
// different app, must not become a rollback target.
func TestGetPreviousDeployment_FiltersByAppAndPlatform(t *testing.T) {
	store := newHistory(
		t,
		history.Record{OperationType: "deploy", ResourceName: "myapp", Platform: "render", Status: "success", Metadata: map[string]any{"imageRef": "render-img"}},
		awsRec("success", "img-1", "us-east-1", "123"),
		awsRec("started", "", "us-east-1", "123"),
	)
	if got := prevImage(t, store); got != "img-1" {
		t.Errorf("filtered rollback = %q, want img-1 (ignore the render record)", got)
	}
}
