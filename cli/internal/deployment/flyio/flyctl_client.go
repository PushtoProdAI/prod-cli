package flyio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/go-errors/errors"
)

// FlyctlClient implements the FlyioClient interface using the flyctl CLI
// This allows us to swap implementations later without changing the interface
type FlyctlClient struct {
	// executor allows us to mock command execution for testing
	executor CommandExecutor
	// workDir is the directory where we'll write temporary files like fly.toml
	workDir string
	// writer for streaming command output
	writer io.Writer
}

// CommandExecutor interface allows us to mock exec.Command for testing
type CommandExecutor interface {
	Execute(ctx context.Context, name string, args ...string) ([]byte, error)
	ExecuteWithInput(ctx context.Context, input []byte, name string, args ...string) ([]byte, error)
	ExecuteInteractive(ctx context.Context, writer io.Writer, name string, args ...string) error
	ExecuteWithStreaming(ctx context.Context, writer io.Writer, name string, args ...string) ([]byte, error)
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
	op, err := cmd.CombinedOutput()
	if err != nil {
		return nil, err
	}
	msg := string(op)
	// even when returning JSON, there is some time plain text "Warning" attached to the message
	msg, _, _ = strings.Cut(msg, "Warning")
	msg = strings.TrimSpace(msg)
	return []byte(msg), err
}

func (e *DefaultCommandExecutor) ExecuteWithInput(ctx context.Context, input []byte, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = bytes.NewReader(input)
	// Flyctl respects FLY_API_TOKEN environment variable
	if token := os.Getenv("FLY_API_TOKEN"); token != "" {
		cmd.Env = append(os.Environ(), fmt.Sprintf("FLY_API_TOKEN=%s", token))
	}
	return cmd.CombinedOutput()
}

func (e *DefaultCommandExecutor) ExecuteInteractive(ctx context.Context, writer io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = writer
	cmd.Stderr = writer
	// Flyctl respects FLY_API_TOKEN environment variable
	if token := os.Getenv("FLY_API_TOKEN"); token != "" {
		cmd.Env = append(os.Environ(), fmt.Sprintf("FLY_API_TOKEN=%s", token))
	}
	return cmd.Run()
}

func (e *DefaultCommandExecutor) ExecuteWithStreaming(ctx context.Context, writer io.Writer, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	// Flyctl respects FLY_API_TOKEN environment variable
	if token := os.Getenv("FLY_API_TOKEN"); token != "" {
		cmd.Env = append(os.Environ(), fmt.Sprintf("FLY_API_TOKEN=%s", token))
	}

	// Capture output while also streaming to writer
	var stdout, stderr bytes.Buffer
	cmd.Stdout = io.MultiWriter(writer, &stdout)
	cmd.Stderr = io.MultiWriter(writer, &stderr)

	err := cmd.Run()
	if err != nil {
		return nil, err
	}

	// Return combined output for parsing
	combined := stdout.String()
	if stderr.Len() > 0 {
		combined += "\n" + stderr.String()
	}

	// Strip warning messages (same as Execute method)
	combined, _, _ = strings.Cut(combined, "Warning")
	combined = strings.TrimSpace(combined)

	return []byte(combined), nil
}

// NewFlyctlClient creates a new flyctl-based Fly.io client
func NewFlyctlClient(writer io.Writer) *FlyctlClient {
	if writer == nil {
		writer = os.Stdout
	}
	return &FlyctlClient{
		executor: &DefaultCommandExecutor{},
		workDir:  os.TempDir(),
		writer:   writer,
	}
}

// NewFlyctlClientWithExecutor creates a new client with a custom executor (for testing)
func NewFlyctlClientWithExecutor(executor CommandExecutor, writer io.Writer) *FlyctlClient {
	if writer == nil {
		writer = os.Stdout
	}
	return &FlyctlClient{
		executor: executor,
		workDir:  os.TempDir(),
		writer:   writer,
	}
}

// ensureFlyctl checks if flyctl is installed and returns an error if not
func (c *FlyctlClient) ensureFlyctl(ctx context.Context) error {
	_, err := c.executor.Execute(ctx, "flyctl", "version", "--json")
	if err != nil {
		return errors.Errorf("flyctl is not installed or not in PATH. Please install it from https://fly.io/docs/flyctl/install/")
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
			return nil, errors.Errorf("failed to create app: %s", errorResp.Error)
		}
		return nil, errors.Errorf("failed to create Fly.io app %q in region %q: %s", req.Name, req.Region, string(output))
	}

	// Parse the JSON response
	var app FlyioApp
	if err := json.Unmarshal(output, &app); err != nil {
		return nil, errors.Errorf("failed to parse app response: %w", err)
	}

	return &app, nil
}

