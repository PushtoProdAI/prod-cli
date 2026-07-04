package render

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-errors/errors"

	"github.com/pushtoprodai/prod-cli/internal/output"
)

// RateLimitError represents a rate limit error with retry information
type RateLimitError struct {
	RetryAfter time.Duration
	Message    string
}

func (e *RateLimitError) Error() string {
	return e.Message
}

// HTTPError represents an HTTP error with status code information
type HTTPError struct {
	StatusCode int
	Message    string
}

func (e *HTTPError) Error() string {
	return e.Message
}

// IsClientError returns true if the status code is in the 4xx range
func (e *HTTPError) IsClientError() bool {
	return e.StatusCode >= 400 && e.StatusCode < 500
}

// IsServerError returns true if the status code is in the 5xx range
func (e *HTTPError) IsServerError() bool {
	return e.StatusCode >= 500 && e.StatusCode < 600
}

// IsRedirection returns true if the status code is in the 3xx range
func (e *HTTPError) IsRedirection() bool {
	return e.StatusCode >= 300 && e.StatusCode < 400
}

// HTTPRenderClient implements the RenderClient interface with actual HTTP calls
type HTTPRenderClient struct {
	baseURL    string
	httpClient *http.Client
	userAgent  string
	writer     io.Writer
}

// NewHTTPRenderClient creates a new HTTP-based Render client
// The apiKey parameter is ignored - the client will dynamically read from RENDER_API_KEY environment variable
func NewHTTPRenderClient(apiKey string, writer io.Writer) *HTTPRenderClient {
	if writer == nil {
		writer = output.NewNoOpWriter()
	}
	return &HTTPRenderClient{
		baseURL: "https://api.render.com",
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		userAgent: "prod-cli/1.0",
		writer:    writer,
	}
}

// getAPIKey retrieves the Render API key from environment variable or config file
func (c *HTTPRenderClient) getAPIKey() string {
	// First check environment variable
	apiKey := os.Getenv("RENDER_API_KEY")
	if apiKey != "" {
		return apiKey
	}

	// Fall back to config file
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	configPath := filepath.Join(homeDir, ".render", "cli.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return ""
	}

	// Simple YAML parsing for the key field
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "key:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}

	return ""
}

// makeRequest makes an HTTP request with proper authentication and error handling
func (c *HTTPRenderClient) makeRequest(ctx context.Context, method, endpoint string, body any) (*http.Response, error) {
	var reqBody io.Reader

	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, errors.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewBuffer(jsonData)
	}

	url := c.baseURL + endpoint
	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, errors.Errorf("failed to create request: %w", err)
	}

	// Set headers per Render API docs
	// Get API key from environment variable or config file
	apiKey := c.getAPIKey()
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.Errorf("failed to make request: %w", err)
	}

	return resp, nil
}

// handleResponse handles HTTP response parsing and error checking
func (c *HTTPRenderClient) handleResponse(resp *http.Response, result any) error {
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return errors.Errorf("failed to read response body: %w", err)
	}

	// Check for rate limit errors (429)
	if resp.StatusCode == 429 {
		retryAfter := c.parseRetryAfter(resp.Header)
		message := c.formatRateLimitMessage(retryAfter)
		fmt.Fprintf(c.writer, "%s\n", message)
		os.Exit(1)
	}

	// Check for HTTP errors
	if resp.StatusCode >= 400 {
		return &HTTPError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("API request failed with status %d: %s", resp.StatusCode, string(body)),
		}
	}

	// Parse successful response
	if result != nil {
		if err := json.Unmarshal(body, result); err != nil {
			return errors.Errorf("failed to parse response JSON: %w", err)
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
		return nil, errors.Errorf("failed to list workspaces: %w", err)
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
		return nil, errors.Errorf("failed to create web service: %w", err)
	}

	var createdResp CreateWebServiceResponse
	if err := c.handleResponse(resp, &createdResp); err != nil {
		return nil, err
	}
	return &createdResp.Service, nil
}

