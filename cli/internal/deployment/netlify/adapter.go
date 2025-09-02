package netlify

import (
	"fmt"
	"io"
	"log"

	"github.com/meroxa/prod/cli/internal/deployment"
	"github.com/meroxa/prod/cli/internal/output"
)

// NetlifyDeploymentAdapter implements the DeploymentAdapter interface for Netlify
type NetlifyDeploymentAdapter struct {
	client NetlifyClient
	writer io.Writer
}

// NewNetlifyDeploymentAdapter creates a new Netlify deployment adapter
func NewNetlifyDeploymentAdapter(client NetlifyClient, writer io.Writer) *NetlifyDeploymentAdapter {
	if writer == nil {
		writer = output.NewNoOpWriter()
	}
	return &NetlifyDeploymentAdapter{
		client: client,
		writer: writer,
	}
}

// NewDefaultNetlifyDeploymentAdapter creates a deployment adapter with the default CLI client
func NewDefaultNetlifyDeploymentAdapter(writer io.Writer) *NetlifyDeploymentAdapter {
	return NewNetlifyDeploymentAdapter(NewCLINetlifyClient(), writer)
}

// SupportedStrategies returns the deployment strategies supported by Netlify
func (n *NetlifyDeploymentAdapter) SupportedStrategies() []deployment.DeploymentStrategy {
	return []deployment.DeploymentStrategy{
		deployment.StrategyNetlify,
	}
}

// GenerateArtifacts generates deployment artifacts for the specified strategy
func (n *NetlifyDeploymentAdapter) GenerateArtifacts(spec *deployment.DeploymentSpec, strategy deployment.DeploymentStrategy) (deployment.Deployable, error) {
	// Validate that this is appropriate for Netlify
	if err := n.validateSpec(spec); err != nil {
		return nil, err
	}

	// Netlify only supports static deployments
	if strategy != deployment.StrategyNetlify {
		return nil, fmt.Errorf("unsupported strategy for Netlify: %s", strategy)
	}

	// Use the queued deployment pattern for better visibility and control
	return NewNetlifyQueuedDeployment(n.client, spec, n.writer), nil
}

// EstimateCost estimates the cost of deployment on Netlify
func (n *NetlifyDeploymentAdapter) EstimateCost(spec *deployment.DeploymentSpec, strategy deployment.DeploymentStrategy) (deployment.CostEstimate, error) {
	log.Printf("Estimating costs for Netlify deployment: %+v\n", spec)

	// Netlify's free tier is quite generous for static sites
	ce := deployment.CostEstimate{
		Services: []deployment.CostService{},
		Total:    0.0, // Free tier
	}

	// Add a note about the free tier limits
	webService := deployment.CostService{
		Service: deployment.Service{
			Name:     "static-site",
			Provider: "netlify",
		},
		Plan: "free",
		Cost: 0.0,
	}

	ce.Services = append(ce.Services, webService)

	// Pro plan starts at $19/month per member if they exceed free tier
	// Free tier includes:
	// - 100GB bandwidth
	// - 300 build minutes
	// - Unlimited static sites

	return ce, nil
}

// validateSpec validates that the deployment spec is suitable for Netlify
func (n *NetlifyDeploymentAdapter) validateSpec(spec *deployment.DeploymentSpec) error {
	// Check for unsupported services
	for _, service := range spec.Services {
		switch service.Provider {
		case "postgresql", "redis", "mysql", "mongodb":
			return fmt.Errorf("Netlify does not support %s hosting. Netlify is for static sites and serverless functions only", service.Provider)
		}
	}

	// Check if this appears to be a backend service
	if spec.StartCommand != "" {
		// Check if it's a static site generator
		knownStaticCommands := []string{"next export", "nuxt generate", "gatsby build", "hugo", "jekyll build"}
		isStatic := false
		for _, cmd := range knownStaticCommands {
			if spec.StartCommand == cmd || spec.BuildCommand == cmd {
				isStatic = true
				break
			}
		}

		if !isStatic && spec.StartCommand != "" {
			log.Printf("Warning: Netlify is designed for static sites. Start command '%s' suggests a backend service which won't work on Netlify", spec.StartCommand)
		}
	}

	return nil
}
