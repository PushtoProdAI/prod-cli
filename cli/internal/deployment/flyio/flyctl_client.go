package flyio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// FlyctlClient implements the FlyioClient interface using the flyctl CLI
// This allows us to swap implementations later without changing the interface
type FlyctlClient struct {
	// executor allows us to mock command execution for testing
	executor CommandExecutor
	// workDir is the directory where we'll write temporary files like fly.toml
	workDir string
}

// CommandExecutor interface allows us to mock exec.Command for testing
type CommandExecutor interface {
	Execute(ctx context.Context, name string, args ...string) ([]byte, error)
	ExecuteWithInput(ctx context.Context, input []byte, name string, args ...string) ([]byte, error)
	ExecuteInteractive(ctx context.Context, name string, args ...string) error
}

// DefaultCommandExecutor implements CommandExecutor using os/exec
type DefaultCommandExecutor struct{}

func (e *DefaultCommandExecutor) Execute(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	// Flyctl respects FLY_API_TOKEN environment variable
	// Make sure it's set if available
	if token := os.Getenv("FLY_API_TOKEN"); token != "" {
		cmd.Env = append(os.Environ(), fmt.Sprintf("FLY_API_TOKEN=%s", token))
	}
	return cmd.Output()
}

func (e *DefaultCommandExecutor) ExecuteWithInput(ctx context.Context, input []byte, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = bytes.NewReader(input)
	// Flyctl respects FLY_API_TOKEN environment variable
	if token := os.Getenv("FLY_API_TOKEN"); token != "" {
		cmd.Env = append(os.Environ(), fmt.Sprintf("FLY_API_TOKEN=%s", token))
	}
	return cmd.Output()
}

func (e *DefaultCommandExecutor) ExecuteInteractive(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Flyctl respects FLY_API_TOKEN environment variable
	if token := os.Getenv("FLY_API_TOKEN"); token != "" {
		cmd.Env = append(os.Environ(), fmt.Sprintf("FLY_API_TOKEN=%s", token))
	}
	return cmd.Run()
}

// NewFlyctlClient creates a new flyctl-based Fly.io client
func NewFlyctlClient() *FlyctlClient {
	return &FlyctlClient{
		executor: &DefaultCommandExecutor{},
		workDir:  os.TempDir(),
	}
}

// NewFlyctlClientWithExecutor creates a new client with a custom executor (for testing)
func NewFlyctlClientWithExecutor(executor CommandExecutor) *FlyctlClient {
	return &FlyctlClient{
		executor: executor,
		workDir:  os.TempDir(),
	}
}

// ensureFlyctl checks if flyctl is installed and returns an error if not
func (c *FlyctlClient) ensureFlyctl(ctx context.Context) error {
	_, err := c.executor.Execute(ctx, "flyctl", "version", "--json")
	if err != nil {
		return fmt.Errorf("flyctl is not installed or not in PATH. Please install it from https://fly.io/docs/flyctl/install/")
	}
	return nil
}

// CreateApp creates a new app on Fly.io
func (c *FlyctlClient) CreateApp(ctx context.Context, req CreateAppRequest) (*FlyioApp, error) {
	if err := c.ensureFlyctl(ctx); err != nil {
		return nil, err
	}

	args := []string{"apps", "create", req.Name}
	
	if req.OrgSlug != "" {
		args = append(args, "--org", req.OrgSlug)
	}
	
	// Use JSON output for structured parsing
	args = append(args, "--json")

	output, err := c.executor.Execute(ctx, "flyctl", args...)
	if err != nil {
		// Try to parse error from output
		var errorResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(output, &errorResp) == nil && errorResp.Error != "" {
			return nil, fmt.Errorf("failed to create app: %s", errorResp.Error)
		}
		return nil, fmt.Errorf("failed to create Fly.io app %q in region %q: %w", req.Name, req.Region, err)
	}

	// Parse the JSON response
	var app FlyioApp
	if err := json.Unmarshal(output, &app); err != nil {
		return nil, fmt.Errorf("failed to parse app response: %w", err)
	}

	return &app, nil
}

