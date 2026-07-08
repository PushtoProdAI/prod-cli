package mcpserver

import (
	"context"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pushtoprodai/prod-cli/internal/deploytarget"
	"github.com/pushtoprodai/prod-cli/internal/history"
)

// appInput identifies a previously deployed app by name.
type appInput struct {
	App string `json:"app" jsonschema:"the app / resource name, as shown by list_deploys"`
}

// latestDeploy returns the most-recent history record for an app name from local
// history, preferring the latest successful deploy.
func latestDeploy(app string) (history.Record, bool, error) {
	store, err := history.NewStore()
	if err != nil {
		return history.Record{}, false, err
	}
	records, err := store.List(0) // all, most-recent-first
	if err != nil {
		return history.Record{}, false, err
	}
	r, ok := history.LatestForApp(records, app)
	return r, ok, nil
}

// probeLive does a short GET and classifies liveness the same way the deploy path does:
// only a connection failure, timeout, or 5xx is "not-live". Returns "live"|"not-live".
func probeLive(ctx context.Context, url string) string {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "unknown"
	}
	resp, err := client.Do(req)
	if err != nil {
		return "not-live"
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return "not-live"
	}
	return "live"
}

// --- status ------------------------------------------------------------------

type statusOutput struct {
	Found       bool   `json:"found"`
	Platform    string `json:"platform,omitempty"`
	Shape       string `json:"shape,omitempty"`  // web | mcp-server | worker | cron (empty for legacy records)
	Status      string `json:"status,omitempty"` // last recorded status: success | failed | started
	LiveURL     string `json:"liveUrl,omitempty"`
	Live        string `json:"live,omitempty"` // "live" | "not-live" | "unknown"
	CanRollback bool   `json:"canRollback"`
	Note        string `json:"note,omitempty"`
}

func addStatus(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "status",
		Description: "Report the status of a previously deployed app from local history: platform, deploy shape, last recorded status, live URL, whether it can be rolled back, and — when the URL is reachable — whether it's currently responding. A worker/cron has no URL to probe; a successful one reports live. Read-only; it does not deploy or spin up the deploy pipeline.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in appInput) (*mcp.CallToolResult, statusOutput, error) {
		r, found, err := latestDeploy(in.App)
		if err != nil {
			return nil, statusOutput{}, err
		}
		if !found {
			return nil, statusOutput{Note: "no deploy found for " + in.App + " — call list_deploys to see known apps"}, nil
		}
		t := deploytarget.Resolve(r)
		out := statusOutput{
			Found: true, Platform: t.Platform, Shape: t.Shape, Status: r.Status, LiveURL: t.LiveURL,
			CanRollback: t.CanRollback, Note: t.Note, Live: "unknown",
		}
		switch {
		case t.LiveURL != "":
			out.Live = probeLive(ctx, t.LiveURL)
		case t.IsWorker() && r.Status == "success":
			// A worker/cron has no URL to probe. Don't report "not-live" just because the URL
			// is empty — a successfully deployed background process is running.
			out.Live = "live"
		}
		return nil, out, nil
	})
}

// --- deep_link ---------------------------------------------------------------

type deepLinkOutput struct {
	Found      bool   `json:"found"`
	LiveURL    string `json:"liveUrl,omitempty"`
	ConsoleURL string `json:"consoleUrl,omitempty"` // the platform's dashboard for this service
	Note       string `json:"note,omitempty"`
}

func addDeepLink(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "deep_link",
		Description: "Return the live URL and the platform-console (dashboard) URL for a deployed app, so you can hand the human clickable links. Read-only; it returns links and never opens a browser.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in appInput) (*mcp.CallToolResult, deepLinkOutput, error) {
		r, found, err := latestDeploy(in.App)
		if err != nil {
			return nil, deepLinkOutput{}, err
		}
		if !found {
			return nil, deepLinkOutput{Note: "no deploy found for " + in.App}, nil
		}
		t := deploytarget.Resolve(r)
		return nil, deepLinkOutput{Found: true, LiveURL: t.LiveURL, ConsoleURL: t.ConsoleURL, Note: t.Note}, nil
	})
}

// --- logs --------------------------------------------------------------------

type logsOutput struct {
	Found      bool   `json:"found"`
	LogsCmd    string `json:"logsCmd,omitempty"`    // the platform CLI command to view logs
	ConsoleURL string `json:"consoleUrl,omitempty"` // where to read logs in the dashboard
	Note       string `json:"note,omitempty"`
}

func addLogs(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "logs",
		Description: "Return the runnable platform-CLI command (e.g. `fly logs -a app`, `gcloud run services logs read …`) and the console URL to view a deployed app's logs. It returns the COMMAND, not raw log bytes — logs can contain secrets and a stdio tool can't stream a live tail. Run the command yourself or show it to the human.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in appInput) (*mcp.CallToolResult, logsOutput, error) {
		r, found, err := latestDeploy(in.App)
		if err != nil {
			return nil, logsOutput{}, err
		}
		if !found {
			return nil, logsOutput{Note: "no deploy found for " + in.App}, nil
		}
		t := deploytarget.Resolve(r)
		note := t.Note
		if t.LogsCmd == "" && note == "" {
			note = "no logs command known for " + t.Platform
		}
		return nil, logsOutput{Found: true, LogsCmd: t.LogsCmd, ConsoleURL: t.ConsoleURL, Note: note}, nil
	})
}
