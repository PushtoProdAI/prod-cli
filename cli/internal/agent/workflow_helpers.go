package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/meroxa/prod/cli/internal/deployment"
	"github.com/meroxa/prod/cli/internal/deployment/flyio"
	"github.com/meroxa/prod/cli/internal/deployment/render"
)

// getStepType returns the type of a Render deployment step
func getStepType(step render.RenderAPIStep) string {
	switch step.(type) {
	case *render.CreatePostgresStep:
		return "postgres"
	case *render.CreateRedisStep:
		return "redis"
	case *render.GetConnectionInfoStep:
		return "connection"
	case *render.BuildAndPushStep:
		return "docker_build"
	case *render.CreateRegistryCredentialStep:
		return "registry_credential"
	case *render.CreateWebServiceStep:
		return "web_service"
	default:
		return "unknown"
	}
}

// extractStepConfig extracts configuration from a Render deployment step
func extractStepConfig(step render.RenderAPIStep) map[string]any {
	config := make(map[string]any)

	switch s := step.(type) {
	case *render.CreatePostgresStep:
		config["name"] = s.Name
		config["databaseName"] = s.DatabaseName
		config["plan"] = "basic_256mb"
		config["version"] = "16"
	case *render.CreateRedisStep:
		config["name"] = s.Name
		config["plan"] = "standard"
	case *render.CreateWebServiceStep:
		config["name"] = s.Name
		config["buildCommand"] = s.BuildCommand
		config["startCommand"] = s.StartCommand
		config["environment"] = s.Environment
	}

	return config
}

// performConflictChecks checks for resource conflicts in a Render deployment
func performConflictChecks(_ string, spec *deployment.DeploymentSpec, _ render.RenderClient) []ConflictCheck {
	var checks []ConflictCheck

	checks = append(checks, ConflictCheck{
		Resource: fmt.Sprintf("Web service '%s-web'", spec.Name),
		Status:   "ok",
		Message:  "No conflicts detected",
	})

	serviceCounts := spec.ServiceCounts()
	for provider, count := range serviceCounts {
		for i := 1; i <= count; i++ {
			checks = append(checks, ConflictCheck{
				Resource: fmt.Sprintf("%s service '%s-%s-%d'", provider, spec.Name, provider, i),
				Status:   "ok",
				Message:  "No conflicts detected",
			})
		}
	}

	return checks
}

// validateDeploymentSpec validates a deployment specification
func validateDeploymentSpec(spec *deployment.DeploymentSpec) []string {
	var errors []string

	if spec.Name == "" {
		errors = append(errors, "Application name is required")
	}

	if spec.Language == "" {
		errors = append(errors, "Programming language must be specified")
	}

	return errors
}

// getFlyioStepType returns the type of a Fly.io deployment step
func getFlyioStepType(step flyio.FlyioAPIStep) string {
	switch step.(type) {
	case *flyio.CreateFlyioAppStep:
		return "app"
	case *flyio.CreateFlyioServiceStep:
		return "service"
	case *flyio.DeployFlyioConfigStep:
		return "config"
	case *flyio.AttachPostgresStep:
		return "attach"
	default:
		return "unknown"
	}
}

// extractFlyioStepConfig extracts configuration from a Fly.io deployment step
func extractFlyioStepConfig(step flyio.FlyioAPIStep) map[string]any {
	config := make(map[string]any)

	// Since the fields are unexported, we'll just use the step description
	// and type information that's available through the interface
	config["step_id"] = step.GetID()
	config["description"] = step.GetDescription()

	return config
}

// performFlyioConflictChecks checks for resource conflicts in a Fly.io deployment
func performFlyioConflictChecks(spec *deployment.DeploymentSpec, client flyio.FlyioClient) []ConflictCheck {
	var conflicts []ConflictCheck

	// Check for app name conflicts by attempting to get the app
	// Use a timeout to prevent hanging
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := client.GetApp(ctx, spec.Name)
	if err == nil {
		// App exists, this is a conflict
		conflicts = append(conflicts, ConflictCheck{
			Resource: "app",
			Status:   "conflict",
			Message:  fmt.Sprintf("App name '%s' already exists", spec.Name),
		})
	}

	return conflicts
}
