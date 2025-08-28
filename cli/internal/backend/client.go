package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-errors/errors"
	"github.com/meroxa/prod/cli/internal/analyzer"
	"github.com/meroxa/prod/cli/internal/config"
)

var baseURL = fmt.Sprintf("%s/%s", config.SupabaseURL, "functions/v1")

type RegistryCredentials struct {
	Username   string `json:"dockerAuthUsername"`
	Token      string `json:"dockerAuthToken"`
	URL        string `json:"proxyEndpoint"`
	Repository string `json:"dockerRepo"`
	ExpiresAt  string `json:"expiresAt"`
	AccountID  string `json:"accountId"`
}

type Client struct {
	httpClient *http.Client
}

func NewClient() *Client {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}
	return &Client{httpClient: client}
}

// RecordRequestedStack sends usage data to the backend service. It will be the stack that we infered from the request so that we can see what users are requesting so we know what to support next
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

	url := fmt.Sprintf("%s/record-stack", baseURL)
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

// GetPushRegistryCredentials fetches temporary Docker registry credentials for pushing images. These are scoped to JUST being able to push to registries for the specified tenant
func (c *Client) GetPushRegistryCredentials(ctx context.Context, authToken string, projectName string) (*RegistryCredentials, error) {
	// Prepare request payload
	payload := map[string]string{
		"name": projectName,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.Errorf("failed to marshal request payload: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/push-token", baseURL), bytes.NewBuffer(payloadBytes))
	if err != nil {
		return nil, errors.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}

	// Make HTTP request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.Errorf("failed to make request to push-token endpoint: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, errors.Errorf("push-token endpoint returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Parse response
	var creds RegistryCredentials
	if err := json.NewDecoder(resp.Body).Decode(&creds); err != nil {
		return nil, errors.Errorf("failed to decode push-token response: %w", err)
	}

	// Validate required fields
	if creds.Username == "" || creds.Token == "" || creds.URL == "" || creds.Repository == "" {
		return nil, errors.Errorf("incomplete credentials received: username=%s, token present=%t, url=%s, repository=%s",
			creds.Username, creds.Token != "", creds.URL, creds.Repository)
	}

	// Strip https:// prefix as Docker doesn't accept it in image references
	creds.URL = strings.TrimPrefix(creds.URL, "https://")
	creds.URL = strings.TrimPrefix(creds.URL, "http://")

	return &creds, nil
}

// GetPullRegistryCredentials fetches temporary Docker registry credentials for pulling images. These are scoped to JUST being able to pull from registries for the specified tenant
func (c *Client) GetPullRegistryCredentials(ctx context.Context, authToken string, projectName string) (*RegistryCredentials, error) {
	// Prepare request payload
	payload := map[string]string{
		"name": projectName,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.Errorf("failed to marshal request payload: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/pull-token", baseURL), bytes.NewBuffer(payloadBytes))
	if err != nil {
		return nil, errors.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}

	// Make HTTP request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.Errorf("failed to make request to pull-token endpoint: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, errors.Errorf("pull-token endpoint returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Parse response
	var creds RegistryCredentials
	if err := json.NewDecoder(resp.Body).Decode(&creds); err != nil {
		return nil, errors.Errorf("failed to decode pull-token response: %w", err)
	}

	// Validate required fields
	if creds.Username == "" || creds.Token == "" || creds.URL == "" || creds.Repository == "" {
		return nil, errors.Errorf("incomplete credentials received: username=%s, token present=%t, url=%s, repository=%s",
			creds.Username, creds.Token != "", creds.URL, creds.Repository)
	}

	// Strip https:// prefix as Docker doesn't accept it in image references
	creds.URL = strings.TrimPrefix(creds.URL, "https://")
	creds.URL = strings.TrimPrefix(creds.URL, "http://")

	return &creds, nil
}

func (c *Client) CreateDockerRepository(ctx context.Context, authToken string, projectName string) error {
	data := map[string]any{
		"name": projectName,
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return errors.Errorf("failed to repository name: %w", err)
	}

	url := fmt.Sprintf("%s/create-repo", baseURL)
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

func (c *Client) GetBaseDockerImages(ctx context.Context) (map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/base-images", baseURL), nil)
	if err != nil {
		return nil, errors.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.Errorf("failed to make request to GetBaseDockerImages endpoint: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, errors.Errorf("base-images endpoint returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var images map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&images); err != nil {
		return nil, errors.Errorf("failed to decode push-token response: %w", err)
	}

	return images, nil
}
