package llm

import (
	"context"

	"github.com/go-errors/errors"
	"github.com/meroxa/prod/cli/baml_client"
	"github.com/meroxa/prod/cli/baml_client/types"
	"github.com/meroxa/prod/cli/internal/config"
)

// Client provides a high-level interface for LLM operations with automatic proxy configuration.
// It abstracts away the details of session handling and proxy routing.
type Client interface {
	// Planning operations
	ExtractIntent(ctx context.Context, prompt string) (types.Intent, error)
	SummarizeIntent(ctx context.Context, intent types.Intent, name, language string) (types.Summary, error)
	DetermineLaunchCommand(ctx context.Context, language string, frameworks, envVars []string, lc types.LaunchContext) (types.LaunchCommand, error)

	// Deployment operations
	SummarizeSteps(ctx context.Context, steps []string) (types.Summary, error)
	DetermineEnvVarRoles(ctx context.Context, ev types.EnvVarCandidate, dbList []string) (types.EnvVarCategory, error)
	DetermineBuildOutput(ctx context.Context, bo types.BuildOutputCandidate) (types.BuildOutput, error)
	SummarizeDeployError(ctx context.Context, error string, intent types.Intent, spec types.ProjectSpec, os string, violations []string) (types.Error, error)

	// Monitoring operations
	CategorizeRoutes(ctx context.Context, routes []types.RouteCandidate) (types.CategorizedRoutes, error)

	// Pricing operations
	FetchPricing(ctx context.Context, service types.Service, content string) (types.ServicePricing, error)
}

// SessionProvider defines how to extract authentication information from context.
// This allows the client to work with different session management approaches.
type SessionProvider interface {
	GetAccessToken() string
}

// SessionExtractor is a function that extracts session information from context.
type SessionExtractor func(ctx context.Context) SessionProvider

// Config holds the configuration for the LLM client.
type Config struct {
	// ProxyURL is the base URL for the LLM proxy service.
	// If empty, direct LLM calls will be made.
	ProxyURL string

	// SessionExtractor is a function to extract session from context.
	// If nil, uses simple context.Value("session") lookup.
	SessionExtractor SessionExtractor
}

// client implements the Client interface with automatic proxy configuration.
type client struct {
	config Config
}

// New creates a new LLM client with the given configuration.
func New(cfg Config) Client {
	if cfg.ProxyURL == "" {
		cfg.ProxyURL = config.GetSupabaseURL() + "/functions/v1/llm-proxy"
	}
	if cfg.SessionExtractor == nil {
		// Default to simple context value lookup
		cfg.SessionExtractor = func(ctx context.Context) SessionProvider {
			if sessValue := ctx.Value("session"); sessValue != nil {
				if sess, ok := sessValue.(SessionProvider); ok {
					return sess
				}
			}
			return nil
		}
	}
	return &client{config: cfg}
}

// NewWithClient creates a new LLM client that wraps an existing client.
// This is useful for dependency injection in testing scenarios.
func NewWithClient(client Client) Client {
	return client
}

// NewDefault creates a new LLM client with default configuration.
func NewDefault() Client {
	return New(Config{})
}

// getCallOptions extracts session information from context and returns appropriate BAML call options.
func (c *client) getCallOptions(ctx context.Context) []baml_client.CallOptionFunc {
	if sess := c.config.SessionExtractor(ctx); sess != nil {
		return []baml_client.CallOptionFunc{
			baml_client.WithEnv(map[string]string{
				"PROXY_API_KEY": sess.GetAccessToken(),
				"SUPABASE_URL":  c.config.ProxyURL,
			}),
		}
	}
	// Return empty options for direct LLM calls when no session is available
	return []baml_client.CallOptionFunc{}
}

// ExtractIntent extracts user intent from a prompt.
func (c *client) ExtractIntent(ctx context.Context, prompt string) (types.Intent, error) {
	opts := c.getCallOptions(ctx)
	intent, err := baml_client.ExtractIntent(ctx, prompt, opts...)
	if err != nil {
		return types.Intent{}, errors.Errorf("failed to extract intent: %w", err)
	}
	return intent, nil
}

