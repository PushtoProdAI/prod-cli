package llm

import (
	"context"

	"github.com/meroxa/prod/cli/internal/workflowext"
)

// AgentSessionExtractor creates a session extractor that works with the agent's session propagator.
// This is used to bridge the gap between the LLM client and the agent's session management.
func AgentSessionExtractor() SessionExtractor {
	// Define the session propagator inline to avoid import cycles
	type Session interface {
		GetAccessToken() string
	}

	sessionPropagator := workflowext.NewPropagator(
		"session",
		workflowext.JSONSerializer[Session]{},
	)

	return func(ctx context.Context) SessionProvider {
		session, ok := sessionPropagator.FromContext(ctx)
		if !ok {
			return nil
		}
		return session
	}
}
