package auth

import (
	"context"
	"fmt"
	"io"

	"github.com/go-errors/errors"
	"github.com/meroxa/prod/cli/internal/backend"
	"github.com/meroxa/prod/cli/internal/config"
)

type AWSAuth struct {
	out            io.Writer
	client         *backend.Client
	sessionFromCtx func(context.Context) *Session
	region         string
}

func NewAWSAuth(out io.Writer) *AWSAuth {
	return &AWSAuth{
		out:    out,
		client: backend.NewClient(),
		region: "us-east-1", // Default region
	}
}

func (aa *AWSAuth) SetSessionExtractor(fn func(context.Context) *Session) {
	aa.sessionFromCtx = fn
}

func (aa *AWSAuth) SetRegion(region string) {
	aa.region = region
}

func (aa *AWSAuth) CheckAuthentication(ctx context.Context) (bool, error) {
	var session *Session
	if aa.sessionFromCtx != nil {
		session = aa.sessionFromCtx(ctx)
	}

	if session == nil {
		return false, errors.Errorf("no session found in context")
	}

	accessToken := session.GetAccessToken()
	if accessToken == "" {
		return false, errors.Errorf("no access token found in session")
	}

	authenticated, err := aa.client.CheckAWSAuthentication(ctx, accessToken)
	if err != nil {
		return false, errors.Errorf("failed to check AWS authentication: %w", err)
	}

	return authenticated, nil
}

func (aa *AWSAuth) ValidateAPIKey(ctx context.Context, token string) (bool, error) {
	return false, nil
}

func (aa *AWSAuth) InitializeSetup(ctx context.Context) error {
	// Get session from context
	var session *Session
	if aa.sessionFromCtx != nil {
		session = aa.sessionFromCtx(ctx)
	}
	if session == nil {
		return errors.Errorf("no session found in context")
	}

	accessToken := session.GetAccessToken()
	if accessToken == "" {
		return errors.Errorf("no access token found in session")
	}

	// Step 1: Initialize AWS auth setup - get external ID from backend
	fmt.Fprint(aa.out, "📋 Initializing AWS authentication setup...\n")
	setup, err := aa.client.InitializeAWSAuth(ctx, accessToken, aa.region)
	if err != nil {
		return errors.Errorf("failed to initialize AWS auth: %w", err)
	}

	fmt.Fprint(aa.out, "✓ Generated secure external ID\n\n")

	// Step 2: Generate CloudFormation console URL with pre-filled parameters
	templateURL := config.GetAWSCloudFormationTemplateURL()
	prodAccountID := config.GetProdAWSAccountID()

	// Validate that required configuration is set
	if prodAccountID == "" {
		return errors.Errorf("PROD_AWS_ACCOUNT_ID is not configured - this is a build-time configuration error")
	}
	if templateURL == "" {
		return errors.Errorf("AWS_CLOUDFORMATION_TEMPLATE_URL is not configured - this is a build-time configuration error")
	}

	// URL encode the template URL and parameters
	cfnURL := fmt.Sprintf(
		"https://console.aws.amazon.com/cloudformation/home?region=%s#/stacks/create/review"+
			"?templateURL=%s"+
			"&stackName=ProdDeployRole"+
			"&param_ExternalId=%s"+
			"&param_ProdAWSAccountId=%s",
		setup.Region,
		templateURL,
		setup.ExternalID,
		prodAccountID,
	)

	// Step 3: Show instructions and open browser
	fmt.Fprint(aa.out, "📝 CloudFormation Setup:\n\n")
	fmt.Fprint(aa.out, "We need to create an IAM role in your AWS account that allows Prod to deploy resources.\n")
	fmt.Fprintf(aa.out, "Opening AWS Console in your browser to region: %s\n\n", setup.Region)

	// Try to open the browser
	if err := openBrowser(cfnURL); err != nil {
		fmt.Fprintf(aa.out, "⚠️  Could not open browser automatically: %v\n", err)
		fmt.Fprint(aa.out, "Please manually open this URL:\n")
		fmt.Fprintf(aa.out, "%s\n\n", cfnURL)
	} else {
		fmt.Fprint(aa.out, "✓ Opened AWS Console in browser\n\n")
	}

	fmt.Fprint(aa.out, "📋 Next steps:\n")
	fmt.Fprint(aa.out, "  1. Review the CloudFormation template\n")
	fmt.Fprint(aa.out, "  2. Check the 'I acknowledge that AWS CloudFormation might create IAM resources' box\n")
	fmt.Fprint(aa.out, "  3. Click 'Create stack'\n")
	fmt.Fprint(aa.out, "  4. Wait for stack creation to complete (usually 1-2 minutes)\n")
	fmt.Fprint(aa.out, "  5. Copy the 'RoleArn' from the Outputs tab\n\n")

	return nil
}

func (aa *AWSAuth) CompleteSetup(ctx context.Context, roleArn string) error {
	// Get session from context
	var session *Session
	if aa.sessionFromCtx != nil {
		session = aa.sessionFromCtx(ctx)
	}
	if session == nil {
		return errors.Errorf("no session found in context")
	}

	accessToken := session.GetAccessToken()
	if accessToken == "" {
		return errors.Errorf("no access token found in session")
	}

	// Call backend to store the Role ARN
	if err := aa.client.CompleteAWSAuth(ctx, accessToken, roleArn, aa.region); err != nil {
		return errors.Errorf("failed to complete AWS auth: %w", err)
	}

	return nil
}

func (aa *AWSAuth) PerformOAuthLogin(ctx context.Context) error {
	// This is kept for interface compatibility but not used for AWS
	// AWS uses InitializeSetup + CompleteSetup instead
	return errors.Errorf("AWS authentication requires InitializeSetup and CompleteSetup methods")
}

func (aa *AWSAuth) APIKeyPrompt() string {
	return ""
}
