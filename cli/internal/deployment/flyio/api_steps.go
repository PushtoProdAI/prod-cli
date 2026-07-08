package flyio

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-errors/errors"
	"github.com/xo/dburl"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
)

// CreateFlyioAppStep creates a new Fly.io app
type CreateFlyioAppStep struct {
	BaseStep
	appName string
	region  string
	// explicitName is true when the user pinned the name (--name). A global collision then
	// fails loudly instead of auto-suffixing — the suffix would fork an unmanaged, orphaned
	// app that CI's later destroy-by-name would never find.
	explicitName bool
}

func (c *CreateFlyioAppStep) Execute(ctx context.Context, client FlyioClient, stepResults map[string]any) (any, error) {
	// The appName should already be normalized by GenerateAPISteps
	appName := c.appName

	// First check if app already exists in your organization
	existingApp, err := client.GetApp(ctx, appName)
	if err == nil && existingApp != nil {
		// App already exists, use it instead of creating
		slog.Info("App already exists, using existing app", "name", appName, "id", existingApp.ID)
		return deployment.CreatedResource{
			ID:   existingApp.ID,
			Type: "app",
			Name: existingApp.Name,
		}, nil
	}

	// Create the app with the normalized name
	slog.Info("Creating Fly.io app", "name", appName)
	app, err := client.CreateApp(ctx, CreateAppRequest{
		Name:    appName,
		Region:  c.region,
		OrgSlug: defaultOrg,
	})
	if err != nil {
		// Check if it's a "name already taken" error
		if strings.Contains(err.Error(), "already been taken") || strings.Contains(err.Error(), "Name has already been taken") {
			// The name is globally reserved by another Fly.io user or in a zombie state
			// Try to get the existing app one more time (in case it's in our org)
			existingApp, getErr := client.GetApp(ctx, appName)
			if getErr == nil && existingApp != nil {
				slog.Info("App was created by another process, using existing app", "name", appName, "id", existingApp.ID)
				return deployment.CreatedResource{
					ID:   existingApp.ID,
					Type: "app",
					Name: existingApp.Name,
				}, nil
			}

			// The name is taken outside your org. When the user PINNED the name (--name, for
			// CI/per-PR), never auto-suffix: a renamed app is unmanaged and orphaned (the
			// matching destroy-by-name would never find it). Fail loudly so the caller picks a
			// unique name instead.
			if c.explicitName {
				return nil, errors.Errorf("Fly.io app name %q is already taken (by another org), and you pinned it with --name, so prod won't silently rename it.\n\n"+
					"Pick a different --name (e.g. add your repo/owner to the prefix) — a name that's globally unique on Fly.io.", appName)
			}

			// Try with a random suffix
			suffix := GenerateRandomSuffix()
			newAppName := appName + "-" + suffix

			// Ensure the new name doesn't exceed 63 chars
			if len(newAppName) > 63 {
				// Truncate the base name to make room for suffix
				baseLen := 63 - len(suffix) - 1 // -1 for the dash
				newAppName = appName[:baseLen] + "-" + suffix
			}

			slog.Info("App name taken, retrying with suffix", "original", appName, "new", newAppName)
			app, retryErr := client.CreateApp(ctx, CreateAppRequest{
				Name:    newAppName,
				Region:  c.region,
				OrgSlug: defaultOrg,
			})
			if retryErr != nil {
				// If retry also fails, provide a clear error message
				return nil, errors.Errorf("app name %q is already taken globally on Fly.io and retry with %q also failed: %v\n\n"+
					"This could be:\n"+
					"1. Taken by another Fly.io user\n"+
					"2. Reserved from a previous failed deployment\n\n"+
					"Solutions:\n"+
					"- Rename your project directory to something more unique\n"+
					"- Or manually choose a unique name for your app", appName, newAppName, retryErr)
			}

			// Success with suffix - update the appName so subsequent steps use it
			slog.Info("Successfully created app with suffix", "name", newAppName)
			// Note: We can't update c.appName here as it's read-only during execution
			// The returned resource will have the actual name used
			return deployment.CreatedResource{
				ID:   app.ID,
				Type: "app",
				Name: app.Name,
			}, nil
		} else {
			return nil, err
		}
	}

	// Return as CreatedResource for consistency
	return deployment.CreatedResource{
		ID:   app.ID,
		Type: "app",
		Name: app.Name,
	}, nil
}

