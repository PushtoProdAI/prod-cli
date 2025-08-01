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
)

const (
	baseURL = "http://127.0.0.1:54321/functions/v1"
)

type RegistryCredentials struct {
	Username   string `json:"dockerAuthUsername"`
	Token      string `json:"dockerAuthToken"`
	URL        string `json:"proxyEndpoint"`
	Repository string `json:"dockerRepo"`
	ExpiresAt  string `json:"expiresAt"`
	AccountID  string `json:"accountId"`
}

// TODO: can refactor this to where thhere is a struct that can hold state like the auth token for the edge functions

func RecordRequestedStack(ctx context.Context, platform string, language string, serviceRequirements []analyzer.ServiceRequirement) error {
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

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return errors.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return errors.Errorf("request failed with status: %d", resp.StatusCode)
	}

	return nil
}

func GetPushRegistryCredentials(ctx context.Context, tenantID string) (*RegistryCredentials, error) {
	// Prepare request payload
	payload := map[string]string{
		"tenantId": tenantID,
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

	// Make HTTP request
	client := &http.Client{}
	resp, err := client.Do(req)
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

func GetPullRegistryCredentials(ctx context.Context, tenantID string) (*RegistryCredentials, error) {
	// Prepare request payload
	payload := map[string]string{
		"tenantId": tenantID,
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

	// Make HTTP request
	client := &http.Client{}
	resp, err := client.Do(req)
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
