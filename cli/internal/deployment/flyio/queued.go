package flyio

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/go-errors/errors"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
)

// FlyioQueuedDeployment handles step-by-step deployments to Fly.io
// This deployment strategy creates resources one at a time with progress tracking
type FlyioQueuedDeployment struct {
	client          FlyioClient
	spec            *deployment.DeploymentSpec
	dockerGenerator *deployment.DockerGenerator
	writer          io.Writer
}

// NewFlyioQueuedDeployment creates a new queued deployment for Fly.io
func NewFlyioQueuedDeployment(client FlyioClient, spec *deployment.DeploymentSpec, dockerGenerator *deployment.DockerGenerator, writer io.Writer) *FlyioQueuedDeployment {
	return &FlyioQueuedDeployment{
		client:          client,
		spec:            spec,
		dockerGenerator: dockerGenerator,
		writer:          writer,
	}
}

// Deploy performs the queued deployment to Fly.io
func (fqd *FlyioQueuedDeployment) Deploy(ctx context.Context) ([]deployment.CreatedResource, error) {
	steps := fqd.GenerateAPISteps()

	var createdResources []deployment.CreatedResource
	stepResults := make(map[string]interface{})

	for _, step := range steps {
		fmt.Fprintf(fqd.writer, "🔄 Executing: %s...\n", step.GetDescription())
		result, err := step.Execute(ctx, fqd.client, stepResults)
		if err != nil {
			fmt.Fprintf(fqd.writer, "✗ Failed: %s - %v\n", step.GetDescription(), err)
			return nil, errors.Errorf("step %s failed: %w", step.GetID(), err)
		}
		stepResults[step.GetID()] = result

		// Convert result to CreatedResource if applicable
		if resource, ok := result.(deployment.CreatedResource); ok {
			createdResources = append(createdResources, resource)
		}
		fmt.Fprintf(fqd.writer, "✓ Completed: %s\n", step.GetDescription())

	}

	return createdResources, nil
}