// GetApp retrieves app information
func (c *FlyctlClient) GetApp(ctx context.Context, appID string) (*FlyioApp, error) {
	// Check if flyctl is installed
	if err := c.ensureFlyctl(ctx); err != nil {
		return nil, err
	}

	output, err := c.executor.Execute(ctx, "flyctl", "apps", "list", "--json")
	if err != nil {
		return nil, errors.Errorf("failed to get Fly.io app %q: %w", appID, err)
	}

	var apps []FlyioApp
	if err := json.Unmarshal(output, &apps); err != nil {
		return nil, errors.Errorf("failed to parse app info: %w", err)
	}

	var app FlyioApp
	for _, a := range apps {
		// Match by ID or Name (for compatibility)
		if a.ID == appID || a.Name == appID {
			app = a
			break
		}
	}

	if app.ID == "" {
		return nil, errors.Errorf("app %q not found", appID)
	}

	if app.Hostname != "" {
		// the hostname comes back with no scheme
		app.Hostname = "https://" + app.Hostname
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
		return errors.Errorf("source directory %s does not exist", sourceDir)
	}

	// Generate fly.toml from config
	flyToml, err := c.generateFlyToml(config)
	if err != nil {
		return errors.Errorf("failed to generate fly.toml: %w", err)
	}

	// Write fly.toml to the source directory
	flyTomlPath := filepath.Join(sourceDir, "fly.toml")

	// Back up existing fly.toml if it exists
	backupPath := ""
	if _, err := os.Stat(flyTomlPath); err == nil {
		backupPath = flyTomlPath + ".backup"
		if err := os.Rename(flyTomlPath, backupPath); err != nil {
			return errors.Errorf("failed to backup existing fly.toml: %w", err)
		}
		defer func() {
			// Restore backup after deployment
			if backupPath != "" {
				os.Rename(backupPath, flyTomlPath)
			}
		}()
	}

	if err := os.WriteFile(flyTomlPath, []byte(flyToml), 0644); err != nil {
		return errors.Errorf("failed to write fly.toml: %w", err)
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

	// Execute from the source directory with streaming output
	cmd := exec.CommandContext(ctx, "flyctl", args...)
	cmd.Dir = sourceDir
	cmd.Env = os.Environ()

	// Stream output to writer so user can see progress
	cmd.Stdout = c.writer
	cmd.Stderr = c.writer

	if err := cmd.Run(); err != nil {
		return errors.Errorf("deployment failed: %w", err)
	}

	slog.Info("App deployed successfully")
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
		return errors.Errorf("failed to destroy Fly.io app %q: %w", appID, err)
	}
	return nil
}

// ListPostgres lists all managed PostgreSQL clusters
func (c *FlyctlClient) ListPostgres(ctx context.Context) ([]FlyioPostgresCluster, error) {
	if err := c.ensureFlyctl(ctx); err != nil {
		return nil, err
	}

	slog.Info("Listing Fly.io apps to get organization")
	output, err := c.executor.Execute(ctx, "flyctl", "apps", "list", "--json")
	if err != nil {
		return nil, errors.Errorf("failed to list apps to get organization: %w", err)
	}

	var apps []FlyioApp
	if err := json.Unmarshal(output, &apps); err != nil {
		return nil, errors.Errorf("failed to parse apps list: %w", err)
	}

	slog.Info("Found apps", "count", len(apps))
	if len(apps) == 0 {
		slog.Warn("No apps found, cannot determine organization for postgres list")
		return []FlyioPostgresCluster{}, nil
	}

	orgSlug := apps[0].Organization.Slug
	slog.Info("Using organization", "slug", orgSlug)
	if orgSlug == "" {
		slog.Warn("Organization slug is empty")
		return []FlyioPostgresCluster{}, nil
	}

	slog.Info("Listing managed postgres clusters", "org", orgSlug)
	pgOutput, err := c.executor.Execute(ctx, "flyctl", "mpg", "list", "-o", orgSlug, "--json")
	if err != nil {
		return nil, errors.Errorf("failed to list postgres clusters: %w", err)
	}

	slog.Debug("Postgres list output", "output", string(pgOutput))

	var clusters []FlyioPostgresCluster
	if err := json.Unmarshal(pgOutput, &clusters); err != nil {
		return nil, errors.Errorf("failed to parse postgres list: %w", err)
	}

	slog.Info("Found postgres clusters", "count", len(clusters))
	for _, cluster := range clusters {
		slog.Info("Postgres cluster", "name", cluster.Name, "id", cluster.ID, "status", cluster.Status)
	}

	return clusters, nil
}

