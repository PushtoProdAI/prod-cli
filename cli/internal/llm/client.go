package llm

import (
	"context"
	"os"

	baml "github.com/boundaryml/baml/engine/language_client_go/pkg"
	"github.com/go-errors/errors"
	"github.com/pushtoprodai/prod-cli/baml_client"
	"github.com/pushtoprodai/prod-cli/baml_client/types"
	"github.com/pushtoprodai/prod-cli/internal/config"
)

// Client provides a high-level interface for LLM operations with automatic proxy configuration.
// It abstracts away the details of session handling and proxy routing.
type Client interface {
	// Planning operations
	ExtractIntent(ctx context.Context, prompt string) (types.Intent, error)
	SummarizeIntent(ctx context.Context, intent types.Intent, name, language string, detectedPlatforms []string) (types.Summary, error)
	DetermineLaunchCommand(ctx context.Context, language string, frameworks, envVars []string, lc types.LaunchContext) (types.LaunchCommand, error)
	DetermineMigrationCommand(ctx context.Context, language string, frameworks, ormTools []string, migrationContext types.MigrationContext) (types.MigrationCommand, error)

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
func (c *client) getCallOptions(ctx context.Context, functionName string) []baml_client.CallOptionFunc {
	if sess := c.config.SessionExtractor(ctx); sess != nil {
		return []baml_client.CallOptionFunc{
			baml_client.WithEnv(map[string]string{
				"PROXY_API_KEY":      sess.GetAccessToken(),
				"SUPABASE_URL":       c.config.ProxyURL,
				"BAML_FUNCTION_NAME": functionName,
			}),
		}
	}
	// No backend session → local (BYO-keys) mode. Select a direct LLM client at
	// call time via a ClientRegistry, so we bypass the ProxyClient default
	// without editing the generated BAML client.
	return []baml_client.CallOptionFunc{
		baml_client.WithClientRegistry(directRegistry(os.Getenv)),
	}
}

// directClient describes a directly-configured (BYO-keys) LLM client used in
// local mode when there's no backend session to proxy through.
type directClient struct {
	name     string
	provider string
	options  map[string]any
}

// selectDirectClient picks a direct LLM provider from the environment in
// priority order: OpenAI, then Anthropic, then a local Ollama fallback (so the
// tool still works with zero cloud keys if Ollama is running locally).
// getenv is injected for testability. Model can be overridden with PROD_LLM_MODEL.
func selectDirectClient(getenv func(string) string) directClient {
	model := getenv("PROD_LLM_MODEL")

	if key := getenv("OPENAI_API_KEY"); key != "" {
		if model == "" {
			model = "gpt-4o"
		}
		return directClient{
			name:     "prod-direct-openai",
			provider: "openai",
			options:  map[string]any{"model": model, "api_key": key},
		}
	}

	if key := getenv("ANTHROPIC_API_KEY"); key != "" {
		if model == "" {
			model = "claude-3-5-sonnet-20241022"
		}
		return directClient{
			name:     "prod-direct-anthropic",
			provider: "anthropic",
			options:  map[string]any{"model": model, "api_key": key},
		}
	}

	// Fallback: local Ollama. No cloud keys required.
	baseURL := getenv("OLLAMA_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:11434/v1"
	}
	if model == "" {
		model = "llama3.1"
	}
	return directClient{
		name:     "prod-direct-ollama",
		provider: "openai-generic",
		options:  map[string]any{"base_url": baseURL, "model": model},
	}
}

// directRegistry builds a BAML ClientRegistry that overrides the default
// ProxyClient with a directly-configured provider for local (BYO-keys) mode.
func directRegistry(getenv func(string) string) *baml.ClientRegistry {
	dc := selectDirectClient(getenv)
	cr := baml.NewClientRegistry()
	cr.AddLlmClient(dc.name, dc.provider, dc.options)
	cr.SetPrimaryClient(dc.name)
	return cr
}

// ExtractIntent extracts user intent from a prompt.
func (c *client) ExtractIntent(ctx context.Context, prompt string) (types.Intent, error) {
	opts := c.getCallOptions(ctx, "ExtractIntent")
	intent, err := baml_client.ExtractIntent(ctx, prompt, opts...)
	if err != nil {
		return types.Intent{}, errors.Errorf("failed to extract intent: %w", err)
	}
	return intent, nil
}

// SummarizeIntent creates a summary of the user's intent.
func (c *client) SummarizeIntent(ctx context.Context, intent types.Intent, name, language string, detectedPlatforms []string) (types.Summary, error) {
	opts := c.getCallOptions(ctx, "SummarizeIntent")
	summary, err := baml_client.SummarizeIntent(ctx, intent, name, language, detectedPlatforms, opts...)
	if err != nil {
		return types.Summary{}, errors.Errorf("failed to summarize intent: %w", err)
	}
	return summary, nil
}

// DetermineLaunchCommand determines the appropriate launch command for a project.
func (c *client) DetermineLaunchCommand(ctx context.Context, language string, frameworks, envVars []string, lc types.LaunchContext) (types.LaunchCommand, error) {
	opts := c.getCallOptions(ctx, "DetermineLaunchCommand")
	cmd, err := baml_client.DetermineLaunchCommand(ctx, language, frameworks, envVars, lc, opts...)
	if err != nil {
		return types.LaunchCommand{}, errors.Errorf("failed to determine launch command: %w", err)
	}
	return cmd, nil
}

// DetermineMigrationCommand determines the appropriate migration command for a project.
func (c *client) DetermineMigrationCommand(ctx context.Context, language string, frameworks, ormTools []string, migrationContext types.MigrationContext) (types.MigrationCommand, error) {
	opts := c.getCallOptions(ctx, "DetermineMigrationCommand")
	cmd, err := baml_client.DetermineMigrationCommand(ctx, language, frameworks, ormTools, migrationContext, opts...)
	if err != nil {
		return types.MigrationCommand{}, errors.Errorf("failed to determine migration command: %w", err)
	}
	return cmd, nil
}

// SummarizeSteps creates a summary of deployment steps.
func (c *client) SummarizeSteps(ctx context.Context, steps []string) (types.Summary, error) {
	opts := c.getCallOptions(ctx, "SummarizeSteps")
	summary, err := baml_client.SummarizeSteps(ctx, steps, opts...)
	if err != nil {
		return types.Summary{}, errors.Errorf("failed to summarize steps: %w", err)
	}
	return summary, nil
}

// DetermineEnvVarRoles categorizes environment variables by their roles.
func (c *client) DetermineEnvVarRoles(ctx context.Context, ev types.EnvVarCandidate, dbList []string) (types.EnvVarCategory, error) {
	opts := c.getCallOptions(ctx, "DetermineEnvVarRoles")
	role, err := baml_client.DetermineEnvVarRoles(ctx, ev, dbList, opts...)
	if err != nil {
		return types.EnvVarCategory{}, errors.Errorf("failed to determine env var roles: %w", err)
	}
	return role, nil
}

// DetermineBuildOutput determines the build output path.
func (c *client) DetermineBuildOutput(ctx context.Context, bo types.BuildOutputCandidate) (types.BuildOutput, error) {
	opts := c.getCallOptions(ctx, "DetermineBuildOutput")
	output, err := baml_client.DetermineBuildOutput(ctx, bo, opts...)
	if err != nil {
		return types.BuildOutput{}, errors.Errorf("failed to determine build output: %w", err)
	}
	return output, nil
}

// SummarizeDeployError creates a summary and remediation for deployment errors.
func (c *client) SummarizeDeployError(ctx context.Context, error string, intent types.Intent, spec types.ProjectSpec, os string, violations []string) (types.Error, error) {
	opts := c.getCallOptions(ctx, "SummarizeDeployError")
	summary, err := baml_client.SummarizeDeployError(ctx, error, intent, spec, os, violations, opts...)
	if err != nil {
		return types.Error{}, errors.Errorf("failed to summarize deploy error: %w", err)
	}
	return summary, nil
}

// CategorizeRoutes analyzes and categorizes application routes.
func (c *client) CategorizeRoutes(ctx context.Context, routes []types.RouteCandidate) (types.CategorizedRoutes, error) {
	opts := c.getCallOptions(ctx, "CategorizeRoutes")
	analysis, err := baml_client.CategorizeRoutes(ctx, routes, opts...)
	if err != nil {
		return types.CategorizedRoutes{}, errors.Errorf("failed to categorize routes: %w", err)
	}
	return analysis, nil
}

// FetchPricing retrieves pricing information for a service.
func (c *client) FetchPricing(ctx context.Context, service types.Service, content string) (types.ServicePricing, error) {
	opts := c.getCallOptions(ctx, "FetchPricing")
	pricing, err := baml_client.FetchPricing(ctx, service, content, opts...)
	if err != nil {
		return types.ServicePricing{}, errors.Errorf("failed to fetch pricing: %w", err)
	}
	return pricing, nil
}

// Compile-time interface compliance check
var _ Client = (*client)(nil)