func (c *CreateFlyioAppStep) Rollback(ctx context.Context, client FlyioClient, stepResults map[string]any) error {
	// Get the app ID from step results
	if appResult, ok := stepResults[c.GetID()]; ok {
		if resource, ok := appResult.(deployment.CreatedResource); ok {
			return client.DestroyApp(ctx, resource.ID)
		}
	}
	return errors.Errorf("could not find app ID for rollback")
}

// DeployFlyioConfigStep deploys configuration to a Fly.io app
type DeployFlyioConfigStep struct {
	BaseStep
	appName   string // App name (fallback if step results not available)
	appStepID string // ID of the step that created the app
	config    *FlyioConfig
}

func (d *DeployFlyioConfigStep) Execute(ctx context.Context, client FlyioClient, stepResults map[string]any) (any, error) {
	// Try to get the actual app name from the create-app step results
	// This handles cases where the app was created with a different name (e.g., with a suffix)
	appName := d.appName
	if d.appStepID != "" {
		if appResult, ok := stepResults[d.appStepID]; ok {
			if resource, ok := appResult.(deployment.CreatedResource); ok {
				appName = resource.Name
			}
		}
	}

	if appName == "" {
		return nil, errors.Errorf("app name is required")
	}

	slog.Info("Deploying app configuration", "app", appName)

	// Update config with the actual app name (in case it was created with a suffix)
	d.config.AppName = appName

	// Deploy using the actual app name
	err := client.DeployApp(ctx, appName, d.config)
	if err != nil {
		return nil, err
	}

	// Fetch the app information to get the URL
	app, err := client.GetApp(ctx, appName)
	if err != nil {
		// Don't fail the deployment if we can't fetch app info, just log it
		return deployment.CreatedResource{
			ID:   appName,
			Type: "app",
			Name: appName,
		}, nil
	}

	return deployment.CreatedResource{
		ID:   app.ID,
		Type: "app",
		Name: app.Name,
		Metadata: map[string]any{
			"url":      app.Hostname,
			"app_url":  app.AppURL,
			"hostname": app.Hostname,
		},
	}, nil
}

func (d *DeployFlyioConfigStep) Rollback(ctx context.Context, client FlyioClient, stepResults map[string]any) error {
	// For config deployment, rollback might involve redeploying a previous config
	// For now, we'll just log that rollback is not implemented
	return errors.Errorf("config rollback not implemented")
}

// CreateFlyioServiceStep creates a backing service (database, volume, etc.)
type CreateFlyioServiceStep struct {
	BaseStep
	serviceType string
	name        string
	region      string
	size        int
}

func (c *CreateFlyioServiceStep) Execute(ctx context.Context, client FlyioClient, stepResults map[string]any) (any, error) {
	switch c.serviceType {
	case "postgres":
		cluster, err := client.CreatePostgres(ctx, CreatePostgresRequest{
			Name:       c.name,
			Region:     c.region,
			VolumeSize: c.size,
			Plan:       "basic", // Default to basic plan
		})
		if err != nil {
			return nil, err
		}

		// No need to wait - CreatePostgres already waits for provisioning
		return deployment.CreatedResource{
			ID:   cluster.ID,
			Type: "postgres_cluster",
			Name: cluster.Name,
			Metadata: map[string]any{
				"cluster_id":        cluster.ID,
				"connection_string": cluster.ConnectionString,
			},
		}, nil

	case "redis":
		redis, err := client.CreateRedis(ctx, CreateRedisRequest{
			Name:   c.name,
			Region: c.region,
		})
		if err != nil {
			return nil, err
		}

		// Wait for Redis to be ready
		if err := c.waitForServiceReady(ctx, client, c.name, "redis"); err != nil {
			return nil, errors.Errorf("redis created but failed to become ready: %w", err)
		}

		return deployment.CreatedResource{
			ID:   redis.ID,
			Type: "redis",
			Name: redis.Name,
		}, nil

	// Volume creation would go here but is handled differently in Fly.io
	// Volumes must be created after the app exists

	default:
		return nil, errors.Errorf("unsupported service type: %s", c.serviceType)
	}
}

