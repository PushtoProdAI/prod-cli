package render

import (
	"context"
	"fmt"

	"github.com/meroxa/prod/cli/internal/deployment"
	"github.com/meroxa/prod/cli/internal/output"
)

type BlueprintDeployment struct {
	client          RenderClient
	spec            *deployment.DeploymentSpec
	dockerGenerator *deployment.DockerGenerator
	useDockerfile   bool
	writer          output.UnifiedOutputWriter
}

func NewBlueprintDeployment(client RenderClient, spec *deployment.DeploymentSpec, dockerGenerator *deployment.DockerGenerator, useDockerfile bool, writer output.UnifiedOutputWriter) *BlueprintDeployment {
	if writer == nil {
		writer = output.NewNoOpWriter()
	}
	return &BlueprintDeployment{
		client:          client,
		spec:            spec,
		dockerGenerator: dockerGenerator,
		useDockerfile:   useDockerfile,
		writer:          writer,
	}
}

func (bd *BlueprintDeployment) Deploy(ctx context.Context) ([]deployment.CreatedResource, error) {
	// TODO: have the blueprint deployment return the created resources
	if bd.useDockerfile && bd.dockerGenerator != nil {
		return []deployment.CreatedResource{}, bd.deployWithDockerfile(ctx)
	}
	return []deployment.CreatedResource{}, bd.deployWithNativeBlueprint(ctx)
}

func (bd *BlueprintDeployment) deployWithDockerfile(ctx context.Context) error {
	// Generate Dockerfile and Docker Compose
	dockerArtifacts, err := bd.dockerGenerator.GenerateDockerfile(bd.spec)
	if err != nil {
		return fmt.Errorf("failed to generate Docker artifacts: %w", err)
	}

	// Create Render blueprint with Dockerfile
	blueprint := bd.createDockerBlueprint(dockerArtifacts)

	// Deploy blueprint to Render
	return bd.client.DeployBlueprint(ctx, blueprint)
}

func (bd *BlueprintDeployment) deployWithNativeBlueprint(ctx context.Context) error {
	// Create native Render blueprint without Docker
	blueprint := bd.createNativeBlueprint()
	// Deploy blueprint to Render
	return bd.client.DeployBlueprint(ctx, blueprint)
}

func (bd *BlueprintDeployment) createDockerBlueprint(artifacts *deployment.DockerArtifacts) *RenderBlueprint {
	services := make([]BlueprintService, 0)

	// Add main web service using Dockerfile
	webService := BlueprintService{
		Name:         fmt.Sprintf("%s-web", bd.spec.Name),
		Type:         "web_service",
		Env:          "docker",
		Repo:         ".", // Assumes source is in current directory
		Dockerfile:   artifacts.Dockerfile,
		BuildCommand: "", // Docker handles the build
		StartCommand: "", // Docker handles the start
		EnvVars:      make(map[string]string),
	}

	// Add environment variables for backing services
	for provider := range bd.spec.ServiceCounts() {
		switch provider {
		case "postgresql":
			webService.EnvVars["DATABASE_URL"] = "${postgres.DATABASE_URL}"
		case "redis":
			webService.EnvVars["REDIS_URL"] = "${redis.REDIS_URL}"
		}
	}

	services = append(services, webService)

	// Add backing services
	for provider := range bd.spec.ServiceCounts() {
		switch provider {
		case "postgresql":
			services = append(services, BlueprintService{
				Name:         fmt.Sprintf("%s-postgres", bd.spec.Name),
				Type:         "postgres",
				DatabaseName: fmt.Sprintf("%s_db", bd.spec.Name),
			})
		case "redis":
			services = append(services, BlueprintService{
				Name: fmt.Sprintf("%s-redis", bd.spec.Name),
				Type: "redis",
			})
		}
	}

	return &RenderBlueprint{
		Services: services,
	}
}

func (bd *BlueprintDeployment) createNativeBlueprint() *RenderBlueprint {
	services := make([]BlueprintService, 0)

	// Add main web service using native Render detection
	webService := BlueprintService{
		Name:         fmt.Sprintf("%s-web", bd.spec.Name),
		Type:         "web_service",
		Env:          bd.getEnvironmentForLanguage(bd.spec.Language),
		Repo:         ".", // Assumes source is in current directory
		BuildCommand: bd.spec.BuildCommand,
		StartCommand: bd.spec.StartCommand,
		EnvVars:      make(map[string]string),
	}

	// Add environment variables for backing services
	for provider := range bd.spec.ServiceCounts() {
		switch provider {
		case "postgresql":
			webService.EnvVars["DATABASE_URL"] = "${postgres.DATABASE_URL}"
		case "redis":
			webService.EnvVars["REDIS_URL"] = "${redis.REDIS_URL}"
		}
	}

	services = append(services, webService)

	// Add backing services
	for provider := range bd.spec.ServiceCounts() {
		switch provider {
		case "postgresql":
			services = append(services, BlueprintService{
				Name:         fmt.Sprintf("%s-postgres", bd.spec.Name),
				Type:         "postgres",
				DatabaseName: fmt.Sprintf("%s_db", bd.spec.Name),
			})
		case "redis":
			services = append(services, BlueprintService{
				Name: fmt.Sprintf("%s-redis", bd.spec.Name),
				Type: "redis",
			})
		}
	}

	return &RenderBlueprint{
		Services: services,
	}
}

func (bd *BlueprintDeployment) getEnvironmentForLanguage(language string) string {
	switch language {
	case "node", "nodejs", "javascript":
		return "node"
	case "python":
		return "python3"
	case "go", "golang":
		return "go"
	default:
		return "docker" // Default to docker for unsupported languages
	}
}