// CreatePostgres creates a new managed PostgreSQL cluster
func (c *FlyctlClient) CreatePostgres(ctx context.Context, req CreatePostgresRequest) (*FlyioPostgresCluster, error) {
	// Check if flyctl is installed
	if err := c.ensureFlyctl(ctx); err != nil {
		return nil, err
	}

	args := []string{
		"mpg", "create",
		"--name", req.Name,
		"--region", req.Region,
		"--volume-size", fmt.Sprintf("%d", req.VolumeSize),
	}

	// Add plan if specified, otherwise default to basic
	if req.Plan != "" {
		args = append(args, "--plan", req.Plan)
	} else {
		args = append(args, "--plan", "basic")
	}

	// Execute with streaming output (this will block until provisioned)
	// Use ExecuteWithStreaming to show progress while capturing output for parsing
	output, err := c.executor.ExecuteWithStreaming(ctx, c.writer, "flyctl", args...)
	if err != nil {
		return nil, errors.Errorf("failed to create PostgreSQL cluster %q in region %q: %w", req.Name, req.Region, err)
	}

	// Parse the output to extract cluster information
	cluster, err := c.parseMPGCreateOutput(string(output))
	if err != nil {
		return nil, errors.Errorf("failed to parse cluster creation output: %w", err)
	}

	return cluster, nil
}

// parseMPGCreateOutput parses the mpg create command output
func (c *FlyctlClient) parseMPGCreateOutput(output string) (*FlyioPostgresCluster, error) {
	cluster := &FlyioPostgresCluster{}

	// Parse the final success output
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Extract ID: q49ypo4wg5qr17ln
		if strings.HasPrefix(line, "ID:") {
			cluster.ID = strings.TrimSpace(strings.TrimPrefix(line, "ID:"))
		}
		// Extract Name: foo
		if strings.HasPrefix(line, "Name:") {
			cluster.Name = strings.TrimSpace(strings.TrimPrefix(line, "Name:"))
		}
		// Extract Organization: james-martinez-457
		if strings.HasPrefix(line, "Organization:") {
			cluster.Organization.Slug = strings.TrimSpace(strings.TrimPrefix(line, "Organization:"))
		}
		// Extract Region: iad
		if strings.HasPrefix(line, "Region:") {
			cluster.Region = strings.TrimSpace(strings.TrimPrefix(line, "Region:"))
		}
		// Extract Plan: basic
		if strings.HasPrefix(line, "Plan:") {
			cluster.Plan = strings.TrimSpace(strings.TrimPrefix(line, "Plan:"))
		}
		// Extract Disk: 10GB
		if strings.HasPrefix(line, "Disk:") {
			cluster.DiskSize = strings.TrimSpace(strings.TrimPrefix(line, "Disk:"))
		}
		// Extract PostGIS: false
		if strings.HasPrefix(line, "PostGIS:") {
			postgisStr := strings.TrimSpace(strings.TrimPrefix(line, "PostGIS:"))
			cluster.PostGIS = postgisStr == "true"
		}
		// Extract Connection string: postgresql://...
		if strings.HasPrefix(line, "Connection string:") {
			cluster.ConnectionString = strings.TrimSpace(strings.TrimPrefix(line, "Connection string:"))
		}
	}

	// Also check for cluster ID in the waiting message
	// "Waiting for cluster foo (q49ypo4wg5qr17ln) to be ready..."
	if cluster.ID == "" {
		re := regexp.MustCompile(`Waiting for cluster .+ \(([a-z0-9]+)\) to be ready`)
		if matches := re.FindStringSubmatch(output); len(matches) > 1 {
			cluster.ID = matches[1]
		}
	}

	// Validate we got the essential fields
	if cluster.ID == "" {
		return nil, errors.Errorf("could not parse cluster ID from output")
	}
	if cluster.Name == "" {
		return nil, errors.Errorf("could not parse cluster name from output")
	}

	return cluster, nil
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
		return nil, errors.Errorf("failed to create Redis database %q in region %q: %w", req.Name, req.Region, err)
	}

	var redis FlyioRedis
	if err := json.Unmarshal(output, &redis); err != nil {
		return nil, errors.Errorf("failed to parse redis response: %w", err)
	}

	return &redis, nil
}

