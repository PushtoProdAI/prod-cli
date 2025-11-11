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

func getBaseURL() string {
	return fmt.Sprintf("%s/%s", config.GetSupabaseURL(), "functions/v1")
}

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

// LogDeploymentOperation logs a deployment operation to the audit system
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

// GetPushRegistryCredentials fetches temporary Docker registry credentials for pushing images. These are scoped to JUST being able to push to registries for the specified tenant
func (c *Client) GetPushRegistryCredentials(ctx context.Context, authToken string, projectName string) (*RegistryCredentials, error) {
	return c.getPushRegistryCredentialsWithLocation(ctx, authToken, projectName, "internal")
}

// GetPushRegistryCredentialsExternal fetches registry credentials for pushing to customer's AWS ECR
func (c *Client) GetPushRegistryCredentialsExternal(ctx context.Context, authToken string, projectName string) (*RegistryCredentials, error) {
	return c.getPushRegistryCredentialsWithLocation(ctx, authToken, projectName, "external")
}

func (c *Client) getPushRegistryCredentialsWithLocation(ctx context.Context, authToken string, projectName string, location string) (*RegistryCredentials, error) {
	// Prepare request payload
	payload := map[string]string{
		"name":     projectName,
		"location": location,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.Errorf("failed to marshal request payload: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/push-token", getBaseURL()), bytes.NewBuffer(payloadBytes))
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
	return c.getPullRegistryCredentialsWithLocation(ctx, authToken, projectName, "internal")
}

// GetPullRegistryCredentialsExternal fetches registry credentials for pulling from customer's AWS ECR
func (c *Client) GetPullRegistryCredentialsExternal(ctx context.Context, authToken string, projectName string) (*RegistryCredentials, error) {
	return c.getPullRegistryCredentialsWithLocation(ctx, authToken, projectName, "external")
}

func (c *Client) getPullRegistryCredentialsWithLocation(ctx context.Context, authToken string, projectName string, location string) (*RegistryCredentials, error) {
	// Prepare request payload
	payload := map[string]string{
		"name":     projectName,
		"location": location,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.Errorf("failed to marshal request payload: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/pull-token", getBaseURL()), bytes.NewBuffer(payloadBytes))
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
	return c.createDockerRepositoryWithLocation(ctx, authToken, projectName, "internal")
}

func (c *Client) CreateDockerRepositoryExternal(ctx context.Context, authToken string, projectName string) error {
	return c.createDockerRepositoryWithLocation(ctx, authToken, projectName, "external")
}

