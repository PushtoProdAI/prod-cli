package render

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// HTTPRenderClient implements the RenderClient interface with actual HTTP calls
type HTTPRenderClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	userAgent  string
}

// NewHTTPRenderClient creates a new HTTP-based Render client
func NewHTTPRenderClient(apiKey string) *HTTPRenderClient {
	return &HTTPRenderClient{
		baseURL: "https://api.render.com",
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		userAgent: "prod-cli/1.0",
	}
}

// makeRequest makes an HTTP request with proper authentication and error handling
func (c *HTTPRenderClient) makeRequest(ctx context.Context, method, endpoint string, body interface{}) (*http.Response, error) {
	var reqBody io.Reader

	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewBuffer(jsonData)
	}

	url := c.baseURL + endpoint
	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers per Render API docs
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}

	return resp, nil
}

// handleResponse handles HTTP response parsing and error checking
func (c *HTTPRenderClient) handleResponse(resp *http.Response, result interface{}) error {
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	// Check for HTTP errors
	if resp.StatusCode >= 400 {
		return fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Parse successful response
	if result != nil {
		if err := json.Unmarshal(body, result); err != nil {
			return fmt.Errorf("failed to parse response JSON: %w", err)
		}
	}

	return nil
}

// CreateProject creates a new project on Render
// Based on: https://api-docs.render.com/reference/create-project
func (c *HTTPRenderClient) CreateProject(ctx context.Context, req CreateProjectRequest) (*RenderProject, error) {
	resp, err := c.makeRequest(ctx, "POST", "/v1/projects", req)
	if err != nil {
		return nil, fmt.Errorf("failed to create project: %w", err)
	}

	var project RenderProject
	if err := c.handleResponse(resp, &project); err != nil {
		return nil, err
	}

	return &project, nil
}

// ListWorkspaces lists the workspaces that your API key has access to
// Based on: https://api-docs.render.com/reference/list-owners
func (c *HTTPRenderClient) ListWorkspaces(ctx context.Context) ([]*RenderWorkspace, error) {
	resp, err := c.makeRequest(ctx, "GET", "/v1/owners", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list workspaces: %w", err)
	}

	var workspaces []*RenderWorkspace
	if err := c.handleResponse(resp, &workspaces); err != nil {
		return nil, err
	}

	return workspaces, nil
}

// Placeholder implementations for interface compliance - will be implemented later
func (c *HTTPRenderClient) GetProject(ctx context.Context, projectID string) (*RenderProject, error) {
	return nil, fmt.Errorf("GetProject not yet implemented")
}

func (c *HTTPRenderClient) ListProjects(ctx context.Context) ([]*RenderProject, error) {
	resp, err := c.makeRequest(ctx, "GET", "/v1/projects", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list projects: %w", err)
	}

	var projects []*RenderProject
	if err := c.handleResponse(resp, &projects); err != nil {
		return nil, err
	}

	return projects, nil
}

func (c *HTTPRenderClient) DeleteProject(ctx context.Context, projectID string) error {
	return fmt.Errorf("DeleteProject not yet implemented")
}

// CreateWebService creates a new web service on Render
// Based on: https://api-docs.render.com/reference/create-service
func (c *HTTPRenderClient) CreateWebService(ctx context.Context, req CreateWebServiceRequest) (*RenderService, error) {
	resp, err := c.makeRequest(ctx, "POST", "/v1/services", req)
	if err != nil {
		return nil, fmt.Errorf("failed to create web service: %w", err)
	}

	var service RenderService
	if err := c.handleResponse(resp, &service); err != nil {
		return nil, err
	}

	return &service, nil
}

func (c *HTTPRenderClient) CreatePostgres(ctx context.Context, req CreatePostgresRequest) (*RenderService, error) {
	return nil, fmt.Errorf("CreatePostgres not yet implemented")
}

func (c *HTTPRenderClient) CreateRedis(ctx context.Context, req CreateRedisRequest) (*RenderService, error) {
	return nil, fmt.Errorf("CreateRedis not yet implemented")
}

func (c *HTTPRenderClient) GetPostgresConnectionInfo(ctx context.Context, serviceID string) (*PostgresConnectionInfo, error) {
	return nil, fmt.Errorf("GetPostgresConnectionInfo not yet implemented")
}

func (c *HTTPRenderClient) GetRedisConnectionInfo(ctx context.Context, serviceID string) (*RedisConnectionInfo, error) {
	return nil, fmt.Errorf("GetRedisConnectionInfo not yet implemented")
}

func (c *HTTPRenderClient) DeployBlueprint(ctx context.Context, blueprint *RenderBlueprint) error {
	return fmt.Errorf("DeployBlueprint not yet implemented")
}