// GetPostgresConnectionInfo retrieves PostgreSQL connection information
func (c *FlyctlClient) GetPostgresConnectionInfo(ctx context.Context, appID string) (*PostgresConnectionInfo, error) {
	// Check if flyctl is installed
	if err := c.ensureFlyctl(ctx); err != nil {
		return nil, err
	}
	// Use flyctl mpg db list to get connection info
	output, err := c.executor.Execute(ctx, "flyctl", "mpg", "db", "list", "--app", appID, "--json")
	if err != nil {
		return nil, errors.Errorf("failed to get postgres connection info: %w", err)
	}

	// Parse the response to extract connection strings
	var dbList []struct {
		Name  string `json:"name"`
		Users []struct {
			Username string `json:"username"`
			Password string `json:"password"`
		} `json:"users"`
	}

	if err := json.Unmarshal(output, &dbList); err != nil {
		return nil, errors.Errorf("failed to parse database list: %w", err)
	}

	if len(dbList) == 0 {
		return nil, errors.Errorf("no databases found")
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
		return nil, errors.Errorf("failed to get redis connection info: %w", err)
	}

	var status struct {
		ConnectionString string `json:"connection_string"`
	}

	if err := json.Unmarshal(output, &status); err != nil {
		return nil, errors.Errorf("failed to parse redis status: %w", err)
	}

	// For Redis, internal and external are typically the same
	return &RedisConnectionInfo{
		InternalConnectionString: status.ConnectionString,
		ExternalConnectionString: status.ConnectionString,
	}, nil
}

