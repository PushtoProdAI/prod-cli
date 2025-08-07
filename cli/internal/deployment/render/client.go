package render

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)

// RateLimitError represents a rate limit error with retry information
type RateLimitError struct {
	RetryAfter time.Duration
	Message    string
}

func (e *RateLimitError) Error() string {
	return e.Message
}

// HTTPRenderClient implements the RenderClient interface with actual HTTP calls
type HTTPRenderClient struct {
	baseURL    string
	httpClient *http.Client
	userAgent  string
}

// NewHTTPRenderClient creates a new HTTP-based Render client
// The apiKey parameter is ignored - the client will dynamically read from RENDER_API_KEY environment variable
func NewHTTPRenderClient(apiKey string) *HTTPRenderClient {
	return &HTTPRenderClient{
		baseURL: "https://api.render.com",
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		userAgent: "prod-cli/1.0",
	}
}

// makeRequest makes an HTTP request with proper authentication and error handling
func (c *HTTPRenderClient) makeRequest(ctx context.Context, method, endpoint string, body any) (*http.Response, error) {
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
	// Dynamically get API key from environment variable
	apiKey := os.Getenv("RENDER_API_KEY")
	req.Header.Set("Authorization", "Bearer "+apiKey)
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
func (c *HTTPRenderClient) handleResponse(resp *http.Response, result any) error {
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	// Check for rate limit errors (429)
	if resp.StatusCode == 429 {
		retryAfter := c.parseRetryAfter(resp.Header)
		message := c.formatRateLimitMessage(retryAfter)
		fmt.Println(message)
		os.Exit(1)
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

// parseRetryAfter parses the Retry-After header and returns the duration
func (c *HTTPRenderClient) parseRetryAfter(headers http.Header) time.Duration {
	retryAfterStr := headers.Get("Retry-After")
	if retryAfterStr == "" {
		// Default to 60 seconds if no Retry-After header
		return 60 * time.Second
	}

	// Try to parse as seconds first
	if seconds, err := strconv.Atoi(retryAfterStr); err == nil {
		return time.Duration(seconds) * time.Second
	}

	// Try to parse as HTTP date format
	if t, err := time.Parse(time.RFC1123, retryAfterStr); err == nil {
		return time.Until(t)
	}

	// Default to 60 seconds if parsing fails
	return 60 * time.Second
}

// formatRateLimitMessage creates a human-readable rate limit message
func (c *HTTPRenderClient) formatRateLimitMessage(retryAfter time.Duration) string {
	minutes := int(retryAfter.Minutes())
	seconds := int(retryAfter.Seconds()) % 60

	var timeStr string
	if minutes > 0 {
		if seconds > 0 {
			timeStr = fmt.Sprintf("%d minutes and %d seconds", minutes, seconds)
		} else {
			timeStr = fmt.Sprintf("%d minutes", minutes)
		}
	} else {
		timeStr = fmt.Sprintf("%d seconds", seconds)
	}

	// Calculate when the rate limit will expire
	expiryTime := time.Now().Add(retryAfter)
	expiryTimeStr := expiryTime.Format("3:04 PM")

	return fmt.Sprintf("🚫 Rate limit exceeded\n\nOur system has exceeded its allowed requests for a short period. Please try again in %s (at %s).", timeStr, expiryTimeStr)
}

// ListWorkspaces lists the workspaces that your API key has access to
// Based on: https://api-docs.render.com/reference/list-owners
func (c *HTTPRenderClient) ListWorkspaces(ctx context.Context) ([]RenderWorkspace, error) {
	resp, err := c.makeRequest(ctx, "GET", "/v1/owners", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list workspaces: %w", err)
	}

	var workspaces []RenderWorkspace
	if err := c.handleResponse(resp, &workspaces); err != nil {
		return nil, err
	}
	return workspaces, nil
}

// CreateWebService creates a new web service on Render
// Based on: https://api-docs.render.com/reference/create-service
func (c *HTTPRenderClient) CreateWebService(ctx context.Context, req CreateWebServiceRequest) (*RenderService, error) {
	resp, err := c.makeRequest(ctx, "POST", "/v1/services", req)
	if err != nil {
		return nil, fmt.Errorf("failed to create web service: %w", err)
	}

	var createdResp CreateWebServiceResponse
	if err := c.handleResponse(resp, &createdResp); err != nil {
		return nil, err
	}
	return &createdResp.Service, nil
}

// CreatePostgres creates a new PostgreSQL database service on Render
// Based on: https://api-docs.render.com/reference/create-postgres
func (c *HTTPRenderClient) CreatePostgres(ctx context.Context, req CreatePostgresRequest) (*RenderService, error) {
	resp, err := c.makeRequest(ctx, "POST", "/v1/postgres", req)
	if err != nil {
		return nil, fmt.Errorf("failed to create postgres service: %w", err)
	}

	var service RenderService
	if err := c.handleResponse(resp, &service); err != nil {
		return nil, err
	}

	return &service, nil
}

// CreateRedis creates a new Redis key-value store service on Render
// Based on: https://api-docs.render.com/reference/create-redis
func (c *HTTPRenderClient) CreateRedis(ctx context.Context, req CreateRedisRequest) (*RenderService, error) {
	resp, err := c.makeRequest(ctx, "POST", "/v1/redis", req)
	if err != nil {
		return nil, fmt.Errorf("failed to create redis service: %w", err)
	}

	var service RenderService
	if err := c.handleResponse(resp, &service); err != nil {
		return nil, err
	}

	return &service, nil
}

// GetPostgresConnectionInfo retrieves the connection strings for a PostgreSQL service
// Based on: https://api-docs.render.com/reference/retrieve-postgres-connection-info
func (c *HTTPRenderClient) GetPostgresConnectionInfo(ctx context.Context, serviceID string) (*PostgresConnectionInfo, error) {
	resp, err := c.makeRequest(ctx, "GET", fmt.Sprintf("/v1/postgres/%s/connection-info", serviceID), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get postgres connection info: %w", err)
	}

	var connectionInfo PostgresConnectionInfo
	if err := c.handleResponse(resp, &connectionInfo); err != nil {
		return nil, err
	}

	return &connectionInfo, nil
}

// GetRedisConnectionInfo retrieves the connection strings for a Redis service
// Based on: https://api-docs.render.com/reference/retrieve-redis-connection-info
func (c *HTTPRenderClient) GetRedisConnectionInfo(ctx context.Context, serviceID string) (*RedisConnectionInfo, error) {
	resp, err := c.makeRequest(ctx, "GET", fmt.Sprintf("/v1/redis/%s/connection-info", serviceID), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get redis connection info: %w", err)
	}

	var connectionInfo RedisConnectionInfo
	if err := c.handleResponse(resp, &connectionInfo); err != nil {
		return nil, err
	}

	return &connectionInfo, nil
}

func (c *HTTPRenderClient) GetWebService(ctx context.Context, serviceID string) (*RenderWebService, error) {
	resp, err := c.makeRequest(ctx, "GET", fmt.Sprintf("/v1/services/%s", serviceID), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get service info: %w", err)
	}

	var webService RenderWebService
	if err := c.handleResponse(resp, &webService); err != nil {
		return nil, err
	}
	log.Printf("Retrieved web service: %+v", webService)
	return &webService, nil
}

func (c *HTTPRenderClient) GetPostgres(ctx context.Context, serviceID string) (*RenderPostgres, error) {
	resp, err := c.makeRequest(ctx, "GET", fmt.Sprintf("/v1/postgres/%s", serviceID), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get postgres service info: %w", err)
	}

	var postgres RenderPostgres
	if err := c.handleResponse(resp, &postgres); err != nil {
		return nil, err
	}
	return &postgres, nil
}

func (c *HTTPRenderClient) DeployBlueprint(ctx context.Context, blueprint *RenderBlueprint) error {
	return fmt.Errorf("DeployBlueprint not yet implemented")
}

// ListRegistryCredentials lists all registry credentials for a given owner
// Based on: https://api-docs.render.com/reference/list-registry-credentials
func (c *HTTPRenderClient) ListRegistryCredentials(ctx context.Context, ownerID string) ([]*RegistryCredential, error) {
	resp, err := c.makeRequest(ctx, "GET", "/v1/registrycredentials", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list registry credentials: %w", err)
	}

	var credentials []*RegistryCredential
	if err := c.handleResponse(resp, &credentials); err != nil {
		return nil, err
	}

	return credentials, nil
}

func (c *HTTPRenderClient) CreateRegistryCredential(ctx context.Context, req CreateRegistryCredentialRequest) (*RegistryCredential, error) {
	resp, err := c.makeRequest(ctx, "POST", "/v1/registrycredentials", req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var registryCred RegistryCredential
	if err := c.handleResponse(resp, &registryCred); err != nil {
		return nil, err
	}

	return &registryCred, nil
}