func (c *Client) createDockerRepositoryWithLocation(ctx context.Context, authToken string, projectName string, location string) error {
	data := map[string]any{
		"name":     projectName,
		"location": location,
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return errors.Errorf("failed to marshal repository name: %w", err)
	}

	url := fmt.Sprintf("%s/create-repo", getBaseURL())
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
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/base-images", getBaseURL()), nil)
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

func (c *Client) CheckAWSAuthentication(ctx context.Context, authToken string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/aws-auth", getBaseURL()), nil)
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

type AWSAuthSetup struct {
	ExternalID string `json:"external_id"`
	Region     string `json:"region"`
}

func (c *Client) InitializeAWSAuth(ctx context.Context, authToken string, region string) (*AWSAuthSetup, error) {
	payload := map[string]string{
		"action": "initialize",
		"region": region,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/aws-auth", getBaseURL()), bytes.NewBuffer(jsonData))
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

func (c *Client) CompleteAWSAuth(ctx context.Context, authToken string, roleArn string, region string) error {
	payload := map[string]string{
		"action":   "complete",
		"role_arn": roleArn,
		"region":   region,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return errors.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/aws-auth", getBaseURL()), bytes.NewBuffer(jsonData))
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

// BackingService represents a backing service (database, cache, etc.) for AWS deployment
type BackingService struct {
	Type             string `json:"type"`
	Name             string `json:"name"`
	Engine           string `json:"engine,omitempty"`
	InstanceClass    string `json:"instanceClass,omitempty"`
	AllocatedStorage int    `json:"allocatedStorage,omitempty"`
	NodeType         string `json:"nodeType,omitempty"`
	NumCacheNodes    int    `json:"numCacheNodes,omitempty"`
}

// EnvVar represents an environment variable with categorization
type EnvVar struct {
	Name              string `json:"name"`
	Value             string `json:"value,omitempty"`
	Role              string `json:"role,omitempty"`              // "full_uri", "hostname", "port", etc.
	Service           string `json:"service,omitempty"`           // "postgresql", "redis", etc.
	Sensitive         bool   `json:"sensitive,omitempty"`         // true if variable contains sensitive data
	SensitivityReason string `json:"sensitivityReason,omitempty"` // explanation for why variable is sensitive
}

// AWSDeploymentSpec represents the specification for deploying to AWS
type AWSDeploymentSpec struct {
	ServiceName      string           `json:"serviceName"`
	ImageURL         string           `json:"imageUrl"`
	CPU              string           `json:"cpu"`
	Memory           string           `json:"memory"`
	Port             int              `json:"port"`
	EnvVars          []EnvVar         `json:"envVars"`
	BackingServices  []BackingService `json:"backingServices,omitempty"`
	MigrationCommand string           `json:"migrationCommand,omitempty"`
	CreateAppRunner  *bool            `json:"createAppRunner,omitempty"` // If false, skip App Runner creation (for first deploy pre-migration)
}

// AWSDeploymentResult represents the result of an AWS deployment
type AWSDeploymentResult struct {
	StackID   string            `json:"stackId"`
	StackName string            `json:"stackName"`
	Status    string            `json:"status"`
	Outputs   map[string]string `json:"outputs,omitempty"`
	Error     string            `json:"error,omitempty"`
}

// DeployAWSStack deploys an application stack to AWS using CloudFormation
func (c *Client) DeployAWSStack(ctx context.Context, authToken string, spec AWSDeploymentSpec) (*AWSDeploymentResult, error) {
	// Increase timeout for CloudFormation operations (can take several minutes)
	client := &http.Client{
		Timeout: 15 * time.Minute,
	}

	jsonData, err := json.Marshal(spec)
	if err != nil {
		return nil, errors.Errorf("failed to marshal deployment spec: %w", err)
	}

	url := fmt.Sprintf("%s/deploy-aws-stack", getBaseURL())
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

	var result AWSDeploymentResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, errors.Errorf("failed to decode response: %w", err)
	}

	if result.Error != "" {
		return &result, errors.Errorf("AWS deployment failed: %s", result.Error)
	}

	return &result, nil
}

// GetAWSStackStatus polls the status of a CloudFormation stack
func (c *Client) GetAWSStackStatus(ctx context.Context, authToken string, stackName string) (*AWSDeploymentResult, error) {
	payload := map[string]any{
		"stackName": stackName,
		// Don't request resources for polling (faster)
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.Errorf("failed to marshal stack status request: %w", err)
	}

	url := fmt.Sprintf("%s/get-aws-stack-status", getBaseURL())
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

	var result AWSDeploymentResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, errors.Errorf("failed to decode response: %w", err)
	}

	// Check if stack doesn't exist (status will be NOT_FOUND)
	if result.Status == "NOT_FOUND" {
		return nil, errors.Errorf("CloudFormation stack not found: %s", stackName)
	}

	return &result, nil
}

// AWSStackCheckRequest represents a request to check if a CloudFormation stack exists
type AWSStackCheckRequest struct {
	StackName string `json:"stackName"`
}

// StackResourceInfo contains information about resources in the stack
type StackResourceInfo struct {
	HasRDS               bool     `json:"hasRDS"`
	HasElastiCache       bool     `json:"hasElastiCache"`
	HasAppRunner         bool     `json:"hasAppRunner"`
	RDSInstances         []string `json:"rdsInstances,omitempty"`
	ElastiCacheInstances []string `json:"elastiCacheInstances,omitempty"`
}

// AWSStackCheckResponse represents the response from checking if a stack exists
type AWSStackCheckResponse struct {
	Exists    bool              `json:"exists"`
	StackID   string            `json:"stackId,omitempty"`
	Status    string            `json:"status,omitempty"`
	Outputs   map[string]string `json:"outputs,omitempty"`
	Resources StackResourceInfo `json:"resources,omitempty"`
	Error     string            `json:"error,omitempty"`
}

// CheckAWSStack checks if a CloudFormation stack exists and returns its details
// This uses the same endpoint as GetAWSStackStatus but with includeResources=true
func (c *Client) CheckAWSStack(ctx context.Context, authToken string, stackName string) (*AWSStackCheckResponse, error) {
	payload := map[string]any{
		"stackName":        stackName,
		"includeResources": true, // Request resource information for detection
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.Errorf("failed to marshal check stack request: %w", err)
	}

	// Use the same consolidated endpoint
	url := fmt.Sprintf("%s/get-aws-stack-status", getBaseURL())
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

	var result AWSStackCheckResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, errors.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

// AppRunnerStatusResult represents the result of checking App Runner service status
type AppRunnerStatusResult struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// GetAppRunnerStatus polls the status of an App Runner service
func (c *Client) GetAppRunnerStatus(ctx context.Context, authToken string, serviceArn string) (*AppRunnerStatusResult, error) {
	payload := map[string]string{
		"serviceArn": serviceArn,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.Errorf("failed to marshal App Runner status request: %w", err)
	}

	url := fmt.Sprintf("%s/get-apprunner-status", getBaseURL())
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

	if resp.StatusCode == http.StatusNotFound {
		return nil, errors.Errorf("App Runner service not found: %s", serviceArn)
	}

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, errors.Errorf("get-apprunner-status failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result AppRunnerStatusResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, errors.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

// ECSMigrationRequest represents the request to run an ECS migration task
type ECSMigrationRequest struct {
	StackName         string   `json:"stackName"`
	ClusterArn        string   `json:"clusterArn"`
	TaskDefinitionArn string   `json:"taskDefinitionArn"`
	MigrationCommand  string   `json:"migrationCommand"`
	Subnets           []string `json:"subnets"`
	SecurityGroups    []string `json:"securityGroups"`
}

// ECSMigrationResult represents the result of running an ECS migration
type ECSMigrationResult struct {
	Success  bool     `json:"success"`
	ExitCode int      `json:"exitCode"`
	Logs     []string `json:"logs"`
	Error    string   `json:"error,omitempty"`
	TaskArn  string   `json:"taskArn,omitempty"`
}

// RunECSMigration runs a database migration as an ECS Fargate task
func (c *Client) RunECSMigration(ctx context.Context, authToken string, req ECSMigrationRequest) (*ECSMigrationResult, error) {
	// Use longer timeout for migrations (can take several minutes)
	client := &http.Client{
		Timeout: 15 * time.Minute,
	}

	jsonData, err := json.Marshal(req)
	if err != nil {
		return nil, errors.Errorf("failed to marshal migration request: %w", err)
	}

	url := fmt.Sprintf("%s/run-ecs-migration", getBaseURL())
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

	var result ECSMigrationResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, errors.Errorf("failed to decode response: %w", err)
	}

	if result.Error != "" {
		return &result, errors.Errorf("ECS migration failed: %s", result.Error)
	}

	return &result, nil
}

// AppRunnerServiceRequest represents the request to create an App Runner service
type AppRunnerServiceRequest struct {
	ServiceName string     `json:"serviceName"`
	ImageURL    string     `json:"imageUrl"`
	CPU         string     `json:"cpu"`
	Memory      string     `json:"memory"`
	Port        int        `json:"port"`
	EnvVars     []EnvVar   `json:"envVars"`
	VPCConfig   *VPCConfig `json:"vpcConfig,omitempty"`
	RoleArns    *RoleArns  `json:"roleArns,omitempty"`
}

// VPCConfig represents VPC configuration for App Runner
type VPCConfig struct {
	VpcId          string   `json:"vpcId"`
	Subnets        []string `json:"subnets"`
	SecurityGroups []string `json:"securityGroups"`
}

// RoleArns represents IAM role ARNs for App Runner
type RoleArns struct {
	AccessRoleArn   string `json:"accessRoleArn"`
	InstanceRoleArn string `json:"instanceRoleArn"`
}

// AppRunnerServiceResult represents the result of creating an App Runner service
type AppRunnerServiceResult struct {
	Success    bool   `json:"success"`
	ServiceArn string `json:"serviceArn"`
	ServiceURL string `json:"serviceUrl"`
	Error      string `json:"error,omitempty"`
}

// CreateAppRunnerService creates an AWS App Runner service
func (c *Client) CreateAppRunnerService(ctx context.Context, authToken string, req AppRunnerServiceRequest) (*AppRunnerServiceResult, error) {
	// Use longer timeout for App Runner service creation
	client := &http.Client{
		Timeout: 15 * time.Minute,
	}

	jsonData, err := json.Marshal(req)
	if err != nil {
		return nil, errors.Errorf("failed to marshal app runner request: %w", err)
	}

	url := fmt.Sprintf("%s/create-apprunner-service", getBaseURL())
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
		return nil, errors.Errorf("create-apprunner-service failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result AppRunnerServiceResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, errors.Errorf("failed to decode response: %w", err)
	}

	if result.Error != "" {
		return &result, errors.Errorf("App Runner service creation failed: %s", result.Error)
	}

	return &result, nil
}
