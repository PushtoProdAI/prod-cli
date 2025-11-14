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

// RegistryCredentials contains Docker registry authentication credentials
type RegistryCredentials struct {
	Username   string `json:"dockerAuthUsername"`
	Token      string `json:"dockerAuthToken"`
	URL        string `json:"proxyEndpoint"`
	Repository string `json:"dockerRepo"`
	ExpiresAt  string `json:"expiresAt"`
	AccountID  string `json:"accountId"`
}

// GetPushRegistryCredentials fetches temporary Docker registry credentials for pushing images
// These are scoped to JUST being able to push to registries for the specified tenant
func (c *Client) GetPushRegistryCredentials(ctx context.Context, authToken string, projectName string) (*RegistryCredentials, error) {
	return c.getPushRegistryCredentialsWithLocation(ctx, authToken, projectName, "internal")
}

// GetPushRegistryCredentialsExternal fetches registry credentials for pushing to customer's AWS ECR
func (c *Client) GetPushRegistryCredentialsExternal(ctx context.Context, authToken string, projectName string) (*RegistryCredentials, error) {
	return c.getPushRegistryCredentialsWithLocation(ctx, authToken, projectName, "external")
}

func (c *Client) getPushRegistryCredentialsWithLocation(ctx context.Context, authToken string, projectName string, location string) (*RegistryCredentials, error) {
	payload := map[string]string{
		"name":     projectName,
		"location": location,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.Errorf("failed to marshal request payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/push-token", getBaseURL()), bytes.NewBuffer(payloadBytes))
	if err != nil {
		return nil, errors.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.Errorf("failed to make request to push-token endpoint: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, errors.Errorf("push-token endpoint returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var creds RegistryCredentials
	if err := json.NewDecoder(resp.Body).Decode(&creds); err != nil {
		return nil, errors.Errorf("failed to decode push-token response: %w", err)
	}

	if creds.Username == "" || creds.Token == "" || creds.URL == "" || creds.Repository == "" {
		return nil, errors.Errorf("incomplete credentials received: username=%s, token present=%t, url=%s, repository=%s",
			creds.Username, creds.Token != "", creds.URL, creds.Repository)
	}

	// Strip https:// prefix as Docker doesn't accept it in image references
	creds.URL = strings.TrimPrefix(creds.URL, "https://")
	creds.URL = strings.TrimPrefix(creds.URL, "http://")

	return &creds, nil
}

// GetPullRegistryCredentials fetches temporary Docker registry credentials for pulling images
// These are scoped to JUST being able to pull from registries for the specified tenant
func (c *Client) GetPullRegistryCredentials(ctx context.Context, authToken string, projectName string) (*RegistryCredentials, error) {
	return c.getPullRegistryCredentialsWithLocation(ctx, authToken, projectName, "internal")
}

// GetPullRegistryCredentialsExternal fetches registry credentials for pulling from customer's AWS ECR
func (c *Client) GetPullRegistryCredentialsExternal(ctx context.Context, authToken string, projectName string) (*RegistryCredentials, error) {
	return c.getPullRegistryCredentialsWithLocation(ctx, authToken, projectName, "external")
}

func (c *Client) getPullRegistryCredentialsWithLocation(ctx context.Context, authToken string, projectName string, location string) (*RegistryCredentials, error) {
	payload := map[string]string{
		"name":     projectName,
		"location": location,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.Errorf("failed to marshal request payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/pull-token", getBaseURL()), bytes.NewBuffer(payloadBytes))
	if err != nil {
		return nil, errors.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.Errorf("failed to make request to pull-token endpoint: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, errors.Errorf("pull-token endpoint returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var creds RegistryCredentials
	if err := json.NewDecoder(resp.Body).Decode(&creds); err != nil {
		return nil, errors.Errorf("failed to decode pull-token response: %w", err)
	}

	if creds.Username == "" || creds.Token == "" || creds.URL == "" || creds.Repository == "" {
		return nil, errors.Errorf("incomplete credentials received: username=%s, token present=%t, url=%s, repository=%s",
			creds.Username, creds.Token != "", creds.URL, creds.Repository)
	}

	// Strip https:// prefix as Docker doesn't accept it in image references
	creds.URL = strings.TrimPrefix(creds.URL, "https://")
	creds.URL = strings.TrimPrefix(creds.URL, "http://")

	return &creds, nil
}

// CreateDockerRepository creates a Docker repository for the project
func (c *Client) CreateDockerRepository(ctx context.Context, authToken string, projectName string) error {
	return c.createDockerRepositoryWithLocation(ctx, authToken, projectName, "internal")
}

// CreateDockerRepositoryExternal creates a Docker repository in customer's AWS ECR
func (c *Client) CreateDockerRepositoryExternal(ctx context.Context, authToken string, projectName string) error {
	return c.createDockerRepositoryWithLocation(ctx, authToken, projectName, "external")
}

func (c *Client) createDockerRepositoryWithLocation(ctx context.Context, authToken string, projectName string, location string) error {
	payload := map[string]string{
		"name":     projectName,
		"location": location,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return errors.Errorf("failed to marshal request payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/create-repo", getBaseURL()), bytes.NewBuffer(payloadBytes))
	if err != nil {
		return errors.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return errors.Errorf("failed to make request to create-repo endpoint: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return errors.Errorf("create-repo endpoint returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}

// GetBaseDockerImages fetches the base Docker images for different languages
func (c *Client) GetBaseDockerImages(ctx context.Context) (map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/base-images", getBaseURL()), nil)
	if err != nil {
		return nil, errors.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.Errorf("failed to make request to base-images endpoint: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, errors.Errorf("base-images endpoint returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, errors.Errorf("failed to decode base-images response: %w", err)
	}

	return result, nil
}
