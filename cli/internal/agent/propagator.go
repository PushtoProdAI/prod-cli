package agent

import (
	"context"

	"github.com/cschleiden/go-workflows/workflow"
	"github.com/pushtoprodai/prod-cli/internal/auth"
	"github.com/pushtoprodai/prod-cli/internal/workflowext"
)

const key = "session"

var SessionPropagator = workflowext.NewPropagator(
	key,
	workflowext.JSONSerializer[*auth.Session]{},
)

func CtxSession(ctx context.Context) *auth.Session {
	session, ok := SessionPropagator.FromContext(ctx)
	if !ok {
		return nil
	}
	return session
}

func CtxWorkflowSession(ctx workflow.Context) *auth.Session {
	session, ok := SessionPropagator.FromWorkflowContext(ctx)
	if !ok {
		return nil
	}
	return session
}

func WithCtxSession(ctx context.Context, session *auth.Session) context.Context {
	if session == nil {
		return ctx
	}
	return SessionPropagator.WithContext(ctx, session)
}