// AttachPostgres attaches a managed PostgreSQL cluster to an app
func (c *FlyctlClient) AttachPostgres(ctx context.Context, req AttachPostgresRequest) error {
	// Check if flyctl is installed
	if err := c.ensureFlyctl(ctx); err != nil {
		return err
	}

	args := []string{
		"mpg", "attach",
		req.ClusterID, // Use cluster ID directly
		"--app", req.AppName,
	}

	// Add variable name (default is DATABASE_URL)
	if req.VariableName != "" {
		args = append(args, "--variable-name", req.VariableName)
	} else {
		args = append(args, "--variable-name", "DATABASE_URL")
	}

	// Use ExecuteInteractive to show output to user
	err := c.executor.ExecuteInteractive(ctx, c.writer, "flyctl", args...)
	if err != nil {
		return errors.Errorf("failed to attach PostgreSQL cluster %q to app %q: %w",
			req.ClusterID, req.AppName, err)
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

	// Use ExecuteInteractive to show output to user
	err := c.executor.ExecuteInteractive(ctx, c.writer, "flyctl", args...)
	if err != nil {
		return errors.Errorf("failed to attach Redis %q to app %q: %w", req.RedisName, req.AppName, err)
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
		return nil, errors.Errorf("failed to create volume: %w", err)
	}

	var volume FlyioVolume
	if err := json.Unmarshal(output, &volume); err != nil {
		return nil, errors.Errorf("failed to parse volume response: %w", err)
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
		return nil, errors.Errorf("failed to get app logs: %w", err)
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
		return nil, errors.Errorf("failed to get app metrics: %w", err)
	}

	var status struct {
		Allocations []struct {
			ID     string  `json:"id"`
			Status string  `json:"status"`
			CPU    float64 `json:"cpu"`
			Memory float64 `json:"memory"`
		} `json:"allocations"`
	}

	if err := json.Unmarshal(output, &status); err != nil {
		return nil, errors.Errorf("failed to parse status: %w", err)
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
		}
		if config.BuildConfig.BuildCmd != "" {
			builder.WriteString(fmt.Sprintf("  build_cmd = \"%s\"\n", config.BuildConfig.BuildCmd))
		}
		builder.WriteString("\n")
	}

	// Deploy configuration (for release command/migrations)
	if config.ReleaseCommand != "" {
		builder.WriteString("[deploy]\n")
		builder.WriteString(fmt.Sprintf("  release_command = \"%s\"\n", config.ReleaseCommand))
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

// ListReleases lists all releases for an app with their Docker images
func (c *FlyctlClient) ListReleases(ctx context.Context, appID string) ([]FlyioRelease, error) {
	if err := c.ensureFlyctl(ctx); err != nil {
		return nil, err
	}

	args := []string{
		"releases",
		"--app", appID,
		"--image",
	}

	output, err := c.executor.Execute(ctx, "flyctl", args...)
	if err != nil {
		return nil, errors.Errorf("failed to list releases: %w", err)
	}

	return c.parseReleases(string(output))
}

// parseReleases parses the output of `fly releases --image`
func (c *FlyctlClient) parseReleases(output string) ([]FlyioRelease, error) {
	lines := strings.Split(output, "\n")
	var releases []FlyioRelease

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "VERSION") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}

		release := FlyioRelease{
			Version:     fields[0],
			Status:      fields[1],
			Description: fields[2],
			User:        fields[3],
		}

		dockerImageIdx := -1
		for i := 4; i < len(fields); i++ {
			if strings.Contains(fields[i], "registry.fly.io") {
				dockerImageIdx = i
				break
			}
		}

		if dockerImageIdx != -1 {
			release.DockerImage = fields[dockerImageIdx]
			release.Date = strings.Join(fields[4:dockerImageIdx], " ")
		} else {
			release.Date = strings.Join(fields[4:], " ")
		}

		if release.DockerImage != "" {
			releases = append(releases, release)
		}
	}

	// Sort releases by version number descending (newest first)
	// Versions are in format "v123" so we extract the number and sort
	sort.Slice(releases, func(i, j int) bool {
		// Extract version numbers (remove 'v' prefix)
		vi := strings.TrimPrefix(releases[i].Version, "v")
		vj := strings.TrimPrefix(releases[j].Version, "v")

		// Parse as integers
		numI, errI := strconv.Atoi(vi)
		numJ, errJ := strconv.Atoi(vj)

		if errI != nil || errJ != nil {
			// If parsing fails, fall back to string comparison
			return releases[i].Version > releases[j].Version
		}

		// Sort descending (higher version first)
		return numI > numJ
	})

	slog.Info("ListReleases sorted", "count", len(releases))
	for i, r := range releases {
		slog.Info("Release", "index", i, "version", r.Version, "status", r.Status, "image", r.DockerImage, "date", r.Date)
	}

	return releases, nil
}

// DeployImage deploys a specific Docker image to an app
func (c *FlyctlClient) DeployImage(ctx context.Context, appID, imageURL string) error {
	// Check if flyctl is installed
	if err := c.ensureFlyctl(ctx); err != nil {
		return err
	}

	// Use flyctl deploy with specific image
	args := []string{
		"deploy",
		"--app", appID,
		"--image", imageURL,
		"--yes", // Auto-confirm
	}

	cmd := exec.CommandContext(ctx, "flyctl", args...)
	cmd.Env = os.Environ()
	cmd.Stdout = c.writer
	cmd.Stderr = c.writer

	if err := cmd.Run(); err != nil {
		return errors.Errorf("failed to deploy image %s: %w", imageURL, err)
	}

	slog.Info("Image deployed successfully", "image", imageURL)
	return nil
}

// SetSecrets sets secrets for a Fly.io app using 'fly secrets set'
func (c *FlyctlClient) SetSecrets(ctx context.Context, appID string, secrets map[string]string) error {
	// Check if flyctl is installed
	if err := c.ensureFlyctl(ctx); err != nil {
		return err
	}

	if len(secrets) == 0 {
		return nil // Nothing to set
	}

	// Build the secrets command arguments
	// Format: fly secrets set KEY1=value1 KEY2=value2 --app appID
	args := []string{"secrets", "set"}

	for key, value := range secrets {
		args = append(args, fmt.Sprintf("%s=%s", key, value))
	}

	args = append(args, "--app", appID)

	cmd := exec.CommandContext(ctx, "flyctl", args...)
	cmd.Env = os.Environ()
	cmd.Stdout = c.writer
	cmd.Stderr = c.writer

	if err := cmd.Run(); err != nil {
		return errors.Errorf("failed to set secrets: %w", err)
	}

	slog.Info("Secrets set successfully", "count", len(secrets), "app", appID)
	return nil
}