// GetApp retrieves app information
func (c *FlyctlClient) GetApp(ctx context.Context, appID string) (*FlyioApp, error) {
	// Check if flyctl is installed
	if err := c.ensureFlyctl(ctx); err != nil {
		return nil, err
	}
	
	output, err := c.executor.Execute(ctx, "flyctl", "apps", "show", appID, "--json")
	if err != nil {
		return nil, fmt.Errorf("failed to get Fly.io app %q: %w", appID, err)
	}

	var app FlyioApp
	if err := json.Unmarshal(output, &app); err != nil {
		return nil, fmt.Errorf("failed to parse app info: %w", err)
	}

	return &app, nil
}

// DeployApp deploys configuration to an app
func (c *FlyctlClient) DeployApp(ctx context.Context, appID string, config *FlyioConfig) error {
	// Check if flyctl is installed
	if err := c.ensureFlyctl(ctx); err != nil {
		return err
	}

	// Determine the source directory (default to current directory)
	sourceDir := "."
	if config.SourcePath != "" {
		sourceDir = config.SourcePath
	}

	// Check if source directory exists
	if _, err := os.Stat(sourceDir); os.IsNotExist(err) {
		return fmt.Errorf("source directory %s does not exist", sourceDir)
	}

	// Generate fly.toml from config
	flyToml, err := c.generateFlyToml(config)
	if err != nil {
		return fmt.Errorf("failed to generate fly.toml: %w", err)
	}

	// Write fly.toml to the source directory
	flyTomlPath := filepath.Join(sourceDir, "fly.toml")
	
	// Back up existing fly.toml if it exists
	backupPath := ""
	if _, err := os.Stat(flyTomlPath); err == nil {
		backupPath = flyTomlPath + ".backup"
		if err := os.Rename(flyTomlPath, backupPath); err != nil {
			return fmt.Errorf("failed to backup existing fly.toml: %w", err)
		}
		defer func() {
			// Restore backup after deployment
			if backupPath != "" {
				os.Rename(backupPath, flyTomlPath)
			}
		}()
	}
	
	if err := os.WriteFile(flyTomlPath, []byte(flyToml), 0644); err != nil {
		return fmt.Errorf("failed to write fly.toml: %w", err)
	}
	defer func() {
		// Clean up generated fly.toml if we didn't have one before
		if backupPath == "" {
			os.Remove(flyTomlPath)
		}
	}()

	// Deploy using flyctl from the source directory
	args := []string{
		"deploy",
		"--app", appID,
		"--yes", // Auto-confirm
	}

	// Execute from the source directory
	cmd := exec.CommandContext(ctx, "flyctl", args...)
	cmd.Dir = sourceDir
	cmd.Env = os.Environ()
	
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Try to parse error from JSON output
		var deployResp struct {
			Error string `json:"error"`
			Status string `json:"status"`
		}
		if json.Unmarshal(output, &deployResp) == nil && deployResp.Error != "" {
			return fmt.Errorf("deployment failed: %s", deployResp.Error)
		}
		return fmt.Errorf("deployment failed: %w", err)
	}

	// Parse deployment response to verify success
	var deployResp struct {
		Status string `json:"status"`
		URL    string `json:"url"`
	}
	if err := json.Unmarshal(output, &deployResp); err == nil {
		if deployResp.Status != "success" && deployResp.Status != "deployed" {
			return fmt.Errorf("deployment status: %s", deployResp.Status)
		}
	}

	return nil
}

// DestroyApp destroys an app
func (c *FlyctlClient) DestroyApp(ctx context.Context, appID string) error {
	// Check if flyctl is installed
	if err := c.ensureFlyctl(ctx); err != nil {
		return err
	}
	// Use --yes to skip confirmation
	_, err := c.executor.Execute(ctx, "flyctl", "apps", "destroy", appID, "--yes")
	if err != nil {
		return fmt.Errorf("failed to destroy Fly.io app %q: %w", appID, err)
	}
	return nil
}

