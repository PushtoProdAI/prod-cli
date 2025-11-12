package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-errors/errors"
)

// LogDeploymentOperation logs a deployment operation to the backend
func (c *Client) LogDeploymentOperation(ctx context.Context, authToken string, operation map[string]any) (string, error) {
	payload := map[string]any{
		"action": "log_deployment",
		"data":   operation,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", errors.Errorf("failed to marshal deployment operation data: %w", err)
	}

	url := fmt.Sprintf("%s/deployment-logger", getBaseURL())
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", errors.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", errors.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", errors.Errorf("deployment logger request failed with status: %d", resp.StatusCode)
	}

	var result struct {
		Success bool   `json:"success"`
		Data    string `json:"data"`
		Error   string `json:"error,omitempty"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", errors.Errorf("failed to decode response: %w", err)
	}

	if !result.Success {
		return "", errors.Errorf("deployment logger returned error: %s", result.Error)
	}

	return result.Data, nil
}

// UpdateDeploymentOperation updates a deployment operation status
func (c *Client) UpdateDeploymentOperation(ctx context.Context, authToken string, operationId string, status string, metadata map[string]any) error {
	payload := map[string]any{
		"action": "update_deployment",
		"data": map[string]any{
			"operation_id": operationId,
			"status":       status,
			"metadata":     metadata,
		},
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return errors.Errorf("failed to marshal deployment update data: %w", err)
	}

	url := fmt.Sprintf("%s/deployment-logger", getBaseURL())
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return errors.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return errors.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return errors.Errorf("deployment logger update request failed with status: %d", resp.StatusCode)
	}

	var result struct {
		Success bool   `json:"success"`
		Error   string `json:"error,omitempty"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return errors.Errorf("failed to decode response: %w", err)
	}

	if !result.Success {
		return errors.Errorf("deployment logger update returned error: %s", result.Error)
	}

	return nil
}

// GetDeploymentHistory retrieves deployment history for a specific service
// Returns the most recent successful deployments, ordered by completion time (newest first)
func (c *Client) GetDeploymentHistory(ctx context.Context, authToken string, serviceName string, platform string, limit int) ([]DeploymentHistoryItem, error) {
	// Use the more flexible QueryDeployments method with success filter
	opts := DeploymentQueryOptions{
		ResourceName: serviceName,
		Platform:     platform,
		Status:       "success",
		Limit:        limit,
		Page:         1,
	}

	response, err := c.QueryDeployments(ctx, authToken, opts)
	if err != nil {
		return nil, err
	}

	return response.Data, nil
}

// QueryDeployments retrieves deployment operations with flexible filtering and pagination
// This is the unified method that supports all query options
func (c *Client) QueryDeployments(ctx context.Context, authToken string, opts DeploymentQueryOptions) (*DeploymentHistoryResponse, error) {
	// Build query parameters
	params := make([]string, 0)

	if opts.ResourceName != "" {
		params = append(params, fmt.Sprintf("resource_name=%s", opts.ResourceName))
	}
	if opts.Platform != "" {
		params = append(params, fmt.Sprintf("platform=%s", opts.Platform))
	}
	if opts.Status != "" {
		params = append(params, fmt.Sprintf("status=%s", opts.Status))
	}
	if opts.OperationType != "" {
		params = append(params, fmt.Sprintf("operation_type=%s", opts.OperationType))
	}

	// Set defaults for pagination
	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}
	page := opts.Page
	if page <= 0 {
		page = 1
	}
	params = append(params, fmt.Sprintf("limit=%d", limit))
	params = append(params, fmt.Sprintf("page=%d", page))

	// Build URL with query parameters
	queryString := strings.Join(params, "&")
	url := fmt.Sprintf("%s/deployment-logger?%s", getBaseURL(), queryString)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, errors.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, errors.Errorf("deployment query request failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result DeploymentHistoryResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, errors.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}
