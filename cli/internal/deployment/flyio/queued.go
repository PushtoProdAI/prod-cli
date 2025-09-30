package flyio

import (
	"context"
	"fmt"
	"io"

	"github.com/go-errors/errors"

	"github.com/meroxa/prod/cli/internal/deployment"
)

// FlyioQueuedDeployment handles step-by-step deployments to Fly.io
// This deployment strategy creates resources one at a time with progress tracking
type FlyioQueuedDeployment struct {
	client FlyioClient
	spec   *deployment.DeploymentSpec
	writer io.Writer
}

// NewFlyioQueuedDeployment creates a new queued deployment for Fly.io
func NewFlyioQueuedDeployment(client FlyioClient, spec *deployment.DeploymentSpec, writer io.Writer) *FlyioQueuedDeployment {
	return &FlyioQueuedDeployment{
		client: client,
		spec:   spec,
		writer: writer,
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

	// Step 1: Create backing services first (they're independent apps)
	for i, service := range fqd.spec.Services {
		stepID := fmt.Sprintf("create-service-%d", i)
		step := fqd.createServiceStep(service, stepID)
		if step != nil {
			steps = append(steps, step)
			serviceStepIDs = append(serviceStepIDs, stepID)
		}
	}

	// Step 2: Create main app
	appName := fqd.spec.Name
	appStepID := "create-app"
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

	// Step 4: Deploy app configuration (after attachments are complete)
	deployDeps := []string{appStepID}
	deployDeps = append(deployDeps, attachmentStepIDs...)

	steps = append(steps, &DeployFlyioConfigStep{
		BaseStep: BaseStep{
			ID:          "deploy-config",
			Description: "Deploying app configuration",
			DependsOn:   deployDeps,
		},
		appName: appName,
		config:  fqd.generateFlyConfig(),
	})

	return steps
}

// createServiceStep creates a deployment step for a service
func (fqd *FlyioQueuedDeployment) createServiceStep(service deployment.Service, stepID string) FlyioAPIStep {
	switch service.Provider {
	case "postgresql":
		return &CreateFlyioServiceStep{
			BaseStep: BaseStep{
				ID:          stepID,
				Description: fmt.Sprintf("Creating PostgreSQL database: %s", service.Name),
			},
			serviceType: "postgres",
			name:        fmt.Sprintf("%s-postgres", fqd.spec.Name),
			region:      defaultRegion,
			size:        postgresVolumeSizeGB,
		}
	case "redis":
		return &CreateFlyioServiceStep{
			BaseStep: BaseStep{
				ID:          stepID,
				Description: fmt.Sprintf("Creating Redis database: %s", service.Name),
			},
			serviceType: "redis",
			name:        fmt.Sprintf("%s-redis", fqd.spec.Name),
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
		for _, v := range fqd.spec.EnvVars {
			if v.Role == deployment.EnvRoleFullURI && v.Service == "postgresql" {
				pgURLVar = v.Name
			}
		}
		return &AttachPostgresStep{
			BaseStep: BaseStep{
				ID:          stepID,
				Description: fmt.Sprintf("Attaching PostgreSQL to app: %s", appName),
				DependsOn:   []string{appStepID, serviceStepID}, // Depends on both app and service creation
			},
			appName:       appName,
			variableName:  pgURLVar,
			serviceStepID: serviceStepID, // Pass the service step ID to retrieve cluster ID
		}
	case "redis":
		return &AttachRedisStep{
			BaseStep: BaseStep{
				ID:          stepID,
				Description: fmt.Sprintf("Attaching Redis to app: %s", appName),
				DependsOn:   []string{appStepID, serviceStepID}, // Depends on both app and service creation
			},
			appName:      appName,
			redisName:    fmt.Sprintf("%s-redis", fqd.spec.Name),
			variableName: "REDIS_URL",
		}
	default:
		// Don't create attachment steps for unsupported services
		return nil
	}
}

// generateFlyConfig generates the Fly.io configuration
func (fqd *FlyioQueuedDeployment) generateFlyConfig() *FlyioConfig {
	envVars := make(map[string]string)

	for _, ev := range fqd.spec.EnvVars {
		if ev.IsNotDBRelated() && ev.Value != "" {
			envVars[ev.Name] = ev.Value
		}
	}

	config := &FlyioConfig{
		AppName:        fqd.spec.Name,
		ReleaseCommand: fqd.spec.MigrationCommand,
		EnvVars:        envVars,
	}

	// Set source path if available in metadata
	if sourcePath, ok := fqd.spec.Metadata["buildContext"].(string); ok {
		config.SourcePath = sourcePath
	}

	// Add build configuration based on language
	config.BuildConfig = &BuildConfig{
		Builder:  fqd.getBuilderForLanguage(fqd.spec.Language),
		BuildCmd: fqd.spec.BuildCommand,
		StartCmd: fqd.spec.StartCommand,
	}

	// Add service configuration
	config.Services = []ServiceConfig{
		{
			Protocol:     "tcp",
			InternalPort: fqd.getInternalPortForLanguage(fqd.spec.Language),
			Ports: []Port{
				{Port: 80, Handlers: []string{"http"}},
				{Port: 443, Handlers: []string{"tls", "http"}},
			},
		},
	}
	return config
}

// getBuilderForLanguage returns the appropriate Fly.io builder for the given language
func (fqd *FlyioQueuedDeployment) getBuilderForLanguage(language string) string {
	config := GetLanguageConfig(language)
	return config.Builder
}

// getInternalPortForLanguage returns the default internal port for the given language
func (fqd *FlyioQueuedDeployment) getInternalPortForLanguage(language string) int {
	config := GetLanguageConfig(language)
	return config.InternalPort
}