// GenerateAPISteps generates the deployment steps for Fly.io
func (fqd *FlyioQueuedDeployment) GenerateAPISteps() []FlyioAPIStep {
	var steps []FlyioAPIStep
	var serviceStepIDs []string
	var attachmentStepIDs []string
	appName := NormalizeFlyAppName(fqd.spec.Name)

	// Inform user if the app name was normalized
	if appName != fqd.spec.Name {
		fmt.Fprintf(fqd.writer, "ℹ️  App name normalized: %q → %q (Fly.io requires lowercase letters, numbers, and dashes only)\n", fqd.spec.Name, appName)
	}

	appStepID := "create-app"

	if fqd.spec.IsUpdate {
		// For updates, create missing services and deploy the new configuration

		// Step 1: Create only missing backing services
		// Note: App already exists for updates, so no app step ID dependency needed
		for i, service := range fqd.spec.Services {
			exists := false
			for _, existingDB := range fqd.spec.ExistingDatabases {
				if existingDB == service.Provider {
					exists = true
					break
				}
			}
			if !exists {
				stepID := fmt.Sprintf("create-service-%d", i)
				// Pass empty string for appStepID since app already exists
				step := fqd.createServiceStep(service, stepID, "")
				if step != nil {
					steps = append(steps, step)
					serviceStepIDs = append(serviceStepIDs, stepID)

					attachStepID := fmt.Sprintf("attach-service-%d", i)
					attachStep := fqd.createAttachmentStep(service, attachStepID, stepID, appName, "")
					if attachStep != nil {
						steps = append(steps, attachStep)
						attachmentStepIDs = append(attachmentStepIDs, attachStepID)
					}
				}
			}
		}

		// Step 2: Generate Dockerfile (if Docker is available)
		generateDockerfileStepID := "generate-dockerfile"
		var deployDeps []string
		deployDeps = append(deployDeps, attachmentStepIDs...)
		if fqd.dockerGenerator != nil && deployment.IsDockerAvailable() {
			steps = append(steps, &GenerateDockerfileStep{
				BaseStep: BaseStep{
					ID:          generateDockerfileStepID,
					Description: "Generating Dockerfile for deployment",
				},
				spec:            fqd.spec,
				dockerGenerator: fqd.dockerGenerator,
			})
			deployDeps = append(deployDeps, generateDockerfileStepID)
		}

		// Step 3: Set/update secrets
		secretsStepID := "set-secrets"
		secrets := fqd.extractSecrets()
		if len(secrets) > 0 {
			steps = append(steps, &SetSecretsStep{
				BaseStep: BaseStep{
					ID:          secretsStepID,
					Description: fmt.Sprintf("Updating %d secret(s) for app", len(secrets)),
				},
				appName: appName,
				secrets: secrets,
			})
			deployDeps = append(deployDeps, secretsStepID)
		}

		// Step 4: Deploy configuration
		steps = append(steps, &DeployFlyioConfigStep{
			BaseStep: BaseStep{
				ID:          "deploy-config",
				Description: "Deploying app configuration update",
				DependsOn:   deployDeps,
			},
			appName: appName,
			config:  fqd.generateFlyConfig(),
		})
		return steps
	}

	// Fresh deployment flow below

	// Step 1: Create backing services first (they're independent)
	// Redis now uses getDefaultOrganization() so doesn't need apps to exist
	for i, service := range fqd.spec.Services {
		stepID := fmt.Sprintf("create-service-%d", i)
		step := fqd.createServiceStep(service, stepID, "")
		if step != nil {
			steps = append(steps, step)
			serviceStepIDs = append(serviceStepIDs, stepID)
		}
	}

	// Step 2: Create main app
	steps = append(steps, &CreateFlyioAppStep{
		BaseStep: BaseStep{
			ID:          appStepID,
			Description: fmt.Sprintf("Creating Fly.io app: %s", appName),
		},
		appName: appName,
		region:  defaultRegion,
	})

	// Step 3: Attach databases to the app (after app creation)
	// Only create attachment steps for services that were successfully created
	for i, service := range fqd.spec.Services {
		// Check if we created a step for this service
		serviceStepID := fmt.Sprintf("create-service-%d", i)
		serviceStepCreated := false
		for _, sid := range serviceStepIDs {
			if sid == serviceStepID {
				serviceStepCreated = true
				break
			}
		}

		// Only create attachment if service step was created
		if serviceStepCreated {
			attachStepID := fmt.Sprintf("attach-service-%d", i)
			attachStep := fqd.createAttachmentStep(service, attachStepID, serviceStepID, appName, appStepID)
			if attachStep != nil {
				steps = append(steps, attachStep)
				attachmentStepIDs = append(attachmentStepIDs, attachStepID)
			}
		}
	}

	// Step 4: Generate Dockerfile (after app creation, before deployment)
	generateDockerfileStepID := "generate-dockerfile"
	if fqd.dockerGenerator != nil && deployment.IsDockerAvailable() {
		steps = append(steps, &GenerateDockerfileStep{
			BaseStep: BaseStep{
				ID:          generateDockerfileStepID,
				Description: "Generating Dockerfile for deployment",
				DependsOn:   []string{appStepID},
			},
			spec:            fqd.spec,
			dockerGenerator: fqd.dockerGenerator,
		})
	}

	// Step 5: Set secrets (after app creation, before deployment)
	secretsStepID := "set-secrets"
	secrets := fqd.extractSecrets()
	if len(secrets) > 0 {
		steps = append(steps, &SetSecretsStep{
			BaseStep: BaseStep{
				ID:          secretsStepID,
				Description: fmt.Sprintf("Setting %d secret(s) for app", len(secrets)),
				DependsOn:   []string{appStepID},
			},
			appName:   appName,
			appStepID: appStepID,
			secrets:   secrets,
		})
	}

	// Step 6: Deploy app configuration (after Dockerfile generation, secrets, and attachments are complete)
	deployDeps := []string{appStepID}
	deployDeps = append(deployDeps, attachmentStepIDs...)
	if fqd.dockerGenerator != nil && deployment.IsDockerAvailable() {
		deployDeps = append(deployDeps, generateDockerfileStepID)
	}
	if len(secrets) > 0 {
		deployDeps = append(deployDeps, secretsStepID)
	}

	steps = append(steps, &DeployFlyioConfigStep{
		BaseStep: BaseStep{
			ID:          "deploy-config",
			Description: "Deploying app configuration",
			DependsOn:   deployDeps,
		},
		appName:   appName,
		appStepID: appStepID,
		config:    fqd.generateFlyConfig(),
	})

	return steps
}

