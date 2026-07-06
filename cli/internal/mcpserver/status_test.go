package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pushtoprodai/prod-cli/internal/history"
)

// Seed a Cloud Run deploy in a temp HOME, then exercise status/deep_link/logs over the
// real in-memory MCP transport — they must resolve console URL + logs command from the
// persisted identifiers, and status must probe the live URL.
func TestStatusDeepLinkLogsTools(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer live.Close()

	store, err := history.NewStore()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := store.Add(history.Record{
		ID: "op1", OperationType: "deploy", ResourceName: "widget", Platform: "googlecloudrun",
		Status: "success", StartedAt: now, CompletedAt: &now,
		Metadata: map[string]any{
			"url": live.URL, "resourceId": "projects/p/locations/us-central1/services/widget",
			"project": "p", "region": "us-central1",
		},
	}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	clientT, serverT := mcp.NewInMemoryTransports()
	go func() { _ = New("test").Run(ctx, serverT) }()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "0"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()

	call := func(tool string) map[string]any {
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: map[string]any{"app": "widget"}})
		if err != nil || res.IsError {
			t.Fatalf("%s: err=%v isError=%v content=%+v", tool, err, res.IsError, res.Content)
		}
		return res.StructuredContent.(map[string]any)
	}

	dl := call("deep_link")
	if !strings.Contains(dl["consoleUrl"].(string), "console.cloud.google.com/run/detail/us-central1/widget") {
		t.Errorf("deep_link consoleUrl = %v", dl["consoleUrl"])
	}
	if dl["liveUrl"] != live.URL {
		t.Errorf("deep_link liveUrl = %v, want %v", dl["liveUrl"], live.URL)
	}

	lg := call("logs")
	if lg["logsCmd"] != "gcloud run services logs read widget --region us-central1 --project p" {
		t.Errorf("logs cmd = %v", lg["logsCmd"])
	}

	st := call("status")
	if st["found"] != true || st["platform"] != "googlecloudrun" {
		t.Errorf("status = %+v", st)
	}
	if st["live"] != "live" {
		t.Errorf("status.live = %v, want live (the seeded URL is up)", st["live"])
	}

	// Unknown app degrades cleanly (found=false, a helpful note), not an error.
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "status", Arguments: map[string]any{"app": "nope"}})
	if err != nil || res.IsError {
		t.Fatalf("status(nope): unexpected error %v / %v", err, res.IsError)
	}
	if res.StructuredContent.(map[string]any)["found"] == true {
		t.Error("status for an unknown app should report found=false")
	}
}
