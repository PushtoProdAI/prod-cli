package auth

import (
	"context"
	"io"
)

type AWSAuth struct {
	out io.Writer
}

func NewAWSAuth(out io.Writer) *AWSAuth {
	return &AWSAuth{
		out: out,
	}
}

func (aa *AWSAuth) CheckAuthentication(ctx context.Context) (bool, error) {
	return false, nil
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
