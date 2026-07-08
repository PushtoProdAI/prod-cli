// Package deploytarget turns a deploy-history record into the per-platform view every
// richer surface needs: where the app is live, how to open its console, how to read its
// logs, and whether it can be rolled back. It is the single place per-cloud console/logs
// knowledge lives — the MCP tools (status/deep_link/logs) and the CLI launcher
// (prod ls/open/logs) both resolve through it, so the knowledge can't drift.
//
// It reads only the identifiers persisted at deploy time (history.Record.Metadata) and
// never calls a cloud API — resolution is offline and instant. A record missing an
// identifier degrades to "not recorded" rather than a broken link.
package deploytarget

import (
	"fmt"

	"github.com/pushtoprodai/prod-cli/internal/history"
)

// Target is the resolved view of one deploy.
type Target struct {
	Platform    string `json:"platform"`             // canonical token, e.g. "googlecloudrun"
	Name        string `json:"name"`                 // the resource/app name
	Shape       string `json:"shape,omitempty"`      // persisted deploy shape: web | mcp-server | worker | cron (empty for legacy records)
	LiveURL     string `json:"liveUrl,omitempty"`    // the public URL, if the deploy has one (workers/cron have none by design)
	ConsoleURL  string `json:"consoleUrl,omitempty"` // deep-link to the platform's dashboard for this service
	LogsCmd     string `json:"logsCmd,omitempty"`    // the platform CLI command to view logs
	CanRollback bool   `json:"canRollback"`          // whether prod can roll this platform back
	Note        string `json:"note,omitempty"`       // e.g. "identifier not recorded — redeploy to enable deep-links"
}

// IsWorker reports whether this deploy is a non-HTTP shape — a worker or cron job, a process
// with no public URL by design. It trusts the persisted shape when present (deploys recorded
// after shape persistence carry it); for older records that predate the shape field it falls
// back to the only available signal — a successful worker/cron records no live URL, so an empty
// LiveURL is treated as non-HTTP. Web and mcp-server shapes are HTTP even before a URL resolves.
func (t Target) IsWorker() bool {
	switch t.Shape {
	case "worker", "cron":
		return true
	case "web", "mcp-server":
		return false
	default:
		return t.LiveURL == ""
	}
}

// Resolve builds a Target from a history record.
func Resolve(r history.Record) Target {
	get := func(k string) string {
		if r.Metadata == nil {
			return ""
		}
		s, _ := r.Metadata[k].(string)
		return s
	}
	plat := history.CanonicalPlatform(r.Platform)
	name := r.ResourceName
	t := Target{Platform: plat, Name: name, Shape: get("shape"), LiveURL: get("url")}

	region := get("region")
	project := get("project")

	switch plat {
	case "flyio":
		app := firstNonEmpty(get("app_id"), get("resourceId"), name)
		t.CanRollback = true
		t.ConsoleURL = "https://fly.io/apps/" + app
		t.LogsCmd = "fly logs -a " + app

	case "render":
		id := get("resourceId")
		t.CanRollback = true
		if id != "" {
			t.ConsoleURL = "https://dashboard.render.com/web/" + id
			t.LogsCmd = "render logs --resources " + id
		}

	case "heroku":
		app := firstNonEmpty(get("resourceId"), name)
		t.CanRollback = true
		t.ConsoleURL = "https://dashboard.heroku.com/apps/" + app
		t.LogsCmd = "heroku logs --tail -a " + app

	case "netlify":
		site := firstNonEmpty(get("resourceId"), name)
		t.CanRollback = true
		t.ConsoleURL = "https://app.netlify.com/sites/" + site
		t.LogsCmd = "netlify logs:deploy" // run from the linked project directory

	case "vercel":
		t.CanRollback = true
		// Vercel's console needs the team slug we don't record; deep-link to the account
		// dashboard and read logs by URL.
		t.ConsoleURL = "https://vercel.com/dashboard"
		if t.LiveURL != "" {
			t.LogsCmd = "vercel logs " + t.LiveURL
		}

	case "aws":
		// resourceId is the service ARN (encodes region + account).
		t.CanRollback = false // App Runner rollback isn't supported yet
		if region != "" {
			t.ConsoleURL = fmt.Sprintf("https://%s.console.aws.amazon.com/apprunner/home?region=%s#/services", region, region)
			t.LogsCmd = fmt.Sprintf("aws logs tail /aws/apprunner/%s --region %s --follow", name, region)
		}

	case "googlecloudrun":
		t.CanRollback = true
		if region != "" && project != "" {
			t.ConsoleURL = fmt.Sprintf("https://console.cloud.google.com/run/detail/%s/%s/logs?project=%s", region, name, project)
			t.LogsCmd = fmt.Sprintf("gcloud run services logs read %s --region %s --project %s", name, region, project)
		}

	case "azure":
		rg := get("resourceGroup")
		sub := get("subscription")
		t.CanRollback = true
		if rg != "" {
			t.LogsCmd = fmt.Sprintf("az containerapp logs show -n %s -g %s --follow", name, rg)
			if sub != "" {
				t.ConsoleURL = fmt.Sprintf("https://portal.azure.com/#resource/subscriptions/%s/resourceGroups/%s/providers/Microsoft.App/containerApps/%s", sub, rg, name)
			}
		}

	case "modal":
		t.CanRollback = false // Modal rollback isn't supported yet
		// Modal's console needs the workspace slug we don't record; deep-link to the app list.
		t.ConsoleURL = "https://modal.com/apps"
		t.LogsCmd = "modal app logs " + name
	}

	if t.ConsoleURL == "" && t.LogsCmd == "" {
		t.Note = "identifier not recorded — redeploy to enable console + logs links"
	}
	return t
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
