package flyio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

// HTTPFlyioClient implements the FlyioClient interface with actual HTTP calls
type HTTPFlyioClient struct {
	baseURL    string
	apiToken   string
	httpClient *http.Client
	userAgent  string
}

// NewHTTPFlyioClient creates a new HTTP-based Fly.io client
// DEPRECATED: This implementation is incomplete. Use NewFlyioClient() instead.
func NewHTTPFlyioClient(apiToken string) *HTTPFlyioClient {
	return &HTTPFlyioClient{
		baseURL:  "https://api.machines.dev",
		apiToken: apiToken,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		userAgent: "prod-cli/1.0",
	}
}

// NewFlyioClient creates the default Fly.io client implementation
// This factory method allows us to swap implementations in the future
func NewFlyioClient() FlyioClient {
	// For now, use the flyctl implementation
	// In the future, we can check for conditions to use different implementations:
	// - If Fly.io API becomes more complete, switch to HTTPFlyioClient
	// - If running in CI/CD, might use a different implementation
	// - Could check for feature flags or environment variables
	return NewFlyctlClient()
}

// makeRequest makes an HTTP request with proper authentication and error handling
func (c *HTTPFlyioClient) makeRequest(ctx context.Context, method, endpoint string, body any) (*http.Response, error) {

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

	// Set headers per Fly.io API docs
	req.Header.Set("Authorization", "Bearer "+c.apiToken)
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
func (c *HTTPFlyioClient) handleResponse(resp *http.Response, result any) error {
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}


	// Check for rate limit errors (429)
	if resp.StatusCode == 429 {
		retryAfter := c.parseRetryAfter(resp.Header)
		return &RateLimitError{
			RetryAfter: retryAfter,
			Message:    c.formatRateLimitMessage(retryAfter),
		}
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
func (c *HTTPFlyioClient) parseRetryAfter(headers http.Header) time.Duration {
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
func (c *HTTPFlyioClient) formatRateLimitMessage(retryAfter time.Duration) string {
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

// CreateApp creates a new app on Fly.io
func (c *HTTPFlyioClient) CreateApp(ctx context.Context, req CreateAppRequest) (*FlyioApp, error) {
	return nil, fmt.Errorf("CreateApp is not implemented for HTTP client - use flyctl client instead")
}

// GetApp retrieves app information
func (c *HTTPFlyioClient) GetApp(ctx context.Context, appID string) (*FlyioApp, error) {
	return nil, fmt.Errorf("GetApp is not implemented for HTTP client - use flyctl client instead")
}

// DeployApp deploys configuration to an app
func (c *HTTPFlyioClient) DeployApp(ctx context.Context, appID string, config *FlyioConfig) error {
	return fmt.Errorf("DeployApp is not implemented for HTTP client - use flyctl client instead")
}

// DestroyApp destroys an app
func (c *HTTPFlyioClient) DestroyApp(ctx context.Context, appID string) error {
	return fmt.Errorf("DestroyApp is not implemented for HTTP client - use flyctl client instead")
}

// CreatePostgres creates a new PostgreSQL database
func (c *HTTPFlyioClient) CreatePostgres(ctx context.Context, req CreatePostgresRequest) (*FlyioPostgresCluster, error) {
	return nil, fmt.Errorf("CreatePostgres is not implemented for HTTP client - use flyctl client instead")
}

// CreateRedis creates a new Redis database
func (c *HTTPFlyioClient) CreateRedis(ctx context.Context, req CreateRedisRequest) (*FlyioRedis, error) {
	return nil, fmt.Errorf("CreateRedis is not implemented for HTTP client - use flyctl client instead")
}

// GetPostgresConnectionInfo retrieves PostgreSQL connection information
func (c *HTTPFlyioClient) GetPostgresConnectionInfo(ctx context.Context, appID string) (*PostgresConnectionInfo, error) {
	return nil, fmt.Errorf("GetPostgresConnectionInfo is not implemented for HTTP client - use flyctl client instead")
}

// GetRedisConnectionInfo retrieves Redis connection information
func (c *HTTPFlyioClient) GetRedisConnectionInfo(ctx context.Context, appID string) (*RedisConnectionInfo, error) {
	return nil, fmt.Errorf("GetRedisConnectionInfo is not implemented for HTTP client - use flyctl client instead")
}

// AttachPostgres attaches a PostgreSQL database to an app
// Note: This is not implemented for HTTP client - use flyctl client instead
func (c *HTTPFlyioClient) AttachPostgres(ctx context.Context, req AttachPostgresRequest) error {
	return fmt.Errorf("AttachPostgres is not implemented for HTTP client - use flyctl client instead")
}

// AttachRedis attaches a Redis database to an app
// Note: This is not implemented for HTTP client - use flyctl client instead
func (c *HTTPFlyioClient) AttachRedis(ctx context.Context, req AttachRedisRequest) error {
	return fmt.Errorf("AttachRedis is not implemented for HTTP client - use flyctl client instead")
}

// CreateVolume creates a new volume
func (c *HTTPFlyioClient) CreateVolume(ctx context.Context, req CreateVolumeRequest) (*FlyioVolume, error) {
	return nil, fmt.Errorf("CreateVolume is not implemented for HTTP client - use flyctl client instead")
}

// GetAppLogs retrieves app logs
func (c *HTTPFlyioClient) GetAppLogs(ctx context.Context, appID string) ([]LogEntry, error) {
	return nil, fmt.Errorf("GetAppLogs is not implemented for HTTP client - use flyctl client instead")
}

// GetAppMetrics retrieves app metrics
func (c *HTTPFlyioClient) GetAppMetrics(ctx context.Context, appID string) (*AppMetrics, error) {
	return nil, fmt.Errorf("GetAppMetrics is not implemented for HTTP client - use flyctl client instead")
}
