package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cschleiden/go-workflows/workflow"
	"github.com/go-errors/errors"
	"github.com/pushtoprodai/prod-cli/baml_client/types"
	"github.com/pushtoprodai/prod-cli/internal/analyzer"
	"github.com/pushtoprodai/prod-cli/internal/deployment"
	"github.com/pushtoprodai/prod-cli/internal/deployment/render"
)

func (a *Activities) getRenderWorkspace(ctx context.Context) (string, error) {
	a.uiWriter.SendStatus("retrieving", "Retrieving Render workspace details...")
	workspaces, err := a.renderClient.ListWorkspaces(ctx)
	if err != nil {
		a.uiWriter.SendStatusComplete("retrieving", "❌ Failed to retrieve workspace details")
		var httpErr *render.HTTPError
		if errors.As(err, &httpErr) {
			if httpErr.IsClientError() {
				return "", workflow.NewPermanentError(errors.Errorf("failed to list workspaces. client error (%d): %s", httpErr.StatusCode, httpErr.Message))
			}
			if httpErr.IsServerError() {
				return "", errors.Errorf("failed to list workspaces. server error (%d): %s", httpErr.StatusCode, httpErr.Message)
			}
		}
		return "", errors.Errorf("failed to list workspaces: %w", err)
	}

	if len(workspaces) == 0 {
		a.uiWriter.SendStatusComplete("retrieving", "❌ No workspaces found")
		return "", errors.Errorf("no workspaces found")
	}

	ownerID := workspaces[0].Owner.ID
	a.uiWriter.SendStatusComplete("retrieving", "✅ Workplace details retrieved")
	return ownerID, nil
}

func (a *Activities) getRenderServiceURL(ctx context.Context, serviceID string) (string, error) {
	service, err := a.renderClient.GetWebService(ctx, serviceID)
	if err != nil {
		return "", errors.Errorf("failed to get service info: %w", err)
	}
	return service.ServiceDetails.URL, nil
}

func (a *Activities) waitForRenderDeploy(ctx context.Context, serviceID, deployID string) error {
	a.uiWriter.SendStatus("deploying", "Waiting for deployment to complete...")

	deploy, err := a.renderClient.GetDeploy(ctx, serviceID, deployID)
	if err != nil {
		return errors.Errorf("failed to get deploy status: %w", err)
	}

	if deploy.Status == "build_failed" || deploy.Status == "update_failed" || deploy.Status == "deactivated" {
		return errors.Errorf("deployment failed with status: %s", deploy.Status)
	}

	if deploy.Status != "live" {
		return errors.Errorf("deployment not yet live, current status: %s", deploy.Status)
	}

	return nil
}

func (a *Activities) getFlyIOAppURL(ctx context.Context, appID string) (string, error) {
	service, err := a.flyClient.GetApp(ctx, appID)
	if err != nil {
		return "", errors.Errorf("failed to get service info: %w", err)
	}
	return service.Hostname, nil
}

// livenessClient is the one HTTP client every liveness probe uses, so the timeout and
// redirect policy are identical across all platforms and shapes (ACD.3). It follows
// exactly one redirect, then judges the destination (a 302→/login is a live app):
// CheckRedirect runs *before* each hop with `via` holding the requests already made, so
// the first redirect has len(via)==1 (allow) and the second has len(via)==2 (stop).
func livenessClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= 2 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
}

// verifyLiveness confirms a freshly-deployed service is up, according to its shape:
//   - web (or unset): probe the URL — responding (2xx / 3xx / 401 / 403 / 4xx) is live.
//   - mcp-server: HTTP alone isn't enough; require a JSON-RPC `initialize` handshake, so a
//     misdeployed web app that merely returns 200 is caught (ACD.4).
//   - worker / cron: no URL — liveness is the platform's concern (adapter-owned), not an
//     HTTP GET — so skip the probe instead of auto-failing a healthy non-HTTP deploy.
func (a *Activities) verifyLiveness(ctx context.Context, shape deployment.DeployShape, url string) error {
	switch shape {
	case deployment.ShapeWorker, deployment.ShapeCron:
		a.uiWriter.SendStatusComplete("deploying", fmt.Sprintf("✅ %s deploy — no HTTP liveness check", shape))
		return nil
	case deployment.ShapeMCPServer:
		return a.isMCPServerLive(ctx, url)
	default:
		return a.isURLLive(ctx, url)
	}
}

func (a *Activities) isURLLive(ctx context.Context, url string) error {
	// We probe the URL rather than the platform's deploy API — it avoids rate limits and
	// is what the user actually cares about. "Live" means the service is *responding*:
	// an auth wall (401/403), a redirect to a login page, or any 2xx all mean it's up and
	// serving. Only a connection failure, a timeout, or a 5xx (the app itself is broken)
	// counts as not-live. Treating every status >300 as dead — the old behavior — failed
	// deploys of any app behind auth or a redirect.
	a.uiWriter.SendStatus("deploying", "Waiting for URL to be live...")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return errors.Errorf("failed to build request to %s: %w", url, err)
	}
	resp, err := livenessClient().Do(req)
	if err != nil {
		// Connection refused / DNS / timeout — genuinely not reachable.
		return errors.Errorf("failed to reach %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 500 {
		return errors.Errorf("received server error %d from %s", resp.StatusCode, url)
	}
	a.uiWriter.SendStatusComplete("deploying", "✅ URL is live")
	return nil
}

