package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/pushtoprodai/prod-cli/internal/analyzer"
	"github.com/pushtoprodai/prod-cli/internal/config"
	"github.com/pushtoprodai/prod-cli/internal/history"
	"github.com/pushtoprodai/prod-cli/internal/output"
)

// Only AWS remains hard-unsupported; Render is now backend-free (gated on a
// registry, checked separately in refuseUnsupportedPlatform).
func TestUnsupportedLocalPlatform(t *testing.T) {
	if _, unsupported := unsupportedLocalPlatform(AWS); !unsupported {
		t.Error("AWS should be refused in local mode")
	}
	for _, p := range []Platform{Render, FlyIO, Vercel, Netlify, Heroku} {
		if msg, unsupported := unsupportedLocalPlatform(p); unsupported {
			t.Errorf("%v should not be hard-unsupported, got refusal: %q", p, msg)
		}
	}
}

// The gate must only fire in local mode; in managed mode the backend powers AWS/Render.
func TestRefuseUnsupportedPlatformIsModeAware(t *testing.T) {
	a := &Agent{}
	var sink discard

	t.Run("local mode refuses AWS", func(t *testing.T) {
		setLocalMode(t)
		if !a.refuseUnsupportedPlatform(&sink, AWS) {
			t.Error("AWS should be refused in local mode")
		}
	})
	t.Run("managed mode allows AWS", func(t *testing.T) {
		t.Setenv("PROD_BACKEND_URL", "https://backend.example.com")
		t.Setenv("SUPABASE_ANON_KEY", "anon")
		if a.refuseUnsupportedPlatform(&sink, AWS) {
			t.Error("AWS should be allowed in managed mode (backend configured)")
		}
	})
	t.Run("local mode allows Fly", func(t *testing.T) {
		setLocalMode(t)
		if a.refuseUnsupportedPlatform(&sink, FlyIO) {
			t.Error("Fly should always be allowed")
		}
	})
	t.Run("deploy refuses Render without a registry", func(t *testing.T) {
		setLocalMode(t)
		t.Setenv("PROD_REGISTRY_USERNAME", "")
		t.Setenv("PROD_REGISTRY_TOKEN", "")
		if !a.refuseDeployPlatform(&sink, Render) {
			t.Error("Render deploy without a configured registry should be refused")
		}
	})
	t.Run("deploy allows Render with a registry", func(t *testing.T) {
		setLocalMode(t)
		t.Setenv("PROD_REGISTRY_USERNAME", "alice")
		t.Setenv("PROD_REGISTRY_TOKEN", "tok")
		if a.refuseDeployPlatform(&sink, Render) {
			t.Error("Render deploy with a configured registry should be allowed")
		}
	})
	t.Run("rollback allows Render without a registry", func(t *testing.T) {
		setLocalMode(t)
		t.Setenv("PROD_REGISTRY_USERNAME", "")
		t.Setenv("PROD_REGISTRY_TOKEN", "")
		// executeRollback uses refuseUnsupportedPlatform, which must not require a
		// registry — a Render rollback hits Render's API directly.
		if a.refuseUnsupportedPlatform(&sink, Render) {
			t.Error("Render rollback should be allowed without a registry")
		}
	})
}

// In local mode, the deploy write path records to the local history store and
// never touches the backend/session.
func TestLogDeploymentRoutingLocal(t *testing.T) {
	setLocalMode(t)

	store := history.Open(filepath.Join(t.TempDir(), "history.json"))
	acts := &Activities{history: store, uiWriter: output.NewNoOpWriter()}

	spec := analyzer.ProjectSpec{Name: "my-app", Language: "node"}
	id, err := acts.logDeploymentStart(context.Background(), "fly", spec, "/src", Deploy)
	if err != nil {
		t.Fatalf("logDeploymentStart: %v", err)
	}
	if id == "" {
		t.Fatal("expected a non-empty local operation id")
	}

	recs, _ := store.List(0)
	if len(recs) != 1 || recs[0].ID != id || recs[0].Status != "started" || recs[0].Platform != "fly" {
		t.Fatalf("unexpected started record: %+v", recs)
	}

	err = acts.updateDeploymentStatus(context.Background(), id, "success",
		map[string]any{"url": "https://my-app.fly.dev", "platform": "fly"})
	if err != nil {
		t.Fatalf("updateDeploymentStatus: %v", err)
	}

	recs, _ = store.List(0)
	if recs[0].Status != "success" || recs[0].CompletedAt == nil {
		t.Fatalf("update not applied: %+v", recs[0])
	}
	if recs[0].Metadata["url"] != "https://my-app.fly.dev" {
		t.Errorf("metadata not merged: %+v", recs[0].Metadata)
	}
}

// setLocalMode forces config into local mode for a test.
func setLocalMode(t *testing.T) {
	t.Helper()
	t.Setenv("PROD_BACKEND_URL", "")
	t.Setenv("SUPABASE_URL", "")
	t.Setenv("SUPABASE_ANON_KEY", "")
	config.SupabaseURL = ""
	config.SupabaseAnonKey = ""
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