// CreatePostgres creates a new PostgreSQL database
func (c *FlyctlClient) CreatePostgres(ctx context.Context, req CreatePostgresRequest) (*FlyioPostgres, error) {
	// Check if flyctl is installed
	if err := c.ensureFlyctl(ctx); err != nil {
		return nil, err
	}
	args := []string{
		"postgres", "create",
		"--name", req.Name,
		"--region", req.Region,
		"--initial-cluster-size", "1", // Start with single node
		"--vm-size", "shared-cpu-1x",
		"--volume-size", fmt.Sprintf("%d", req.Size),
		"--json",
	}

	output, err := c.executor.Execute(ctx, "flyctl", args...)
	if err != nil {
		return nil, fmt.Errorf("failed to create PostgreSQL database %q in region %q: %w", req.Name, req.Region, err)
	}

	var postgres FlyioPostgres
	if err := json.Unmarshal(output, &postgres); err != nil {
		return nil, fmt.Errorf("failed to parse postgres response: %w", err)
	}

	return &postgres, nil
}

// CreateRedis creates a new Redis database
func (c *FlyctlClient) CreateRedis(ctx context.Context, req CreateRedisRequest) (*FlyioRedis, error) {
	// Check if flyctl is installed
	if err := c.ensureFlyctl(ctx); err != nil {
		return nil, err
	}
	// Note: Fly.io uses Upstash Redis, which is created differently
	args := []string{
		"redis", "create",
		"--name", req.Name,
		"--region", req.Region,
		"--no-replicas", // Start with no replicas
		"--json",
	}

	output, err := c.executor.Execute(ctx, "flyctl", args...)
	if err != nil {
		return nil, fmt.Errorf("failed to create Redis database %q in region %q: %w", req.Name, req.Region, err)
	}

	var redis FlyioRedis
	if err := json.Unmarshal(output, &redis); err != nil {
		return nil, fmt.Errorf("failed to parse redis response: %w", err)
	}

	return &redis, nil
}

// GetPostgresConnectionInfo retrieves PostgreSQL connection information
func (c *FlyctlClient) GetPostgresConnectionInfo(ctx context.Context, appID string) (*PostgresConnectionInfo, error) {
	// Check if flyctl is installed
	if err := c.ensureFlyctl(ctx); err != nil {
		return nil, err
	}
	// Use flyctl postgres db list to get connection info
	output, err := c.executor.Execute(ctx, "flyctl", "postgres", "db", "list", "--app", appID, "--json")
	if err != nil {
		return nil, fmt.Errorf("failed to get postgres connection info: %w", err)
	}

	// Parse the response to extract connection strings
	var dbList []struct {
		Name string `json:"name"`
		Users []struct {
			Username string `json:"username"`
			Password string `json:"password"`
		} `json:"users"`
	}

	if err := json.Unmarshal(output, &dbList); err != nil {
		return nil, fmt.Errorf("failed to parse database list: %w", err)
	}

	if len(dbList) == 0 {
		return nil, fmt.Errorf("no databases found")
	}

	// Construct connection strings
	// Internal uses .internal domain, external uses .fly.dev
	db := dbList[0]
	var username, password string
	if len(db.Users) > 0 {
		username = db.Users[0].Username
		password = db.Users[0].Password
	}

	return &PostgresConnectionInfo{
		InternalConnectionString: fmt.Sprintf("postgres://%s:%s@%s.internal:5432/%s", 
			username, password, appID, db.Name),
		ExternalConnectionString: fmt.Sprintf("postgres://%s:%s@%s.fly.dev:5432/%s", 
			username, password, appID, db.Name),
	}, nil
}

// GetRedisConnectionInfo retrieves Redis connection information
func (c *FlyctlClient) GetRedisConnectionInfo(ctx context.Context, appID string) (*RedisConnectionInfo, error) {
	// Check if flyctl is installed
	if err := c.ensureFlyctl(ctx); err != nil {
		return nil, err
	}
	// Get Redis connection info
	output, err := c.executor.Execute(ctx, "flyctl", "redis", "status", appID, "--json")
	if err != nil {
		return nil, fmt.Errorf("failed to get redis connection info: %w", err)
	}

	var status struct {
		ConnectionString string `json:"connection_string"`
	}

	if err := json.Unmarshal(output, &status); err != nil {
		return nil, fmt.Errorf("failed to parse redis status: %w", err)
	}

	// For Redis, internal and external are typically the same
	return &RedisConnectionInfo{
		InternalConnectionString: status.ConnectionString,
		ExternalConnectionString: status.ConnectionString,
	}, nil
}