// SummarizeIntent creates a summary of the user's intent.
func (c *client) SummarizeIntent(ctx context.Context, intent types.Intent, name, language string) (types.Summary, error) {
	opts := c.getCallOptions(ctx)
	summary, err := baml_client.SummarizeIntent(ctx, intent, name, language, opts...)
	if err != nil {
		return types.Summary{}, errors.Errorf("failed to summarize intent: %w", err)
	}
	return summary, nil
}

// DetermineLaunchCommand determines the appropriate launch command for a project.
func (c *client) DetermineLaunchCommand(ctx context.Context, language string, frameworks, envVars []string, lc types.LaunchContext) (types.LaunchCommand, error) {
	opts := c.getCallOptions(ctx)
	cmd, err := baml_client.DetermineLaunchCommand(ctx, language, frameworks, envVars, lc, opts...)
	if err != nil {
		return types.LaunchCommand{}, errors.Errorf("failed to determine launch command: %w", err)
	}
	return cmd, nil
}

// SummarizeSteps creates a summary of deployment steps.
func (c *client) SummarizeSteps(ctx context.Context, steps []string) (types.Summary, error) {
	opts := c.getCallOptions(ctx)
	summary, err := baml_client.SummarizeSteps(ctx, steps, opts...)
	if err != nil {
		return types.Summary{}, errors.Errorf("failed to summarize steps: %w", err)
	}
	return summary, nil
}

// DetermineEnvVarRoles categorizes environment variables by their roles.
func (c *client) DetermineEnvVarRoles(ctx context.Context, ev types.EnvVarCandidate, dbList []string) (types.EnvVarCategory, error) {
	opts := c.getCallOptions(ctx)
	role, err := baml_client.DetermineEnvVarRoles(ctx, ev, dbList, opts...)
	if err != nil {
		return types.EnvVarCategory{}, errors.Errorf("failed to determine env var roles: %w", err)
	}
	return role, nil
}

// DetermineBuildOutput determines the build output path.
func (c *client) DetermineBuildOutput(ctx context.Context, bo types.BuildOutputCandidate) (types.BuildOutput, error) {
	opts := c.getCallOptions(ctx)
	output, err := baml_client.DetermineBuildOutput(ctx, bo, opts...)
	if err != nil {
		return types.BuildOutput{}, errors.Errorf("failed to determine build output: %w", err)
	}
	return output, nil
}

// SummarizeDeployError creates a summary and remediation for deployment errors.
func (c *client) SummarizeDeployError(ctx context.Context, error string, intent types.Intent, spec types.ProjectSpec, os string, violations []string) (types.Error, error) {
	opts := c.getCallOptions(ctx)
	summary, err := baml_client.SummarizeDeployError(ctx, error, intent, spec, os, violations, opts...)
	if err != nil {
		return types.Error{}, errors.Errorf("failed to summarize deploy error: %w", err)
	}
	return summary, nil
}

// CategorizeRoutes analyzes and categorizes application routes.
func (c *client) CategorizeRoutes(ctx context.Context, routes []types.RouteCandidate) (types.CategorizedRoutes, error) {
	opts := c.getCallOptions(ctx)
	analysis, err := baml_client.CategorizeRoutes(ctx, routes, opts...)
	if err != nil {
		return types.CategorizedRoutes{}, errors.Errorf("failed to categorize routes: %w", err)
	}
	return analysis, nil
}

// FetchPricing retrieves pricing information for a service.
func (c *client) FetchPricing(ctx context.Context, service types.Service, content string) (types.ServicePricing, error) {
	opts := c.getCallOptions(ctx)
	pricing, err := baml_client.FetchPricing(ctx, service, content, opts...)
	if err != nil {
		return types.ServicePricing{}, errors.Errorf("failed to fetch pricing: %w", err)
	}
	return pricing, nil
}

// Compile-time interface compliance check
var _ Client = (*client)(nil)
