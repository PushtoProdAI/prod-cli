package flyio

import (
	"fmt"

	"github.com/meroxa/prod/cli/internal/deployment"
)

// FlyioDeploymentAdapter implements the DeploymentAdapter interface for Fly.io
type FlyioDeploymentAdapter struct {
	client FlyioClient
}

// NewFlyioDeploymentAdapter creates a new Fly.io deployment adapter
func NewFlyioDeploymentAdapter(client FlyioClient) *FlyioDeploymentAdapter {
	return &FlyioDeploymentAdapter{
		client: client,
	}
}

// NewDefaultFlyioDeploymentAdapter creates a deployment adapter with the default client
func NewDefaultFlyioDeploymentAdapter() *FlyioDeploymentAdapter {
	return NewFlyioDeploymentAdapter(NewFlyioClient())
}

// SupportedStrategies returns the deployment strategies supported by Fly.io
func (fda *FlyioDeploymentAdapter) SupportedStrategies() []deployment.DeploymentStrategy {
	// Fly.io doesn't have blueprints - that's a Render concept
	// We only have one strategy: step-by-step deployment
	return []deployment.DeploymentStrategy{
		deployment.StrategyFlyio,
	}
}

// GenerateArtifacts generates deployment artifacts for the specified strategy
func (fda *FlyioDeploymentAdapter) GenerateArtifacts(spec *deployment.DeploymentSpec, strategy deployment.DeploymentStrategy) (deployment.Deployable, error) {
	// Fly.io only has one deployment approach - no blueprints
	if strategy != deployment.StrategyFlyio {
		return nil, fmt.Errorf("unsupported deployment strategy for Fly.io: %s (only %s is supported)", strategy, deployment.StrategyFlyio)
	}
	return NewFlyioQueuedDeployment(fda.client, spec), nil
}

// GenerateArtifactsWithSource generates deployment artifacts with source path
func (fda *FlyioDeploymentAdapter) GenerateArtifactsWithSource(spec *deployment.DeploymentSpec, strategy deployment.DeploymentStrategy, sourcePath string) (deployment.Deployable, error) {
	// Only one strategy supported now
	if strategy != deployment.StrategyFlyio {
		return nil, fmt.Errorf("unsupported deployment strategy for Fly.io: %s (only %s is supported)", strategy, deployment.StrategyFlyio)
	}
	
	// Pass the source path through metadata so it can be used during deployment
	if spec.Metadata == nil {
		spec.Metadata = make(map[string]any)
	}
	spec.Metadata["buildContext"] = sourcePath
	
	return NewFlyioQueuedDeployment(fda.client, spec), nil
}

// EstimateCost estimates the cost of deployment on Fly.io
func (fda *FlyioDeploymentAdapter) EstimateCost(spec *deployment.DeploymentSpec, strategy deployment.DeploymentStrategy) (deployment.CostEstimate, error) {
	// TODO: Implement cost estimation for Fly.io
	// This would need to query Fly.io pricing API or use static pricing data
	return deployment.CostEstimate{
		Total:    0.0,
		Services: []deployment.CostService{},
	}, nil
}

// Removed unused helper methods - these are now in blueprint.go and queued.go
// where they're actually used