// AttachPostgres attaches a PostgreSQL database to an app
func (c *FlyctlClient) AttachPostgres(ctx context.Context, req AttachPostgresRequest) error {
	// Check if flyctl is installed
	if err := c.ensureFlyctl(ctx); err != nil {
		return err
	}
	args := []string{
		"postgres", "attach",
		req.PostgresName,
		"--app", req.AppName,
	}
	
	// Add optional database name
	if req.DatabaseName != "" {
		args = append(args, "--database-name", req.DatabaseName)
	}
	
	// Add variable name (default is DATABASE_URL)
	if req.VariableName != "" {
		args = append(args, "--variable-name", req.VariableName)
	} else {
		args = append(args, "--variable-name", "DATABASE_URL")
	}
	
	// Auto-confirm the attachment
	args = append(args, "-y")
	
	_, err := c.executor.Execute(ctx, "flyctl", args...)
	if err != nil {
		return fmt.Errorf("failed to attach PostgreSQL %q to app %q: %w", req.PostgresName, req.AppName, err)
	}
	
	return nil
}

// AttachRedis attaches a Redis database to an app
func (c *FlyctlClient) AttachRedis(ctx context.Context, req AttachRedisRequest) error {
	// Check if flyctl is installed
	if err := c.ensureFlyctl(ctx); err != nil {
		return err
	}
	// Note: Fly.io uses Upstash Redis which has a different attachment process
	// The redis attach command sets up the connection automatically
	args := []string{
		"redis", "attach",
		req.RedisName,
		"--app", req.AppName,
	}
	
	// Add variable name (default is REDIS_URL)
	if req.VariableName != "" {
		args = append(args, "--variable-name", req.VariableName)
	} else {
		args = append(args, "--variable-name", "REDIS_URL")
	}
	
	// Auto-confirm the attachment
	args = append(args, "-y")
	
	_, err := c.executor.Execute(ctx, "flyctl", args...)
	if err != nil {
		return fmt.Errorf("failed to attach Redis %q to app %q: %w", req.RedisName, req.AppName, err)
	}
	
	return nil
}

// CreateVolume creates a new volume
func (c *FlyctlClient) CreateVolume(ctx context.Context, req CreateVolumeRequest) (*FlyioVolume, error) {
	// Check if flyctl is installed
	if err := c.ensureFlyctl(ctx); err != nil {
		return nil, err
	}
	args := []string{
		"volumes", "create",
		req.Name,
		"--app", req.AppID,
		"--region", req.Region,
		"--size", fmt.Sprintf("%d", req.Size),
		"--json",
	}

	output, err := c.executor.Execute(ctx, "flyctl", args...)
	if err != nil {
		return nil, fmt.Errorf("failed to create volume: %w", err)
	}

	var volume FlyioVolume
	if err := json.Unmarshal(output, &volume); err != nil {
		return nil, fmt.Errorf("failed to parse volume response: %w", err)
	}

	return &volume, nil
}

// GetAppLogs retrieves app logs
func (c *FlyctlClient) GetAppLogs(ctx context.Context, appID string) ([]LogEntry, error) {
	// Check if flyctl is installed
	if err := c.ensureFlyctl(ctx); err != nil {
		return nil, err
	}
	// Get recent logs in JSON format
	output, err := c.executor.Execute(ctx, "flyctl", "logs", "--app", appID, "--json", "--limit", "100")
	if err != nil {
		return nil, fmt.Errorf("failed to get app logs: %w", err)
	}

	// Parse JSON lines (flyctl outputs newline-delimited JSON)
	var logs []LogEntry
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		var entry LogEntry
		if err := json.Unmarshal([]byte(line), &entry); err == nil {
			logs = append(logs, entry)
		}
	}

	return logs, nil
}

