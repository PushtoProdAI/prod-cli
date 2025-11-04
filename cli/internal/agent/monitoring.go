package agent

import (
	"context"
	"net/http"
	"time"

	"github.com/cschleiden/go-workflows/workflow"
	"github.com/go-errors/errors"
	"github.com/meroxa/prod/cli/baml_client/types"
	"github.com/meroxa/prod/cli/internal/analyzer"
	"github.com/meroxa/prod/cli/internal/deployment/render"
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

func (a *Activities) waitForAWSStack(ctx context.Context, authToken, stackName string) (map[string]string, error) {
	a.uiWriter.SendStatus("deploying", "Waiting for CloudFormation stack to complete...")

	status, err := a.beClient.GetAWSStackStatus(ctx, authToken, stackName)
	if err != nil {
		return nil, errors.Errorf("failed to get stack status: %w", err)
	}

	// Check for failure states
	if status.Status == "CREATE_FAILED" ||
		status.Status == "ROLLBACK_COMPLETE" ||
		status.Status == "ROLLBACK_FAILED" ||
		status.Status == "UPDATE_ROLLBACK_COMPLETE" ||
		status.Status == "UPDATE_ROLLBACK_FAILED" {
		errorMsg := "CloudFormation stack failed"
		if status.Error != "" {
			errorMsg = status.Error
		}
		return nil, errors.Errorf("CloudFormation deployment failed: %s (status: %s)", errorMsg, status.Status)
	}

	// Check if deployment is complete
	if status.Status != "CREATE_COMPLETE" && status.Status != "UPDATE_COMPLETE" {
		return nil, errors.Errorf("CloudFormation stack not yet complete, current status: %s", status.Status)
	}

	// Return stack outputs on success
	return status.Outputs, nil
}

func (a *Activities) getFlyIOAppURL(ctx context.Context, appID string) (string, error) {
	service, err := a.flyClient.GetApp(ctx, appID)
	if err != nil {
		return "", errors.Errorf("failed to get service info: %w", err)
	}
	return service.Hostname, nil
}

func (a *Activities) isURLLive(ctx context.Context, url string) error {
	// we could also use the deploys endpoint and check the status of the latest deploy,
	// but using the URL saves us on the rate limiting and ultimately is what the user cares about
	client := &http.Client{
		Timeout: 10 * time.Second,
	}
	a.uiWriter.SendStatus("deploying", "Waiting for URL to be live...")
	resp, err := client.Get(url)
	if err != nil {
		return errors.Errorf("failed to make GET request to %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode > 300 {
		return errors.Errorf("received non-success status code %d from %s", resp.StatusCode, url)
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
