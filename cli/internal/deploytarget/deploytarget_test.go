package deploytarget

import (
	"strings"
	"testing"

	"github.com/pushtoprodai/prod-cli/internal/history"
)

func rec(platform, name string, md map[string]any) history.Record {
	return history.Record{Platform: platform, ResourceName: name, Metadata: md}
}

func TestResolvePerPlatform(t *testing.T) {
	cases := []struct {
		name        string
		r           history.Record
		wantConsole string // substring
		wantLogs    string // substring
		canRollback bool
	}{
		{
			"fly", rec("flyio", "myapp", map[string]any{"url": "https://myapp.fly.dev", "app_id": "myapp"}),
			"fly.io/apps/myapp", "fly logs -a myapp", true,
		},
		{
			"render", rec("render", "api", map[string]any{"url": "https://x", "resourceId": "srv-abc"}),
			"dashboard.render.com/web/srv-abc", "render logs --resources srv-abc", true,
		},
		{
			"heroku", rec("heroku", "myapp", map[string]any{"url": "https://x"}),
			"dashboard.heroku.com/apps/myapp", "heroku logs --tail -a myapp", true,
		},
		{
			"cloud run", rec("googlecloudrun", "widget", map[string]any{"url": "https://x", "resourceId": "projects/p/locations/us-central1/services/widget", "project": "p", "region": "us-central1"}),
			"console.cloud.google.com/run/detail/us-central1/widget", "gcloud run services logs read widget --region us-central1 --project p", true,
		},
		{
			"app runner (no rollback)", rec("aws", "svc", map[string]any{"url": "https://x", "resourceId": "arn:...", "region": "us-east-1", "account": "123"}),
			"us-east-1.console.aws.amazon.com/apprunner", "aws logs tail /aws/apprunner/svc --region us-east-1 --follow", false,
		},
		{
			"azure", rec("azure", "app", map[string]any{"url": "https://x", "resourceGroup": "prod-apps", "subscription": "sub-1", "location": "eastus"}),
			"portal.azure.com/#resource/subscriptions/sub-1/resourceGroups/prod-apps", "az containerapp logs show -n app -g prod-apps --follow", true,
		},
		{
			"modal (no rollback)", rec("modal", "agent", map[string]any{"url": "https://x.modal.run"}),
			"modal.com/apps", "modal app logs agent", false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Resolve(c.r)
			if !strings.Contains(got.ConsoleURL, c.wantConsole) {
				t.Errorf("ConsoleURL = %q, want to contain %q", got.ConsoleURL, c.wantConsole)
			}
			if got.LogsCmd != c.wantLogs {
				t.Errorf("LogsCmd = %q, want %q", got.LogsCmd, c.wantLogs)
			}
			if got.CanRollback != c.canRollback {
				t.Errorf("CanRollback = %v, want %v", got.CanRollback, c.canRollback)
			}
			if got.Note != "" {
				t.Errorf("unexpected degrade note for a complete record: %q", got.Note)
			}
		})
	}
}

// Mixed-case legacy platform strings must still resolve (canonicalized on read).
func TestResolveNormalizesCasing(t *testing.T) {
	got := Resolve(rec("GoogleCloudRun", "widget", map[string]any{"project": "p", "region": "r", "url": "https://x"}))
	if got.Platform != "googlecloudrun" || !strings.Contains(got.ConsoleURL, "cloud.google.com") {
		t.Errorf("mixed-case platform should resolve: %+v", got)
	}
}

// A container-cloud record from before identifiers were persisted must degrade cleanly,
// not produce a broken link.
func TestResolveDegradesWhenIdentifierMissing(t *testing.T) {
	got := Resolve(rec("azure", "app", map[string]any{"url": "https://x"})) // no resourceGroup
	if got.ConsoleURL != "" || got.LogsCmd != "" {
		t.Errorf("expected no links without identifiers, got console=%q logs=%q", got.ConsoleURL, got.LogsCmd)
	}
	if got.Note == "" {
		t.Error("expected a degrade note when identifiers are missing")
	}
	if got.LiveURL != "https://x" {
		t.Errorf("live URL should still resolve: %q", got.LiveURL)
	}
}

// A Fly worker/cron records no live URL by design. It must still resolve its console + logs
// from the persisted app_id (logs don't depend on a URL) and carry the shape through.
func TestResolveWorkerHasNoURLButKeepsLogs(t *testing.T) {
	got := Resolve(rec("flyio", "cronjob", map[string]any{"app_id": "cronjob", "shape": "worker"}))
	if got.LiveURL != "" {
		t.Errorf("worker should have no live URL, got %q", got.LiveURL)
	}
	if got.Shape != "worker" {
		t.Errorf("shape = %q, want worker", got.Shape)
	}
	if got.LogsCmd != "fly logs -a cronjob" {
		t.Errorf("logs cmd should resolve without a URL: %q", got.LogsCmd)
	}
	if !strings.Contains(got.ConsoleURL, "fly.io/apps/cronjob") {
		t.Errorf("console URL should resolve: %q", got.ConsoleURL)
	}
	if got.Note != "" {
		t.Errorf("a worker with console+logs should not degrade: %q", got.Note)
	}
}

// IsWorker trusts the persisted shape, and for legacy shapeless records falls back to
// "no live URL" — while HTTP shapes are never workers even before their URL resolves.
func TestIsWorker(t *testing.T) {
	cases := []struct {
		name  string
		shape string
		url   string
		want  bool
	}{
		{"explicit worker", "worker", "", true},
		{"explicit cron", "cron", "", true},
		{"web with url", "web", "https://x", false},
		{"web without url yet", "web", "", false},
		{"mcp-server", "mcp-server", "", false},
		{"legacy no-url record", "", "", true},
		{"legacy record with url", "", "https://x", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Target{Shape: c.shape, LiveURL: c.url}.IsWorker()
			if got != c.want {
				t.Errorf("IsWorker(shape=%q,url=%q) = %v, want %v", c.shape, c.url, got, c.want)
			}
		})
	}
}
