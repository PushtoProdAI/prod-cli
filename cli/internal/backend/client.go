package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-errors/errors"
	"github.com/pushtoprodai/prod-cli/internal/analyzer"
	"github.com/pushtoprodai/prod-cli/internal/config"
)

func getBaseURL() string {
	return fmt.Sprintf("%s/%s", config.GetSupabaseURL(), "functions/v1")
}

// Client is the main backend API client
type Client struct {
	httpClient *http.Client
}

// NewClient creates a new backend client
func NewClient() *Client {
	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	return &Client{
		httpClient: httpClient,
	}
}

// RecordRequestedStack sends usage data to the backend service
// It records the stack that was inferred from the request so we can see what users are requesting
func (c *Client) RecordRequestedStack(ctx context.Context, authToken string, platform string, language string, serviceRequirements []analyzer.ServiceRequirement) error {
	data := map[string]any{
		"platform":            platform,
		"language":            language,
		"serviceRequirements": serviceRequirements,
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return errors.Errorf("failed to marshal usage data: %w", err)
	}

	url := fmt.Sprintf("%s/record-stack", getBaseURL())
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
		return errors.Errorf("request failed with status: %d", resp.StatusCode)
	}

	return nil
}

// Deployment history types
type DeploymentHistoryItem struct {
	OperationID   string         `json:"operation_id"`
	UserID        string         `json:"user_id"`
	OperationType string         `json:"operation_type"`
	ResourceType  string         `json:"resource_type"`
	ResourceID    string         `json:"resource_id"`
	ResourceName  string         `json:"resource_name"`
	Status        string         `json:"status"`
	Platform      string         `json:"platform"`
	Language      string         `json:"language"`
	StartedAt     string         `json:"started_at"`
	CompletedAt   string         `json:"completed_at"`
	Duration      int            `json:"duration_seconds"`
	Metadata      map[string]any `json:"metadata"`
}

type DeploymentHistoryResponse struct {
	Data       []DeploymentHistoryItem `json:"data"`
	Pagination struct {
		Page       int `json:"page"`
		Limit      int `json:"limit"`
		Total      int `json:"total"`
		TotalPages int `json:"total_pages"`
	} `json:"pagination"`
}

type DeploymentQueryOptions struct {
	ResourceName  string // Filter by service name (e.g., "my-app")
	Platform      string // Filter by platform (e.g., "aws", "vercel")
	Status        string // Filter by status (e.g., "success", "failed")
	OperationType string // Filter by operation type (e.g., "deploy", "rollback")
	Limit         int    // Max results per page (default: 50, max: 1000)
	Page          int    // Page number (default: 1)
}