// waitForServiceReady polls the service status until it's ready
func (c *CreateFlyioServiceStep) waitForServiceReady(ctx context.Context, client FlyioClient, serviceName string, serviceType string) error {
	// Use configuration constants from flyio.go
	timeoutCtx, cancel := context.WithTimeout(ctx, serviceReadyTimeout)
	defer cancel()

	ticker := time.NewTicker(serviceReadyInterval)
	defer ticker.Stop()

	for {
		select {
		case <-timeoutCtx.Done():
			return errors.Errorf("%s service %s failed to become ready within %v", serviceType, serviceName, serviceReadyTimeout)
		case <-ticker.C:
			// Check service status based on type
			ready := false
			var err error

			switch serviceType {
			case "postgres":
				// Check if PostgreSQL is ready by trying to get connection info
				_, err = client.GetPostgresConnectionInfo(ctx, serviceName)
				ready = err == nil
			case "redis":
				// Check if Redis is ready by trying to get connection info
				_, err = client.GetRedisConnectionInfo(ctx, serviceName)
				ready = err == nil
			}

			if ready {
				return nil
			}
		}
	}
}

func (c *CreateFlyioServiceStep) Rollback(ctx context.Context, client FlyioClient, stepResults map[string]any) error {
	// Get the service ID from step results
	if serviceResult, ok := stepResults[c.GetID()]; ok {
		if resource, ok := serviceResult.(deployment.CreatedResource); ok {
			// For now, we'll just log that service deletion is not implemented
			// In a real implementation, you would call the appropriate delete method
			return errors.Errorf("service deletion not implemented for %s", resource.Type)
		}
	}
	return errors.Errorf("could not find service ID for rollback")
}

// AttachPostgresStep attaches a PostgreSQL database to a Fly.io app
type AttachPostgresStep struct {
	BaseStep
	appStepID     string              // ID of the step that created the app
	serviceStepID string              // ID of the step that created the postgres cluster
	variableName  string              // Main DATABASE_URL variable name
	allEnvVars    []deployment.EnvVar // All PostgreSQL env vars from spec
}

func (s *AttachPostgresStep) Execute(ctx context.Context, client FlyioClient, stepResults map[string]any) (any, error) {
	// Get the actual app name from the create-app step results
	appName := ""
	if appResult, ok := stepResults[s.appStepID]; ok {
		if resource, ok := appResult.(deployment.CreatedResource); ok {
			appName = resource.Name
		}
	}

	if appName == "" {
		return nil, errors.Errorf("could not find app name from step %s", s.appStepID)
	}

	// Get the cluster ID and connection string from the previous step
	clusterID := ""
	connectionString := ""
	if serviceResult, ok := stepResults[s.serviceStepID]; ok {
		if resource, ok := serviceResult.(deployment.CreatedResource); ok {
			if metadata, ok := resource.Metadata["cluster_id"].(string); ok {
				clusterID = metadata
			}
			// Get the connection string that was already created
			if connStr, ok := resource.Metadata["connection_string"].(string); ok {
				connectionString = connStr
			}
		}
	}

	if clusterID == "" {
		return nil, errors.Errorf("could not find cluster ID from step %s", s.serviceStepID)
	}

	// Attach using cluster ID
	err := client.AttachPostgres(ctx, AttachPostgresRequest{
		AppName:      appName,
		ClusterID:    clusterID,
		VariableName: s.variableName,
	})
	if err != nil {
		return nil, errors.Errorf("failed to attach postgres: %w", err)
	}

	// If we don't have the connection string from metadata, try to get it
	if connectionString == "" {
		connInfo, err := client.GetPostgresConnectionInfo(ctx, clusterID)
		if err != nil {
			slog.Warn("Could not get connection info to set individual DB variables", "error", err)
			// Continue anyway - the main DATABASE_URL should be set
			return map[string]string{
				"status":       "attached",
				"app":          appName,
				"cluster_id":   clusterID,
				"variableName": s.variableName,
			}, nil
		}

		// Parse the connection string to extract components
		connectionString = connInfo.InternalConnectionString
		if connectionString == "" {
			connectionString = connInfo.ExternalConnectionString
		}
	}

	// Now set individual DB component variables if they're in the spec
	if len(s.allEnvVars) > 0 && connectionString != "" {
		secrets := make(map[string]string)

		// Parse the connection URL
		// Expected format: postgresql://user:password@host:port/database
		parsedURL, err := parsePostgresURL(connectionString)
		if err != nil {
			slog.Warn("Could not parse connection string for individual variables", "error", err)
		} else {
			// Map each env var based on its role
			for _, envVar := range s.allEnvVars {
				if envVar.Service != "postgresql" {
					continue
				}

				var value string
				switch envVar.Role {
				case deployment.EnvRoleFullURI:
					value = connectionString
				case deployment.EnvRoleHostname:
					value = parsedURL.Host
				case deployment.EnvRolePort:
					value = parsedURL.Port
				case deployment.EnvRoleUsername:
					value = parsedURL.Username
				case deployment.EnvRolePassword:
					value = parsedURL.Password
				case deployment.EnvRoleDatabaseName:
					value = parsedURL.Database
				}

				if value != "" {
					secrets[envVar.Name] = value
				}
			}

			// Set these as secrets (they contain sensitive info like password)
			if len(secrets) > 0 {
				slog.Info("Setting individual PostgreSQL environment variables", "count", len(secrets), "app", appName)
				if err := client.SetSecrets(ctx, appName, secrets); err != nil {
					slog.Warn("Failed to set individual DB variables", "error", err)
					// Don't fail the whole deployment for this
				}
			}
		}
	}

	// Return success indicator
	return map[string]string{
		"status":       "attached",
		"app":          appName,
		"cluster_id":   clusterID,
		"variableName": s.variableName,
	}, nil
}