// isMCPServerLive verifies a deployed MCP server actually speaks MCP, not just serves
// HTTP: it POSTs a JSON-RPC `initialize` and requires a response advertising serverInfo,
// capabilities, or a protocolVersion. A plain 200 with no handshake is NOT live (ACD.4) —
// that's a misdeployed web app, not a working MCP server.
func (a *Activities) isMCPServerLive(ctx context.Context, url string) error {
	a.uiWriter.SendStatus("deploying", "Waiting for the MCP server to answer initialize...")
	const initReq = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"prod","version":"0"}}}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(initReq))
	if err != nil {
		return errors.Errorf("failed to build MCP initialize request to %s: %w", url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := livenessClient().Do(req)
	if err != nil {
		return errors.Errorf("failed to reach MCP server %s: %w", url, err)
	}
	defer resp.Body.Close()

	// A 5xx means the app itself is broken → not-live.
	if resp.StatusCode >= 500 {
		return errors.Errorf("MCP server %s returned server error %d", url, resp.StatusCode)
	}
	// Only a 2xx can carry a handshake we can check. A non-2xx that still responds — an auth
	// wall (401/403) or the MCP endpoint mounted at another path (404/405) — is reachable, and
	// like isURLLive we call that live rather than failing (and possibly rolling back) a
	// healthy but auth-protected server. (Only the current streamable-HTTP transport is
	// probed; an older SSE-first transport — GET /sse then POST — lands here as "reachable".)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		a.uiWriter.SendStatusComplete("deploying", fmt.Sprintf("✅ MCP server reachable (HTTP %d)", resp.StatusCode))
		return nil
	}
	// 2xx: require a real MCP initialize handshake, so a misdeployed web app that merely
	// returns 200 is caught (ACD.4).
	if !readMCPInitialize(resp.Body) {
		return errors.Errorf("%s returned 200 but did not complete the MCP initialize handshake (not an MCP server?)", url)
	}
	a.uiWriter.SendStatusComplete("deploying", "✅ MCP server answered initialize")
	return nil
}

// readMCPInitialize scans a 2xx body incrementally and returns true as soon as a frame
// parses as a live MCP initialize result — so a streamable-HTTP server that keeps its SSE
// connection open after replying doesn't stall the probe to the full client timeout.
// Bounded to 1 MB. Handles a raw-JSON body and an SSE stream (`data:` lines, which the spec
// joins with "\n" within an event; a blank line ends the event).
func readMCPInitialize(r io.Reader) bool {
	scanner := bufio.NewScanner(io.LimitReader(r, 1<<20))
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	var raw []byte     // a non-SSE (possibly pretty-printed multi-line) JSON body
	var event [][]byte // data: segments of the current SSE event
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		switch {
		case bytes.HasPrefix(line, []byte("data:")):
			event = append(event, bytes.TrimSpace(line[len("data:"):]))
			if mcpInitializeOK(bytes.Join(event, []byte("\n"))) {
				return true // valid handshake — stop before blocking on further stream
			}
		case len(line) == 0:
			event = event[:0] // event boundary — reset
		default:
			raw = append(raw, line...)
		}
	}
	return mcpInitializeOK(raw)
}

// mcpInitializeOK reports whether an initialize response advertises an MCP server. It
// accepts either a raw JSON body or a Server-Sent-Events frame (`data: {...}`), which the
// streamable-HTTP transport uses.
func mcpInitializeOK(body []byte) bool {
	var r struct {
		Result struct {
			ServerInfo      json.RawMessage `json:"serverInfo"`
			Capabilities    json.RawMessage `json:"capabilities"`
			ProtocolVersion string          `json:"protocolVersion"`
		} `json:"result"`
	}
	if err := json.Unmarshal(extractJSONRPC(body), &r); err != nil {
		return false
	}
	return len(r.Result.ServerInfo) > 0 || len(r.Result.Capabilities) > 0 || r.Result.ProtocolVersion != ""
}

// extractJSONRPC returns the JSON object from either a raw JSON body or an SSE stream
// (concatenating any `data:` lines).
func extractJSONRPC(body []byte) []byte {
	if t := bytes.TrimSpace(body); len(t) > 0 && t[0] == '{' {
		return t
	}
	var buf []byte
	for _, line := range bytes.Split(body, []byte("\n")) {
		if line = bytes.TrimSpace(line); bytes.HasPrefix(line, []byte("data:")) {
			buf = append(buf, bytes.TrimSpace(line[len("data:"):])...)
		}
	}
	return buf
}

func (a *Activities) determineRootPath(ctx context.Context, routes []analyzer.RouteCandidate) (string, error) {
	a.uiWriter.SendStatus("analyzing", "Determining root path of your application")
	routeInputs := make([]types.RouteCandidate, len(routes))
	for i, r := range routes {
		routeInputs[i] = types.RouteCandidate{
			Method:  r.Method,
			Context: r.Context,
			File:    r.File,
			Path:    r.Path,
			Line:    int64(r.Line),
		}
	}
	r, err := a.llmClient.CategorizeRoutes(ctx, routeInputs)
	if err != nil {
		return "", errors.Errorf("failed to categorize routes: %w", err)
	}
	// just grab the recommend path from the LLM. The data comes back scored with a confidence, so
	// we can do more verification if needed
	a.uiWriter.SendStatusComplete("analyzing", "✅ root path determined")
	return r.Recommended.Path, nil
}
