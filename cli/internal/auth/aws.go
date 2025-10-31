package auth

import (
	"context"
	"fmt"
	"io"

	"github.com/go-errors/errors"
	"github.com/meroxa/prod/cli/internal/backend"
)

type AWSAuth struct {
	out            io.Writer
	client         *backend.Client
	sessionFromCtx func(context.Context) *Session
}

func NewAWSAuth(out io.Writer) *AWSAuth {
	return &AWSAuth{
		out:    out,
		client: backend.NewClient(),
	}
}

func (aa *AWSAuth) SetSessionExtractor(fn func(context.Context) *Session) {
	aa.sessionFromCtx = fn
}

func (aa *AWSAuth) CheckAuthentication(ctx context.Context) (bool, error) {
	var session *Session
	if aa.sessionFromCtx != nil {
		session = aa.sessionFromCtx(ctx)
	}

	if session == nil {
		fmt.Fprintf(aa.out, "DEBUG: No session found in context\n")
		return false, errors.Errorf("no session found in context")
	}

	accessToken := session.GetAccessToken()
	if accessToken == "" {
		fmt.Fprintf(aa.out, "DEBUG: No access token in session\n")
		return false, errors.Errorf("no access token found in session")
	}

	fmt.Fprintf(aa.out, "DEBUG: Found access token (length: %d)\n", len(accessToken))
	authenticated, err := aa.client.CheckAWSAuthentication(ctx, accessToken)
	if err != nil {
		fmt.Fprintf(aa.out, "DEBUG: Backend check failed: %v\n", err)
		return false, errors.Errorf("failed to check AWS authentication: %w", err)
	}

	fmt.Fprintf(aa.out, "DEBUG: Backend returned authenticated=%v\n", authenticated)
	return authenticated, nil
}

func (aa *AWSAuth) ValidateAPIKey(ctx context.Context, token string) (bool, error) {
	return false, nil
}

func (aa *AWSAuth) PerformOAuthLogin(ctx context.Context) error {
	return nil
}

func (aa *AWSAuth) APIKeyPrompt() string {
	return ""
}
