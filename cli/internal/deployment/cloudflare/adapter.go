package cloudflare

import (
	"context"
	"io"

	"github.com/go-errors/errors"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
	"github.com/pushtoprodai/prod-cli/internal/llm"
	"github.com/pushtoprodai/prod-cli/internal/output"
)

// CloudflareDeploymentAdapter implements deployment.DeploymentAdapter for Cloudflare Pages.
type CloudflareDeploymentAdapter struct {
	client    CloudflareClient
	writer    io.Writer
	llmClient llm.Client
}

// NewCloudflareDeploymentAdapter creates a Cloudflare Pages adapter with the given client.
func NewCloudflareDeploymentAdapter(client CloudflareClient, writer io.Writer, llmClient llm.Client) *CloudflareDeploymentAdapter {
	if writer == nil {
		writer = output.NewNoOpWriter()
	}
	return &CloudflareDeploymentAdapter{client: client, writer: writer, llmClient: llmClient}
}

// NewDefaultCloudflareDeploymentAdapter uses the default HTTP client (reads the user's
// CLOUDFLARE_API_TOKEN + CLOUDFLARE_ACCOUNT_ID).
func NewDefaultCloudflareDeploymentAdapter(writer io.Writer, llmClient llm.Client) *CloudflareDeploymentAdapter {
	return NewCloudflareDeploymentAdapter(NewHTTPCloudflareClient(), writer, llmClient)
}

// SupportedStrategies returns the deployment strategies supported by Cloudflare Pages.
func (a *CloudflareDeploymentAdapter) SupportedStrategies() []deployment.DeploymentStrategy {
	return []deployment.DeploymentStrategy{deployment.StrategyCloudflare}
}

// GenerateArtifacts returns the Cloudflare Pages deployable for a static site.
func (a *CloudflareDeploymentAdapter) GenerateArtifacts(spec *deployment.DeploymentSpec, strategy deployment.DeploymentStrategy) (deployment.Deployable, error) {
	if strategy != deployment.StrategyCloudflare {
		return nil, errors.Errorf("unsupported strategy for Cloudflare Pages: %s", strategy)
	}
	return NewCloudflareQueuedDeployment(a.client, spec, a.writer), nil
}

// EstimateCost returns a zero estimate — Cloudflare Pages' free tier covers static hosting
// (unlimited requests/bandwidth; the paid tier is for build minutes/concurrency, not hosting).
func (a *CloudflareDeploymentAdapter) EstimateCost(_ context.Context, _ *deployment.DeploymentSpec, _ deployment.DeploymentStrategy) (deployment.CostEstimate, error) {
	return deployment.CostEstimate{Total: 0}, nil
}