// GetAppMetrics retrieves app metrics
func (c *FlyctlClient) GetAppMetrics(ctx context.Context, appID string) (*AppMetrics, error) {
	// Check if flyctl is installed
	if err := c.ensureFlyctl(ctx); err != nil {
		return nil, err
	}
	// Note: flyctl doesn't have a direct metrics command with JSON output
	// We can use status to get basic metrics
	output, err := c.executor.Execute(ctx, "flyctl", "status", "--app", appID, "--json")
	if err != nil {
		return nil, fmt.Errorf("failed to get app metrics: %w", err)
	}

	var status struct {
		Allocations []struct {
			ID        string `json:"id"`
			Status    string `json:"status"`
			CPU       float64 `json:"cpu"`
			Memory    float64 `json:"memory"`
		} `json:"allocations"`
	}

	if err := json.Unmarshal(output, &status); err != nil {
		return nil, fmt.Errorf("failed to parse status: %w", err)
	}

	// Aggregate metrics from all allocations
	var totalCPU, totalMemory float64
	for _, alloc := range status.Allocations {
		totalCPU += alloc.CPU
		totalMemory += alloc.Memory
	}

	return &AppMetrics{
		CPU:    totalCPU,
		Memory: totalMemory,
		Network: NetworkMetrics{
			BytesIn:  0, // Not available via flyctl
			BytesOut: 0, // Not available via flyctl
		},
	}, nil
}

// generateFlyToml generates a fly.toml configuration from our config struct
func (c *FlyctlClient) generateFlyToml(config *FlyioConfig) (string, error) {
	// Use a simple template approach for now
	// In the future, we could use the template generator in templates/fly_toml.go
	
	var builder strings.Builder
	
	// App name
	builder.WriteString(fmt.Sprintf("app = \"%s\"\n", config.AppName))
	builder.WriteString("primary_region = \"iad\"\n\n")
	
	// Build configuration
	if config.BuildConfig != nil {
		builder.WriteString("[build]\n")
		if config.BuildConfig.Dockerfile != "" {
			builder.WriteString("  dockerfile = \"Dockerfile\"\n")
		} else if config.BuildConfig.Builder != "" {
			builder.WriteString(fmt.Sprintf("  builder = \"%s\"\n", config.BuildConfig.Builder))
		}
		if config.BuildConfig.BuildCmd != "" {
			builder.WriteString(fmt.Sprintf("  build_cmd = \"%s\"\n", config.BuildConfig.BuildCmd))
		}
		builder.WriteString("\n")
	}
	
	// Environment variables
	if len(config.EnvVars) > 0 {
		builder.WriteString("[env]\n")
		for key, value := range config.EnvVars {
			builder.WriteString(fmt.Sprintf("  %s = \"%s\"\n", key, value))
		}
		builder.WriteString("\n")
	}
	
	// Services (HTTP endpoints)
	if len(config.Services) > 0 {
		for _, service := range config.Services {
			builder.WriteString("[[services]]\n")
			builder.WriteString(fmt.Sprintf("  protocol = \"%s\"\n", service.Protocol))
			builder.WriteString(fmt.Sprintf("  internal_port = %d\n", service.InternalPort))
			
			for _, port := range service.Ports {
				builder.WriteString("  [[services.ports]]\n")
				builder.WriteString(fmt.Sprintf("    port = %d\n", port.Port))
				if len(port.Handlers) > 0 {
					builder.WriteString("    handlers = [")
					for i, handler := range port.Handlers {
						if i > 0 {
							builder.WriteString(", ")
						}
						builder.WriteString(fmt.Sprintf("\"%s\"", handler))
					}
					builder.WriteString("]\n")
				}
			}
		}
		builder.WriteString("\n")
	}
	
	// Volumes
	if len(config.Volumes) > 0 {
		for _, volume := range config.Volumes {
			builder.WriteString("[[mounts]]\n")
			builder.WriteString(fmt.Sprintf("  source = \"%s\"\n", volume.Name))
			builder.WriteString("  destination = \"/data\"\n\n")
		}
	}
	
	return builder.String(), nil
}