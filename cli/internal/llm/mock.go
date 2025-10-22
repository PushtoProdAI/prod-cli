package llm

import (
	"context"

	"github.com/meroxa/prod/cli/baml_client/types"
)

// MockClient provides a test implementation of the Client interface.
// Useful for unit testing without making actual LLM calls.
type MockClient struct {
	// Function implementations can be overridden for specific test scenarios
	ExtractIntentFunc             func(ctx context.Context, prompt string) (types.Intent, error)
	SummarizeIntentFunc           func(ctx context.Context, intent types.Intent, name, language string) (types.Summary, error)
	DetermineLaunchCommandFunc    func(ctx context.Context, language string, frameworks, envVars []string, lc types.LaunchContext) (types.LaunchCommand, error)
	DetermineMigrationCommandFunc func(ctx context.Context, language string, frameworks, ormTools []string, migrationContext types.MigrationContext) (types.MigrationCommand, error)
	SummarizeStepsFunc            func(ctx context.Context, steps []string) (types.Summary, error)
	DetermineEnvVarRolesFunc      func(ctx context.Context, ev types.EnvVarCandidate, dbList []string) (types.EnvVarCategory, error)
	DetermineBuildOutputFunc      func(ctx context.Context, bo types.BuildOutputCandidate) (types.BuildOutput, error)
	SummarizeDeployErrorFunc      func(ctx context.Context, error string, intent types.Intent, spec types.ProjectSpec, os string, violations []string) (types.Error, error)
	CategorizeRoutesFunc          func(ctx context.Context, routes []types.RouteCandidate) (types.CategorizedRoutes, error)
	FetchPricingFunc              func(ctx context.Context, service types.Service, content string) (types.ServicePricing, error)
}

// NewMockClient creates a new mock client with default implementations.
func NewMockClient() *MockClient {
	return &MockClient{
		ExtractIntentFunc: func(ctx context.Context, prompt string) (types.Intent, error) {
			return types.Intent{
				Action:   "deploy",
				Platform: "render",
				Source:   "/path/to/project",
			}, nil
		},
		SummarizeIntentFunc: func(ctx context.Context, intent types.Intent, name, language string) (types.Summary, error) {
			return types.Summary{
				Summary: "Mock summary of intent",
			}, nil
		},
		DetermineLaunchCommandFunc: func(ctx context.Context, language string, frameworks, envVars []string, lc types.LaunchContext) (types.LaunchCommand, error) {
			return types.LaunchCommand{
				Command: "npm start",
			}, nil
		},
		DetermineMigrationCommandFunc: func(ctx context.Context, language string, frameworks, ormTools []string, migrationContext types.MigrationContext) (types.MigrationCommand, error) {
			return types.MigrationCommand{
				Command:     "npm run migrate",
				Confidence:  "high",
				Explanation: "Mock migration command",
			}, nil
		},
		SummarizeStepsFunc: func(ctx context.Context, steps []string) (types.Summary, error) {
			return types.Summary{
				Summary: "Mock summary of deployment steps",
			}, nil
		},
		DetermineEnvVarRolesFunc: func(ctx context.Context, ev types.EnvVarCandidate, dbList []string) (types.EnvVarCategory, error) {
			return types.EnvVarCategory{
				Role:   "database",
				DbType: "postgresql",
			}, nil
		},
		DetermineBuildOutputFunc: func(ctx context.Context, bo types.BuildOutputCandidate) (types.BuildOutput, error) {
			return types.BuildOutput{
				Path: "dist",
			}, nil
		},
		SummarizeDeployErrorFunc: func(ctx context.Context, error string, intent types.Intent, spec types.ProjectSpec, os string, violations []string) (types.Error, error) {
			return types.Error{
				Summary: "Mock error summary",
				Remediations: []types.Remediation{
					{
						Description: "Mock remediation",
						CliCommand:  "npm install",
					},
				},
			}, nil
		},
		CategorizeRoutesFunc: func(ctx context.Context, routes []types.RouteCandidate) (types.CategorizedRoutes, error) {
			return types.CategorizedRoutes{
				Recommended: types.Route{
					Path: "/",
				},
			}, nil
		},
		FetchPricingFunc: func(ctx context.Context, service types.Service, content string) (types.ServicePricing, error) {
			return types.ServicePricing{
				Service_name: service.Name,
				Service_type: service.Type,
				Plan:         service.Plan,
				Monthly_cost: 10.0,
			}, nil
		},
	}
}

// ExtractIntent implements the Client interface.
func (m *MockClient) ExtractIntent(ctx context.Context, prompt string) (types.Intent, error) {
	return m.ExtractIntentFunc(ctx, prompt)
}

// SummarizeIntent implements the Client interface.
func (m *MockClient) SummarizeIntent(ctx context.Context, intent types.Intent, name, language string, detectedPlatforms []string) (types.Summary, error) {
	return m.SummarizeIntentFunc(ctx, intent, name, language)
}

// DetermineLaunchCommand implements the Client interface.
func (m *MockClient) DetermineLaunchCommand(ctx context.Context, language string, frameworks, envVars []string, lc types.LaunchContext) (types.LaunchCommand, error) {
	return m.DetermineLaunchCommandFunc(ctx, language, frameworks, envVars, lc)
}

// DetermineMigrationCommand implements the Client interface.
func (m *MockClient) DetermineMigrationCommand(ctx context.Context, language string, frameworks, ormTools []string, migrationContext types.MigrationContext) (types.MigrationCommand, error) {
	return m.DetermineMigrationCommandFunc(ctx, language, frameworks, ormTools, migrationContext)
}

// SummarizeSteps implements the Client interface.
func (m *MockClient) SummarizeSteps(ctx context.Context, steps []string) (types.Summary, error) {
	return m.SummarizeStepsFunc(ctx, steps)
}

// DetermineEnvVarRoles implements the Client interface.
func (m *MockClient) DetermineEnvVarRoles(ctx context.Context, ev types.EnvVarCandidate, dbList []string) (types.EnvVarCategory, error) {
	return m.DetermineEnvVarRolesFunc(ctx, ev, dbList)
}

// DetermineBuildOutput implements the Client interface.
func (m *MockClient) DetermineBuildOutput(ctx context.Context, bo types.BuildOutputCandidate) (types.BuildOutput, error) {
	return m.DetermineBuildOutputFunc(ctx, bo)
}

// SummarizeDeployError implements the Client interface.
func (m *MockClient) SummarizeDeployError(ctx context.Context, error string, intent types.Intent, spec types.ProjectSpec, os string, violations []string) (types.Error, error) {
	return m.SummarizeDeployErrorFunc(ctx, error, intent, spec, os, violations)
}

// CategorizeRoutes implements the Client interface.
func (m *MockClient) CategorizeRoutes(ctx context.Context, routes []types.RouteCandidate) (types.CategorizedRoutes, error) {
	return m.CategorizeRoutesFunc(ctx, routes)
}

// FetchPricing implements the Client interface.
func (m *MockClient) FetchPricing(ctx context.Context, service types.Service, content string) (types.ServicePricing, error) {
	return m.FetchPricingFunc(ctx, service, content)
}

// Compile-time interface compliance check
var _ Client = (*MockClient)(nil)
