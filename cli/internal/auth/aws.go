package auth

import (
	"context"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/go-errors/errors"
)

// AWSAuth resolves the user's AWS credentials from the standard AWS credential
// chain (~/.aws, environment variables, SSO). There is no backend and no
// cross-account role — prod deploys into the user's own AWS account with their
// own credentials, exactly like the AWS CLI.
type AWSAuth struct {
	out    io.Writer
	region string // optional override; otherwise resolved from the environment/config
}

// AWSAuth satisfies the platform AuthProvider interface.
var _ AuthProvider = (*AWSAuth)(nil)

func NewAWSAuth(out io.Writer) *AWSAuth {
	return &AWSAuth{out: out}
}

// SetRegion overrides the region resolved from the environment/config.
func (aa *AWSAuth) SetRegion(region string) { aa.region = region }

// Config loads AWS config from the default credential chain and verifies the
// credentials are usable, returning the config and the caller's account ID.
// Later stages (ECR, App Runner) build their SDK clients from this config.
func (aa *AWSAuth) Config(ctx context.Context) (aws.Config, string, error) {
	var opts []func(*awsconfig.LoadOptions) error
	if aa.region != "" {
		opts = append(opts, awsconfig.WithRegion(aa.region))
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return aws.Config{}, "", errors.Errorf("failed to load AWS config: %w", err)
	}
	if cfg.Region == "" {
		return aws.Config{}, "", errors.Errorf("no AWS region configured — set AWS_REGION or a region in ~/.aws/config")
	}

	ident, err := sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return aws.Config{}, "", errors.Errorf("AWS credentials aren't usable — configure them via ~/.aws, environment, or SSO (sts:GetCallerIdentity failed): %w", err)
	}

	return cfg, aws.ToString(ident.Account), nil
}

// CheckAuthentication reports whether usable AWS credentials are configured.
func (aa *AWSAuth) CheckAuthentication(ctx context.Context) (bool, error) {
	if _, _, err := aa.Config(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (aa *AWSAuth) ValidateAPIKey(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func (aa *AWSAuth) PerformOAuthLogin(_ context.Context) error {
	return errors.Errorf("AWS uses your local AWS credentials (~/.aws, environment, or SSO) — there's nothing to log in to")
}

func (aa *AWSAuth) APIKeyPrompt() string { return "" }
