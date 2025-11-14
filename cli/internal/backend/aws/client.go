package aws

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/go-errors/errors"
)

// Client provides AWS-specific backend operations
type Client struct {
	httpClient *http.Client
	baseURL    string
}

// NewClient creates a new AWS backend client
func NewClient(baseURL string) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		baseURL: baseURL,
	}
}

// CheckAuthentication checks if AWS authentication is set up for the user
func (c *Client) CheckAuthentication(ctx context.Context, authToken string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/aws-auth", c.baseURL), nil)
	if err != nil {
		return false, errors.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, errors.Errorf("failed to make request to aws-auth endpoint: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return false, errors.Errorf("unauthorized: invalid or missing auth token")
	}

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return false, errors.Errorf("aws-auth endpoint returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result struct {
		Authenticated bool   `json:"authenticated"`
		Error         string `json:"error,omitempty"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, errors.Errorf("failed to decode aws-auth response: %w", err)
	}

	if result.Error != "" {
		return false, errors.Errorf("aws-auth returned error: %s", result.Error)
	}

	return result.Authenticated, nil
}

// InitializeAuth initializes AWS authentication by creating IAM resources
func (c *Client) InitializeAuth(ctx context.Context, authToken string, region string) (*AWSAuthSetup, error) {
	payload := map[string]string{
		"action": "initialize",
		"region": region,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/aws-auth", c.baseURL), bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, errors.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, errors.Errorf("initialize aws-auth failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var setup AWSAuthSetup
	if err := json.NewDecoder(resp.Body).Decode(&setup); err != nil {
		return nil, errors.Errorf("failed to decode response: %w", err)
	}

	return &setup, nil
}

// CompleteAuth completes AWS authentication by storing customer's role ARN
func (c *Client) CompleteAuth(ctx context.Context, authToken string, roleArn string, region string) error {
	payload := map[string]string{
		"action":   "complete",
		"role_arn": roleArn,
		"region":   region,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return errors.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/aws-auth", c.baseURL), bytes.NewBuffer(jsonData))
	if err != nil {
		return errors.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return errors.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return errors.Errorf("complete aws-auth failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result struct {
		Success bool   `json:"success"`
		Error   string `json:"error,omitempty"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return errors.Errorf("failed to decode response: %w", err)
	}

	if result.Error != "" {
		return errors.Errorf("aws-auth returned error: %s", result.Error)
	}

	if !result.Success {
		return errors.Errorf("failed to complete AWS authentication setup")
	}

	return nil
}

// DeployStack deploys an application stack to AWS using CloudFormation
func (c *Client) DeployStack(ctx context.Context, authToken string, spec DeploymentSpec) (*DeploymentResult, error) {
	// Increase timeout for CloudFormation operations (can take several minutes)
	client := &http.Client{
		Timeout: 15 * time.Minute,
	}

	jsonData, err := json.Marshal(spec)
	if err != nil {
		return nil, errors.Errorf("failed to marshal deployment spec: %w", err)
	}

	url := fmt.Sprintf("%s/deploy-aws-stack", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, errors.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, errors.Errorf("deploy-aws-stack failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result DeploymentResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, errors.Errorf("failed to decode response: %w", err)
	}

	if result.Error != "" {
		return &result, errors.Errorf("AWS deployment failed: %s", result.Error)
	}

	return &result, nil
}

// PreviewCloudFormationTemplate generates a CloudFormation template without deploying
func (c *Client) PreviewCloudFormationTemplate(ctx context.Context, authToken string, spec AWSDeploymentSpec) (*TemplatePreviewResponse, error) {
	jsonData, err := json.Marshal(spec)
	if err != nil {
		return nil, errors.Errorf("failed to marshal preview request: %w", err)
	}

	url := fmt.Sprintf("%s/preview-aws-template", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
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
		return nil, errors.Errorf("preview-aws-template failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result TemplatePreviewResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, errors.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

// CheckStack checks if a CloudFormation stack exists and returns its details
// This uses the same endpoint as GetStackStatus but with includeResources=true
func (c *Client) CheckStack(ctx context.Context, authToken string, stackName string) (*StackCheckResponse, error) {
	payload := map[string]any{
		"stackName":        stackName,
		"includeResources": true, // Request resource information for detection
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.Errorf("failed to marshal check stack request: %w", err)
	}

	// Use the same consolidated endpoint
	url := fmt.Sprintf("%s/get-aws-stack-status", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
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
		return nil, errors.Errorf("get-aws-stack-status failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result StackCheckResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, errors.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

// RunMigration runs an ECS Fargate task to execute database migrations
func (c *Client) RunMigration(ctx context.Context, authToken string, req MigrationRequest) (*MigrationResult, error) {
	// Use longer timeout for migrations (can take several minutes)
	client := &http.Client{
		Timeout: 15 * time.Minute,
	}

	jsonData, err := json.Marshal(req)
	if err != nil {
		return nil, errors.Errorf("failed to marshal migration request: %w", err)
	}

	url := fmt.Sprintf("%s/run-ecs-migration", c.baseURL)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, errors.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if authToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+authToken)
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, errors.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, errors.Errorf("run-ecs-migration failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result MigrationResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, errors.Errorf("failed to decode response: %w", err)
	}

	if result.Error != "" {
		return &result, errors.Errorf("ECS migration failed: %s", result.Error)
	}

	return &result, nil
}


// GetStackStatus polls the status of a CloudFormation stack
func (c *Client) GetStackStatus(ctx context.Context, authToken string, stackName string) (*DeploymentResult, error) {
	payload := map[string]any{
		"stackName": stackName,
		// Don't request resources for polling (faster)
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.Errorf("failed to marshal stack status request: %w", err)
	}

	url := fmt.Sprintf("%s/get-aws-stack-status", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
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
		return nil, errors.Errorf("get-aws-stack-status failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result DeploymentResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, errors.Errorf("failed to decode response: %w", err)
	}

	// Check if stack doesn't exist (status will be NOT_FOUND)
	if result.Status == "NOT_FOUND" {
		return nil, errors.Errorf("CloudFormation stack not found: %s", stackName)
	}

	return &result, nil
}