func (c *HTTPRenderClient) UpdateServiceImage(ctx context.Context, serviceID string, req UpdateServiceImageRequest) error {
	updateReq := map[string]any{
		"image": req.Image,
	}

	resp, err := c.makeRequest(ctx, "PATCH", fmt.Sprintf("/v1/services/%s", serviceID), updateReq)
	if err != nil {
		return errors.Errorf("failed to update service image: %w", err)
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		slog.Warn("Failed to read response body", "error", readErr)
	}

	if resp.StatusCode != http.StatusOK {
		return errors.Errorf("failed to update service image: status %d, body: %s", resp.StatusCode, string(body))
	}

	return nil
}

// CreatePostgres creates a new PostgreSQL database service on Render
// Based on: https://api-docs.render.com/reference/create-postgres
func (c *HTTPRenderClient) CreatePostgres(ctx context.Context, req CreatePostgresRequest) (*RenderService, error) {
	resp, err := c.makeRequest(ctx, "POST", "/v1/postgres", req)
	if err != nil {
		return nil, errors.Errorf("failed to create postgres service: %w", err)
	}

	var service RenderService
	if err := c.handleResponse(resp, &service); err != nil {
		return nil, err
	}

	return &service, nil
}

// CreateRedis creates a new Redis key-value store service on Render using the Key Value API
// Note: This uses the Key Value API under the hood, as the Redis API is deprecated
// Based on: https://api-docs.render.com/reference/create-key-value
func (c *HTTPRenderClient) CreateRedis(ctx context.Context, req CreateRedisRequest) (*RenderService, error) {
	// Convert to Key Value request
	kvReq := CreateKeyValueRequest{
		Name:    req.Name,
		OwnerID: req.OwnerID,
		Plan:    req.Plan,
		Region:  "virginia", // Default region
	}

	resp, err := c.makeRequest(ctx, "POST", "/v1/key-value", kvReq)
	if err != nil {
		return nil, errors.Errorf("failed to create key-value service: %w", err)
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
		return nil, errors.Errorf("failed to get postgres connection info: %w", err)
	}

	var connectionInfo PostgresConnectionInfo
	if err := c.handleResponse(resp, &connectionInfo); err != nil {
		return nil, err
	}

	return &connectionInfo, nil
}

// GetRedisConnectionInfo retrieves the connection strings for a Redis/Key Value service
// Note: This uses the Key Value API under the hood, as the Redis API is deprecated
// Based on: https://api-docs.render.com/reference/retrieve-key-value-connection-info
func (c *HTTPRenderClient) GetRedisConnectionInfo(ctx context.Context, serviceID string) (*RedisConnectionInfo, error) {
	resp, err := c.makeRequest(ctx, "GET", fmt.Sprintf("/v1/key-value/%s/connection-info", serviceID), nil)
	if err != nil {
		return nil, errors.Errorf("failed to get key-value connection info: %w", err)
	}

	var connectionInfo KeyValueConnectionInfo
	if err := c.handleResponse(resp, &connectionInfo); err != nil {
		return nil, err
	}

	// Convert to RedisConnectionInfo for API compatibility
	return &RedisConnectionInfo{
		InternalConnectionString: connectionInfo.InternalConnectionString,
		ExternalConnectionString: connectionInfo.ExternalConnectionString,
	}, nil
}

func (c *HTTPRenderClient) GetWebService(ctx context.Context, serviceID string) (*RenderWebService, error) {
	resp, err := c.makeRequest(ctx, "GET", fmt.Sprintf("/v1/services/%s", serviceID), nil)
	if err != nil {
		return nil, errors.Errorf("failed to get service info: %w", err)
	}

	var webService RenderWebService
	if err := c.handleResponse(resp, &webService); err != nil {
		return nil, err
	}
	slog.Info("Retrieved web service", "service", webService)
	return &webService, nil
}

// GetKeyValue retrieves details about a Key Value (Redis) service
// Based on: https://api-docs.render.com/reference/retrieve-key-value
func (c *HTTPRenderClient) GetKeyValue(ctx context.Context, serviceID string) (*RenderKeyValue, error) {
	resp, err := c.makeRequest(ctx, "GET", fmt.Sprintf("/v1/key-value/%s", serviceID), nil)
	if err != nil {
		return nil, errors.Errorf("failed to get key-value service info: %w", err)
	}

	var keyValue RenderKeyValue
	if err := c.handleResponse(resp, &keyValue); err != nil {
		return nil, err
	}

	return &keyValue, nil
}

