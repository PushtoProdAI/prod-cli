package render

import (
	"fmt"

	"github.com/meroxa/prod/cli/internal/deployment"
)

type RenderDeploymentAdapter struct {
	client          RenderClient
	dockerGenerator *deployment.DockerGenerator
}

func NewRenderDeploymentAdapter(client RenderClient) *RenderDeploymentAdapter {
	return &RenderDeploymentAdapter{
		client:          client,
		dockerGenerator: deployment.NewDockerGenerator(),
	}
}

func (rda *RenderDeploymentAdapter) SupportedStrategies() []deployment.DeploymentStrategy {
	return []deployment.DeploymentStrategy{
		deployment.StrategyRenderBlueprint,
		deployment.StrategyRenderQueued,
	}
}

func (rda *RenderDeploymentAdapter) GenerateArtifacts(spec *deployment.DeploymentSpec, strategy deployment.DeploymentStrategy) (deployment.Deployable, error) {
	// Determine whether to use Docker based on language support and strategy
	useDockerfile := rda.shouldUseDockerfile(spec, strategy)

	switch strategy {
	case deployment.StrategyRenderBlueprint:
		return NewBlueprintDeployment(rda.client, spec, rda.dockerGenerator, useDockerfile), nil
	case deployment.StrategyRenderQueued:
		return NewQueuedDeployment(rda.client, spec, rda.dockerGenerator, useDockerfile), nil
	default:
		return nil, fmt.Errorf("unsupported strategy: %s", strategy)
	}
}

// shouldUseDockerfile determines whether to use Docker based on various factors
func (rda *RenderDeploymentAdapter) shouldUseDockerfile(spec *deployment.DeploymentSpec, _ deployment.DeploymentStrategy) bool {
	// For now, use a simple heuristic: 
	// - Use Docker if the language has good native Render support
	// - Or if there are complex service dependencies
	// - Or if custom build/start commands suggest complex setup
	
	// Languages with good native Render support
	nativeLanguages := map[string]bool{
		"node":       true,
		"nodejs":     true,
		"javascript": true,
		"python":     true,
		"go":         true,
		"golang":     true,
	}
	
	hasNativeSupport := nativeLanguages[spec.Language]
	hasComplexServices := len(spec.Services) > 1
	hasCustomCommands := spec.BuildCommand != "" || spec.StartCommand != ""
	
	// Use Docker if:
	// - Language doesn't have native support, OR
	// - Has complex service dependencies, OR  
	// - Has custom build commands that might be complex
	return !hasNativeSupport || hasComplexServices || hasCustomCommands
}