package agent

import (
	"context"
	"fmt"
	"net/http"
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

// verifyLiveness confirms a freshly-deployed service is up, according to its
// shape. Web and MCP servers answer HTTP, so we probe the URL. A worker or cron
// job has no URL — its liveness is the platform's concern (adapter-owned), not an
// HTTP GET — so we skip the probe instead of auto-failing (and auto-rolling-back)
// a perfectly healthy non-HTTP deploy. Only the two explicitly non-HTTP shapes
// skip; anything else (including an unset shape) is URL-probed.
func (a *Activities) verifyLiveness(ctx context.Context, shape deployment.DeployShape, url string) error {
	if shape == deployment.ShapeWorker || shape == deployment.ShapeCron {
		a.uiWriter.SendStatusComplete("deploying", fmt.Sprintf("✅ %s deploy — no HTTP liveness check", shape))
		return nil
	}
	return a.isURLLive(ctx, url)
}

func (a *Activities) isURLLive(ctx context.Context, url string) error {
	// We probe the URL rather than the platform's deploy API — it avoids rate limits and
	// is what the user actually cares about. "Live" means the service is *responding*:
	// an auth wall (401/403), a redirect to a login page, or any 2xx all mean it's up and
	// serving. Only a connection failure, a timeout, or a 5xx (the app itself is broken)
	// counts as not-live. Treating every status >300 as dead — the old behavior — failed
	// deploys of any app behind auth or a redirect.
	client := &http.Client{
		Timeout: 10 * time.Second,
		// Follow exactly one redirect, then judge the destination (a 302→/login is a
		// live app). CheckRedirect is called *before* each hop with `via` holding the
		// requests already made, so the first redirect has len(via)==1 (allow it) and
		// the second has len(via)==2 (stop and judge the first hop's target).
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= 2 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
	a.uiWriter.SendStatus("deploying", "Waiting for URL to be live...")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return errors.Errorf("failed to build request to %s: %w", url, err)
	}
	resp, err := client.Do(req)
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