// ParsedPostgresURL holds the components of a PostgreSQL connection URL
type ParsedPostgresURL struct {
	Host     string
	Port     string
	Username string
	Password string
	Database string
}

// parsePostgresURL parses a PostgreSQL connection URL into its components
// Expected format: postgresql://user:password@host:port/database
func parsePostgresURL(connStr string) (*ParsedPostgresURL, error) {
	parsed, err := dburl.Parse(connStr)
	if err != nil {
		return nil, errors.Errorf("failed to parse connection string: %w", err)
	}

	result := &ParsedPostgresURL{
		Host:     parsed.Hostname(),
		Port:     parsed.Port(),
		Database: strings.TrimPrefix(parsed.Path, "/"),
	}

	if parsed.User != nil {
		result.Username = parsed.User.Username()
		// Password() returns (password, bool) where bool indicates if password was set
		if password, ok := parsed.User.Password(); ok {
			result.Password = password
		}
	}

	return result, nil
}

func (s *AttachPostgresStep) Rollback(ctx context.Context, client FlyioClient, stepResults map[string]any) error {
	// Detaching would require removing the database user and connection
	// For now, we'll log that detachment is not implemented
	return errors.Errorf("postgres detachment not implemented")
}

// AttachRedisStep attaches a Redis database to a Fly.io app
type AttachRedisStep struct {
	BaseStep
	appStepID    string              // ID of the step that created the app
	redisName    string              // Name of the Redis instance
	variableName string              // Main REDIS_URL variable name
	allEnvVars   []deployment.EnvVar // All Redis env vars from spec
}