// createServiceStep creates a deployment step for a service
func (fqd *FlyioQueuedDeployment) createServiceStep(service deployment.Service, stepID string, appStepID string) FlyioAPIStep {
	normalizedAppName := NormalizeFlyAppName(fqd.spec.Name)
	switch service.Provider {
	case "postgresql":
		// Postgres is independent - doesn't need to wait for app
		return &CreateFlyioServiceStep{
			BaseStep: BaseStep{
				ID:          stepID,
				Description: fmt.Sprintf("Creating PostgreSQL database: %s", service.Name),
			},
			serviceType: "postgres",
			name:        fmt.Sprintf("%s-postgres", normalizedAppName),
			region:      defaultRegion,
			size:        postgresVolumeSizeGB,
		}
	case "redis":
		// Redis uses getDefaultOrganization() so is independent
		return &CreateFlyioServiceStep{
			BaseStep: BaseStep{
				ID:          stepID,
				Description: fmt.Sprintf("Creating Redis database: %s", service.Name),
			},
			serviceType: "redis",
			name:        fmt.Sprintf("%s-redis", normalizedAppName),
			region:      defaultRegion,
		}
	case "volume":
		// Volumes need to be created after the app exists
		// Skip volume creation in the queued steps
		return nil
	default:
		return nil
	}
}

// createAttachmentStep creates an attachment step for a service
func (fqd *FlyioQueuedDeployment) createAttachmentStep(service deployment.Service, stepID string, serviceStepID string, appName string, appStepID string) FlyioAPIStep {
	// Only create attachment steps for services that were actually created
	switch service.Provider {
	case "postgresql":
		pgURLVar := "DATABASE_URL"
		var pgEnvVars []deployment.EnvVar
		for _, v := range fqd.spec.EnvVars {
			// Include all DB-related vars (PostgreSQL is the default/primary database)
			if v.IsDBRelated() {
				// Normalize service to postgresql
				if v.Service == "" || v.Service == "unknown" {
					v.Service = "postgresql"
				}
				pgEnvVars = append(pgEnvVars, v)
				if v.Role == deployment.EnvRoleFullURI {
					pgURLVar = v.Name
				}
			}
		}
		return &AttachPostgresStep{
			BaseStep: BaseStep{
				ID:          stepID,
				Description: fmt.Sprintf("Attaching PostgreSQL to app: %s", appName),
				DependsOn:   []string{appStepID, serviceStepID}, // Depends on both app and service creation
			},
			appStepID:     appStepID, // Pass the app step ID to retrieve actual app name
			variableName:  pgURLVar,
			serviceStepID: serviceStepID, // Pass the service step ID to retrieve cluster ID
			allEnvVars:    pgEnvVars,     // Pass all PostgreSQL env vars to set individual components
		}
	case "redis":
		redisURLVar := "REDIS_URL"
		var redisEnvVars []deployment.EnvVar
		for _, v := range fqd.spec.EnvVars {
			if v.IsRedisRelated() {
				redisEnvVars = append(redisEnvVars, v)
				if v.Role == deployment.EnvRoleRedisURI {
					redisURLVar = v.Name
				}
			}
		}
		return &AttachRedisStep{
			BaseStep: BaseStep{
				ID:          stepID,
				Description: fmt.Sprintf("Attaching Redis to app: %s", appName),
				DependsOn:   []string{appStepID, serviceStepID}, // Depends on both app and service creation
			},
			appStepID:    appStepID, // Pass the app step ID to retrieve actual app name
			redisName:    fmt.Sprintf("%s-redis", NormalizeFlyAppName(fqd.spec.Name)),
			variableName: redisURLVar,
			allEnvVars:   redisEnvVars, // Pass all Redis env vars to set individual components
		}
	default:
		// Don't create attachment steps for unsupported services
		return nil
	}
}