func (c *HTTPRenderClient) GetPostgres(ctx context.Context, serviceID string) (*RenderPostgres, error) {
	resp, err := c.makeRequest(ctx, "GET", fmt.Sprintf("/v1/postgres/%s", serviceID), nil)
	if err != nil {
		return nil, errors.Errorf("failed to get postgres service info: %w", err)
	}

	var postgres RenderPostgres
	if err := c.handleResponse(resp, &postgres); err != nil {
		return nil, err
	}
	return &postgres, nil
}

func (c *HTTPRenderClient) DeployBlueprint(ctx context.Context, blueprint *RenderBlueprint) error {
	return errors.Errorf("DeployBlueprint not yet implemented")
}

// ListRegistryCredentials lists all registry credentials for a given owner
// Based on: https://api-docs.render.com/reference/list-registry-credentials
func (c *HTTPRenderClient) ListRegistryCredentials(ctx context.Context, ownerID string) ([]*RegistryCredential, error) {
	endpoint := fmt.Sprintf("/v1/registrycredentials?ownerId=%s", ownerID)
	resp, err := c.makeRequest(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, errors.Errorf("failed to list registry credentials: %w", err)
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

func (c *HTTPRenderClient) UpdateRegistryCredential(ctx context.Context, credID string, req UpdateRegistryCredentialRequest) (*RegistryCredential, error) {
	endpoint := fmt.Sprintf("/v1/registrycredentials/%s", credID)
	resp, err := c.makeRequest(ctx, "PATCH", endpoint, req)
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

func (c *HTTPRenderClient) DeleteRegistryCredential(ctx context.Context, credID string) error {
	endpoint := fmt.Sprintf("/v1/registrycredentials/%s", credID)
	slog.Info("Deleting registry credential", "credID", credID, "endpoint", endpoint)

	resp, err := c.makeRequest(ctx, "DELETE", endpoint, nil)
	if err != nil {
		slog.Error("Failed to make delete request", "error", err)
		return err
	}
	defer resp.Body.Close()

	// Read response body for error details
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		slog.Warn("Failed to read response body", "error", readErr)
	}

	slog.Info("Delete registry credential response", "status", resp.StatusCode, "body", string(body))

	// Accept 200 OK, 204 No Content, or 404 Not Found (already deleted)
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		slog.Error("Failed to delete registry credential", "status", resp.StatusCode, "body", string(body))
		return errors.Errorf("failed to delete registry credential: status %d, body: %s", resp.StatusCode, string(body))
	}

	// Log if credential was already gone
	if resp.StatusCode == http.StatusNotFound {
		slog.Info("Registry credential already deleted or not found", "credID", credID)
	}

	return nil
}

func (c *HTTPRenderClient) ListServices(ctx context.Context, name string) ([]RenderService, error) {
	endpoint := "/v1/services"
	if name != "" {
		endpoint = endpoint + "?name=" + name
	}

	resp, err := c.makeRequest(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, errors.Errorf("failed to list services: %w", err)
	}

	var wrappedServices []struct {
		Service RenderService `json:"service"`
	}
	if err := c.handleResponse(resp, &wrappedServices); err != nil {
		return nil, err
	}

	services := make([]RenderService, len(wrappedServices))
	for i, wrapped := range wrappedServices {
		services[i] = wrapped.Service
	}

	return services, nil
}

func (c *HTTPRenderClient) ListPostgres(ctx context.Context) ([]RenderPostgres, error) {
	resp, err := c.makeRequest(ctx, "GET", "/v1/postgres", nil)
	if err != nil {
		return nil, errors.Errorf("failed to list postgres: %w", err)
	}

	var wrappedPostgres []struct {
		Postgres RenderPostgres `json:"postgres"`
	}
	if err := c.handleResponse(resp, &wrappedPostgres); err != nil {
		return nil, err
	}

	postgres := make([]RenderPostgres, len(wrappedPostgres))
	for i, wrapped := range wrappedPostgres {
		postgres[i] = wrapped.Postgres
	}

	return postgres, nil
}

// ListRedis lists all Key Value (Redis) instances
// Note: This uses the Key Value API under the hood, as the Redis API is deprecated
// Based on: https://api-docs.render.com/reference/list-key-value
func (c *HTTPRenderClient) ListRedis(ctx context.Context) ([]RenderService, error) {
	resp, err := c.makeRequest(ctx, "GET", "/v1/key-value", nil)
	if err != nil {
		return nil, errors.Errorf("failed to list key-value services: %w", err)
	}

	var wrappedKeyValue []struct {
		KeyValue RenderService `json:"keyValue"`
	}
	if err := c.handleResponse(resp, &wrappedKeyValue); err != nil {
		return nil, err
	}

	keyValues := make([]RenderService, len(wrappedKeyValue))
	for i, wrapped := range wrappedKeyValue {
		keyValues[i] = wrapped.KeyValue
	}

	return keyValues, nil
}

func (c *HTTPRenderClient) TriggerDeploy(ctx context.Context, serviceID string) (*RenderDeploy, error) {
	resp, err := c.makeRequest(ctx, "POST", fmt.Sprintf("/v1/services/%s/deploys", serviceID), nil)
	if err != nil {
		return nil, errors.Errorf("failed to trigger deploy: %w", err)
	}
	defer resp.Body.Close()

	var deploy RenderDeploy
	if err := c.handleResponse(resp, &deploy); err != nil {
		return nil, err
	}

	return &deploy, nil
}

func (c *HTTPRenderClient) GetDeploy(ctx context.Context, serviceID, deployID string) (*RenderDeploy, error) {
	resp, err := c.makeRequest(ctx, "GET", fmt.Sprintf("/v1/services/%s/deploys/%s", serviceID, deployID), nil)
	if err != nil {
		return nil, errors.Errorf("failed to get deploy: %w", err)
	}
	defer resp.Body.Close()

	var deploy RenderDeploy
	if err := c.handleResponse(resp, &deploy); err != nil {
		return nil, err
	}

	return &deploy, nil
}

func (c *HTTPRenderClient) ListDeploys(ctx context.Context, serviceID string) ([]*RenderDeploy, error) {
	resp, err := c.makeRequest(ctx, "GET", fmt.Sprintf("/v1/services/%s/deploys", serviceID), nil)
	if err != nil {
		return nil, errors.Errorf("failed to list deploys: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, &HTTPError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("API request failed with status %d: %s", resp.StatusCode, string(body)),
		}
	}

	var wrappedDeploys []struct {
		Deploy RenderDeploy `json:"deploy"`
		Cursor string       `json:"cursor"`
	}
	if err := json.Unmarshal(body, &wrappedDeploys); err != nil {
		return nil, errors.Errorf("failed to parse response JSON: %w", err)
	}

	deploys := make([]*RenderDeploy, len(wrappedDeploys))
	for i, wrapped := range wrappedDeploys {
		deploy := wrapped.Deploy
		deploys[i] = &deploy
	}

	// Sort deploys by createdAt descending (newest first)
	sort.Slice(deploys, func(i, j int) bool {
		timeI, errI := time.Parse(time.RFC3339, deploys[i].CreatedAt)
		timeJ, errJ := time.Parse(time.RFC3339, deploys[j].CreatedAt)
		if errI != nil || errJ != nil {
			return false
		}
		return timeI.After(timeJ)
	})

	slog.Info("ListDeploys parsed and sorted", "count", len(deploys))
	for i, d := range deploys {
		slog.Info("Deploy", "index", i, "id", d.ID, "status", d.Status, "createdAt", d.CreatedAt)
	}

	return deploys, nil
}

func (c *HTTPRenderClient) RollbackDeploy(ctx context.Context, serviceID, deployID string) (*RenderDeploy, error) {
	rollbackReq := map[string]string{
		"deployId": deployID,
	}

	resp, err := c.makeRequest(ctx, "POST", fmt.Sprintf("/v1/services/%s/rollback", serviceID), rollbackReq)
	if err != nil {
		return nil, errors.Errorf("failed to rollback deploy: %w", err)
	}
	defer resp.Body.Close()

	var deploy RenderDeploy
	if err := c.handleResponse(resp, &deploy); err != nil {
		return nil, err
	}

	return &deploy, nil
}
