// Package llm provides a high-level, idiomatic Go interface for LLM operations.
//
// This package abstracts away the details of BAML client configuration, proxy routing,
// and session management, providing a clean interface for making LLM calls throughout
// the application.
//
// # Key Features
//
//   - Automatic proxy configuration based on session context
//   - Centralized error handling and logging
//   - Easy mocking for unit tests
//   - Consistent interface across all LLM operations
//
// # Usage
//
// Basic usage with default configuration:
//
//	client := llm.NewDefault()
//	intent, err := client.ExtractIntent(ctx, "deploy my app to render")
//
// Custom configuration:
//
//	client := llm.New(llm.Config{
//		ProxyURL:   "https://my-proxy.example.com/llm",
//		SessionKey: "auth_session",
//	})
//
// # Context and Sessions
//
// The client automatically detects session information from context when available.
// When a session is present, all calls are routed through the configured proxy.
// When no session is available, calls are made directly to the LLM provider.
//
// Sessions must implement the SessionProvider interface:
//
//	type SessionProvider interface {
//		GetAccessToken() string
//	}
//
// # Testing
//
// Use MockClient for unit testing:
//
//	mockClient := llm.NewMockClient()
//	mockClient.ExtractIntentFunc = func(ctx context.Context, prompt string) (types.Intent, error) {
//		return types.Intent{Action: "test"}, nil
//	}
package llm