// generateFlyConfig generates the Fly.io configuration
// Sensitive environment variables are excluded and should be set via SetSecrets
func (fqd *FlyioQueuedDeployment) generateFlyConfig() *FlyioConfig {
	envVars := make(map[string]string)

	// Only include non-sensitive, non-backing-service environment variables in fly.toml
	// Sensitive variables will be set using fly secrets
	// Backing service variables (DB, Redis) are auto-populated by the platform
	for _, ev := range fqd.spec.EnvVars {
		if !ev.IsBackingServiceRelated() && ev.Value != "" && !ev.Sensitive {
			envVars[ev.Name] = ev.Value
		}
	}

	// Determine the internal port
	internalPort := fqd.determineInternalPort()

	// Always set PORT environment variable to match internal_port
	// This ensures the app knows which port to listen on
	envVars["PORT"] = fmt.Sprintf("%d", internalPort)

	config := &FlyioConfig{
		AppName:        NormalizeFlyAppName(fqd.spec.Name),
		ReleaseCommand: fqd.spec.MigrationCommand,
		EnvVars:        envVars,
	}

	// Set source path if available in metadata
	if sourcePath, ok := fqd.spec.Metadata["buildContext"].(string); ok {
		config.SourcePath = sourcePath
	}

	// Add build configuration - use Dockerfile if Docker is available
	config.BuildConfig = &BuildConfig{
		Dockerfile: "Dockerfile",
		BuildCmd:   fqd.spec.BuildCommand,
		StartCmd:   fqd.spec.StartCommand,
	}

	// Add service configuration
	config.Services = []ServiceConfig{
		{
			Protocol:     "tcp",
			InternalPort: internalPort,
			Ports: []Port{
				{Port: 80, Handlers: []string{"http"}},
				{Port: 443, Handlers: []string{"tls", "http"}},
			},
		},
	}
	return config
}

// determineInternalPort determines the internal port for the application
// Priority: 1) PORT env var from spec, 2) language default, 3) 8080 fallback
func (fqd *FlyioQueuedDeployment) determineInternalPort() int {
	// First check if PORT is already defined in env vars
	for _, ev := range fqd.spec.EnvVars {
		if ev.Name == "PORT" && ev.Value != "" {
			var portInt int
			if _, err := fmt.Sscanf(ev.Value, "%d", &portInt); err == nil && portInt > 0 {
				return portInt
			}
		}
	}

	// Fall back to language default
	return fqd.getInternalPortForLanguage(fqd.spec.Language)
}

// getInternalPortForLanguage returns the default internal port for the given language
func (fqd *FlyioQueuedDeployment) getInternalPortForLanguage(language string) int {
	config := GetLanguageConfig(language)
	return config.InternalPort
}

// extractSecrets extracts sensitive environment variables that should be set as secrets
func (fqd *FlyioQueuedDeployment) extractSecrets() map[string]string {
	secrets := make(map[string]string)

	for _, ev := range fqd.spec.EnvVars {
		// Include all sensitive variables (both DB-related and non-DB-related)
		// DB-related secrets like DATABASE_URL are also set as secrets
		if ev.Sensitive && ev.Value != "" {
			secrets[ev.Name] = ev.Value
		}
	}

	return secrets
}

func (fqd *FlyioQueuedDeployment) getCurrentDeployment(ctx context.Context) (*deployment.DeploymentInfo, error) {
	if fqd.spec.Name == "" {
		return nil, errors.Errorf("no app name available")
	}

	releases, err := fqd.client.ListReleases(ctx, fqd.spec.Name)
	if err != nil {
		return nil, errors.Errorf("failed to list releases: %w", err)
	}

	if len(releases) == 0 {
		return nil, errors.Errorf("no releases found for app %s", fqd.spec.Name)
	}

	slog.Info("GetCurrentDeployment: found releases", "count", len(releases))

	// Releases are sorted newest-first, so take the first complete release with a docker image
	for i, rel := range releases {
		slog.Info("Checking release", "index", i, "version", rel.Version, "status", rel.Status, "image", rel.DockerImage)

		if rel.Status == "complete" && rel.DockerImage != "" {
			slog.Info("Found current deployment", "version", rel.Version, "image", rel.DockerImage)
			return &deployment.DeploymentInfo{
				ID:        rel.DockerImage,
				Status:    rel.Status,
				CreatedAt: rel.Date,
			}, nil
		}
	}

	return nil, errors.Errorf("no complete release found for app %s", fqd.spec.Name)
}