func (s *AttachRedisStep) Execute(ctx context.Context, client FlyioClient, stepResults map[string]any) (any, error) {
	// Get the actual app name from the create-app step results
	appName := ""
	if appResult, ok := stepResults[s.appStepID]; ok {
		if resource, ok := appResult.(deployment.CreatedResource); ok {
			appName = resource.Name
		}
	}

	if appName == "" {
		return nil, errors.Errorf("could not find app name from step %s", s.appStepID)
	}

	slog.Info("Attaching Redis to app", "app", appName, "redis", s.redisName)

	// Attach the Redis database to the app
	err := client.AttachRedis(ctx, AttachRedisRequest{
		AppName:      appName,
		RedisName:    s.redisName,
		VariableName: s.variableName,
	})
	if err != nil {
		return nil, errors.Errorf("failed to attach redis: %w", err)
	}

	// Get the connection info to set individual Redis variables
	connInfo, err := client.GetRedisConnectionInfo(ctx, s.redisName)
	if err != nil {
		slog.Warn("Could not get Redis connection info to set individual variables", "error", err)
		return map[string]string{
			"status":       "attached",
			"app":          appName,
			"redis":        s.redisName,
			"variableName": s.variableName,
		}, nil
	}

	// Get the connection string
	connectionString := connInfo.InternalConnectionString
	if connectionString == "" {
		connectionString = connInfo.ExternalConnectionString
	}

	// Set individual Redis component variables if they're in the spec
	if len(s.allEnvVars) > 0 && connectionString != "" {
		secrets := make(map[string]string)

		// Parse the Redis connection URL
		// Expected format: redis://default:password@host:port
		parsedURL, err := parseRedisURL(connectionString)
		if err != nil {
			slog.Warn("Could not parse Redis connection string for individual variables", "error", err)
		} else {
			// Map each env var based on its role
			for _, envVar := range s.allEnvVars {
				if !envVar.IsRedisRelated() {
					continue
				}

				var value string
				switch envVar.Role {
				case deployment.EnvRoleRedisURI:
					value = connectionString
				case deployment.EnvRoleRedisHost:
					value = parsedURL.Host
				case deployment.EnvRoleRedisPort:
					value = parsedURL.Port
				case deployment.EnvRoleRedisPassword:
					value = parsedURL.Password
				}

				if value != "" {
					secrets[envVar.Name] = value
				}
			}

			// Set these as secrets
			if len(secrets) > 0 {
				slog.Info("Setting individual Redis environment variables", "count", len(secrets), "app", appName)
				if err := client.SetSecrets(ctx, appName, secrets); err != nil {
					slog.Warn("Failed to set individual Redis variables", "error", err)
				}
			}
		}
	}

	// Return success indicator
	return map[string]string{
		"status":       "attached",
		"app":          appName,
		"redis":        s.redisName,
		"variableName": s.variableName,
	}, nil
}

// ParsedRedisURL holds the components of a Redis connection URL
type ParsedRedisURL struct {
	Host     string
	Port     string
	Password string
}

// parseRedisURL parses a Redis connection URL into its components
// Expected format: redis://[username]:password@host:port or redis://:password@host:port
func parseRedisURL(connStr string) (*ParsedRedisURL, error) {
	parsed, err := url.Parse(connStr)
	if err != nil {
		return nil, errors.Errorf("failed to parse Redis connection string: %w", err)
	}

	result := &ParsedRedisURL{
		Host: parsed.Hostname(),
		Port: parsed.Port(),
	}

	// Default Redis port
	if result.Port == "" {
		result.Port = "6379"
	}

	if parsed.User != nil {
		// Password() returns (password, bool) where bool indicates if password was set
		if password, ok := parsed.User.Password(); ok {
			result.Password = password
		}
	}

	return result, nil
}

func (s *AttachRedisStep) Rollback(ctx context.Context, client FlyioClient, stepResults map[string]any) error {
	// Detaching would require removing the Redis connection
	// For now, we'll log that detachment is not implemented
	return errors.Errorf("redis detachment not implemented")
}

// GenerateDockerfileStep generates a Dockerfile for the deployment
type GenerateDockerfileStep struct {
	BaseStep
	spec            *deployment.DeploymentSpec
	dockerGenerator *deployment.DockerGenerator
}