func (fqd *FlyioQueuedDeployment) GetPreviousDeployment(ctx context.Context) (*deployment.DeploymentInfo, error) {
	if fqd.spec.Name == "" {
		return nil, errors.Errorf("no app name available")
	}

	releases, err := fqd.client.ListReleases(ctx, fqd.spec.Name)
	if err != nil {
		return nil, errors.Errorf("failed to list releases: %w", err)
	}

	if len(releases) < 2 {
		return nil, errors.Errorf("no previous release found for app %s (need at least 2 releases, found %d)", fqd.spec.Name, len(releases))
	}

	slog.Info("GetPreviousDeployment: found releases", "count", len(releases))

	// Determine if we need to skip the first complete release or not
	// If the most recent release is failed/incomplete, this is likely an auto-rollback
	// after a failed deployment, so we want the first complete release
	// If the most recent release is complete, this is likely a manual rollback,
	// so we want the second complete release (to go back one step)
	mostRecentIsFailed := len(releases) > 0 && (releases[0].Status != "complete" || releases[0].DockerImage == "")

	if mostRecentIsFailed {
		slog.Info("Most recent release is failed/incomplete - auto-rollback scenario, will return first complete release")
	} else {
		slog.Info("Most recent release is complete - manual rollback scenario, will skip first complete and return second")
	}

	// Releases are sorted newest first
	foundFirstComplete := false
	for i, rel := range releases {
		slog.Info("Checking release", "index", i, "version", rel.Version, "status", rel.Status, "image", rel.DockerImage)

		// Skip non-complete releases (including failed ones)
		if rel.Status != "complete" || rel.DockerImage == "" {
			slog.Info("Skipping incomplete release", "version", rel.Version, "status", rel.Status)
			continue
		}

		// If most recent is failed (auto-rollback), return the first complete release
		if mostRecentIsFailed {
			slog.Info("Found previous release for auto-rollback (first complete)", "version", rel.Version, "image", rel.DockerImage, "status", rel.Status)
			return &deployment.DeploymentInfo{
				ID:        rel.DockerImage,
				Status:    rel.Status,
				CreatedAt: rel.Date,
			}, nil
		}

		// Otherwise (manual rollback), skip the first complete and return the second
		if !foundFirstComplete {
			slog.Info("Skipping most recent complete release for manual rollback", "version", rel.Version, "image", rel.DockerImage)
			foundFirstComplete = true
			continue
		}

		slog.Info("Found previous release for manual rollback (second complete)", "version", rel.Version, "image", rel.DockerImage, "status", rel.Status)
		return &deployment.DeploymentInfo{
			ID:        rel.DockerImage,
			Status:    rel.Status,
			CreatedAt: rel.Date,
		}, nil
	}

	return nil, errors.Errorf("no previous release found for app %s", fqd.spec.Name)
}

func (fqd *FlyioQueuedDeployment) Rollback(ctx context.Context, targetDeploymentID string) error {
	if fqd.spec.Name == "" {
		return errors.Errorf("no app name available for rollback")
	}

	appName := fqd.spec.Name

	slog.Info("Rolling back Fly.io release", "app", appName, "targetImage", targetDeploymentID)

	err := fqd.client.DeployImage(ctx, appName, targetDeploymentID)
	if err != nil {
		return errors.Errorf("failed to rollback to image %s: %w", targetDeploymentID, err)
	}

	slog.Info("Release rolled back successfully", "targetImage", targetDeploymentID)

	return nil
}