func (g *GenerateDockerfileStep) Execute(ctx context.Context, client FlyioClient, stepResults map[string]any) (any, error) {
	// Get the build context from metadata
	buildContext := "."
	if bc, ok := g.spec.Metadata["buildContext"].(string); ok {
		buildContext = bc
	}

	// Determine the internal port (same logic as generateFlyConfig)
	internalPort := g.determineInternalPort()

	// Create a new docker generator with the PORT env var
	envVars := make([]deployment.EnvVar, len(g.spec.EnvVars))
	copy(envVars, g.spec.EnvVars)

	// Add or update PORT env var
	hasPort := false
	for i := range envVars {
		if envVars[i].Name == "PORT" {
			envVars[i].Value = fmt.Sprintf("%d", internalPort)
			hasPort = true
			break
		}
	}
	if !hasPort {
		envVars = append(envVars, deployment.EnvVar{
			Name:  "PORT",
			Value: fmt.Sprintf("%d", internalPort),
		})
	}

	dockerGen := deployment.NewDockerGenerator(nil, envVars)
	defer dockerGen.Close()

	// Generate Dockerfile artifacts
	artifacts, err := dockerGen.GenerateDockerfile(g.spec)
	if err != nil {
		return nil, errors.Errorf("failed to generate Dockerfile: %w", err)
	}

	// The project ships its own Dockerfile — leave it untouched and build with it.
	if artifacts.UseExisting {
		return map[string]string{"dockerfile": "existing"}, nil
	}

	// Write Dockerfile to build context
	dockerfilePath := filepath.Join(buildContext, "Dockerfile")
	if err := os.WriteFile(dockerfilePath, []byte(artifacts.Dockerfile), 0o644); err != nil {
		return nil, errors.Errorf("failed to write Dockerfile: %w", err)
	}

	// Write .dockerignore if provided
	if artifacts.DockerIgnore != "" {
		dockerignorePath := filepath.Join(buildContext, ".dockerignore")
		if err := os.WriteFile(dockerignorePath, []byte(artifacts.DockerIgnore), 0o644); err != nil {
			return nil, errors.Errorf("failed to write .dockerignore: %w", err)
		}
	}

	// Write additional files to build context
	for filename, content := range artifacts.AdditionalFiles {
		filePath := filepath.Join(buildContext, filename)
		if err := os.WriteFile(filePath, []byte(content), 0o755); err != nil {
			return nil, errors.Errorf("failed to write additional file %s: %w", filename, err)
		}
	}

	return map[string]any{
		"dockerfile_path": dockerfilePath,
		"build_context":   buildContext,
	}, nil
}

// determineInternalPort determines the internal port for the application
func (g *GenerateDockerfileStep) determineInternalPort() int {
	// First check if PORT is already defined in env vars
	for _, ev := range g.spec.EnvVars {
		if ev.Name == "PORT" && ev.Value != "" {
			var portInt int
			if _, err := fmt.Sscanf(ev.Value, "%d", &portInt); err == nil && portInt > 0 {
				return portInt
			}
		}
	}

	// Fall back to language default
	config := GetLanguageConfig(g.spec.Language)
	return config.InternalPort
}

func (g *GenerateDockerfileStep) Rollback(ctx context.Context, client FlyioClient, stepResults map[string]any) error {
	// Get the result from this step
	if result, ok := stepResults[g.GetID()]; ok {
		if resultMap, ok := result.(map[string]any); ok {
			if dockerfilePath, ok := resultMap["dockerfile_path"].(string); ok {
				// Clean up generated Dockerfile
				if err := os.Remove(dockerfilePath); err != nil {
					fmt.Printf("Warning: failed to remove generated Dockerfile: %v\n", err)
				}
			}
		}
	}
	return nil
}

// SetSecretsStep sets secrets for a Fly.io app
type SetSecretsStep struct {
	BaseStep
	appName   string // App name (fallback if step results not available)
	appStepID string // ID of the step that created the app
	secrets   map[string]string
}

func (s *SetSecretsStep) Execute(ctx context.Context, client FlyioClient, stepResults map[string]any) (any, error) {
	if len(s.secrets) == 0 {
		return map[string]any{
			"status": "skipped",
			"reason": "no secrets to set",
		}, nil
	}

	// Try to get the actual app name from the create-app step results
	// This handles cases where the app was created with a different name (e.g., with a suffix)
	appName := s.appName
	if s.appStepID != "" {
		if appResult, ok := stepResults[s.appStepID]; ok {
			if resource, ok := appResult.(deployment.CreatedResource); ok {
				appName = resource.Name
			}
		}
	}

	if appName == "" {
		return nil, errors.Errorf("app name is required")
	}

	slog.Info("Setting secrets for app", "app", appName, "count", len(s.secrets))

	err := client.SetSecrets(ctx, appName, s.secrets)
	if err != nil {
		return nil, errors.Errorf("failed to set secrets: %w", err)
	}

	return map[string]any{
		"status":       "success",
		"secrets_set":  len(s.secrets),
		"secret_names": getSecretNames(s.secrets),
	}, nil
}

// getSecretNames returns a list of secret names (not values) for logging
func getSecretNames(secrets map[string]string) []string {
	names := make([]string, 0, len(secrets))
	for name := range secrets {
		names = append(names, name)
	}
	return names
}

func (s *SetSecretsStep) Rollback(ctx context.Context, client FlyioClient, stepResults map[string]any) error {
	// Secrets cannot be easily rolled back - they would need to be unset individually
	// For now, we'll leave secrets in place as they don't break anything
	return nil
}
