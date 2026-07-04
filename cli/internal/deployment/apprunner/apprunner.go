// Package apprunner deploys a container image to AWS App Runner using the user's
// own AWS credentials — a managed container→HTTPS service with no VPC, no ECS
// cluster, and no CloudFormation. The image lives in the user's ECR (see the
// registry adapter's ecr kind); this package ensures the IAM access role App
// Runner needs to pull it, then creates or redeploys the service.
package apprunner

import (
	"context"
	stderrors "errors"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	arsdk "github.com/aws/aws-sdk-go-v2/service/apprunner"
	artypes "github.com/aws/aws-sdk-go-v2/service/apprunner/types"
	iamsdk "github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	smsdk "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/go-errors/errors"
)

const (
	// ecrAccessRoleName is the IAM role App Runner assumes to pull from ECR.
	ecrAccessRoleName  = "prod-apprunner-ecr-access"
	ecrAccessPolicyArn = "arn:aws:iam::aws:policy/service-role/AWSAppRunnerServicePolicyForECRAccess"
	// assumeRolePolicy lets the App Runner build service assume the role.
	assumeRolePolicy = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"build.apprunner.amazonaws.com"},"Action":"sts:AssumeRole"}]}`
)

// iamAPI / appRunnerAPI are the SDK subsets used here — injectable for tests.
type iamAPI interface {
	CreateRole(context.Context, *iamsdk.CreateRoleInput, ...func(*iamsdk.Options)) (*iamsdk.CreateRoleOutput, error)
	AttachRolePolicy(context.Context, *iamsdk.AttachRolePolicyInput, ...func(*iamsdk.Options)) (*iamsdk.AttachRolePolicyOutput, error)
	PutRolePolicy(context.Context, *iamsdk.PutRolePolicyInput, ...func(*iamsdk.Options)) (*iamsdk.PutRolePolicyOutput, error)
	GetRole(context.Context, *iamsdk.GetRoleInput, ...func(*iamsdk.Options)) (*iamsdk.GetRoleOutput, error)
}

type appRunnerAPI interface {
	ListServices(context.Context, *arsdk.ListServicesInput, ...func(*arsdk.Options)) (*arsdk.ListServicesOutput, error)
	CreateService(context.Context, *arsdk.CreateServiceInput, ...func(*arsdk.Options)) (*arsdk.CreateServiceOutput, error)
	UpdateService(context.Context, *arsdk.UpdateServiceInput, ...func(*arsdk.Options)) (*arsdk.UpdateServiceOutput, error)
	DescribeService(context.Context, *arsdk.DescribeServiceInput, ...func(*arsdk.Options)) (*arsdk.DescribeServiceOutput, error)
}

type secretsAPI interface {
	CreateSecret(context.Context, *smsdk.CreateSecretInput, ...func(*smsdk.Options)) (*smsdk.CreateSecretOutput, error)
	PutSecretValue(context.Context, *smsdk.PutSecretValueInput, ...func(*smsdk.Options)) (*smsdk.PutSecretValueOutput, error)
}

// Deployer creates and updates App Runner services.
type Deployer struct {
	ar           appRunnerAPI
	iam          iamAPI
	sm           secretsAPI
	pollInterval time.Duration // between DescribeService polls in WaitForRunning
}

// New builds a Deployer from the user's AWS config.
func New(cfg aws.Config) *Deployer {
	return &Deployer{
		ar:           arsdk.NewFromConfig(cfg),
		iam:          iamsdk.NewFromConfig(cfg),
		sm:           smsdk.NewFromConfig(cfg),
		pollInterval: 15 * time.Second,
	}
}

// ServiceConfig describes an App Runner web service.
type ServiceConfig struct {
	Name          string            // App Runner service name
	ImageRef      string            // ECR image reference (<host>/<repo>:<tag>)
	AccessRoleArn string            // ECR access role (from EnsureAccessRole)
	Port          string            // container port, e.g. "8080"
	CPU           string            // e.g. "1024" (1 vCPU)
	Memory        string            // e.g. "2048" (2 GB)
	StartCommand  string            // optional
	EnvVars       map[string]string // plain runtime env vars
	Secrets       map[string]string // sensitive env vars → stored in Secrets Manager

	// Resolved by Deploy when Secrets is non-empty:
	secretARNs      map[string]string // env name → secret ARN (RuntimeEnvironmentSecrets)
	instanceRoleArn string            // instance role granting secretsmanager:GetSecretValue
}

// EnsureAccessRole creates (or finds) the IAM role App Runner assumes to pull
// images from the user's ECR, attaches the managed ECR-access policy, and
// returns the role ARN. Idempotent.
//
// Note: IAM is eventually consistent — a freshly created role may not be
// immediately assumable by App Runner, so the caller should retry CreateService
// on a transient "role cannot be assumed" error (handled when wired in stage F).
func (d *Deployer) EnsureAccessRole(ctx context.Context) (string, error) {
	_, err := d.iam.CreateRole(ctx, &iamsdk.CreateRoleInput{
		RoleName:                 aws.String(ecrAccessRoleName),
		AssumeRolePolicyDocument: aws.String(assumeRolePolicy),
		Description:              aws.String("Lets AWS App Runner pull images from your ECR (created by prod)"),
	})
	if err != nil {
		var exists *iamtypes.EntityAlreadyExistsException
		if !stderrors.As(err, &exists) {
			return "", errors.Errorf("failed to create App Runner ECR access role: %w", err)
		}
	}

	// Attaching a managed policy is idempotent, so attach unconditionally.
	if _, err := d.iam.AttachRolePolicy(ctx, &iamsdk.AttachRolePolicyInput{
		RoleName:  aws.String(ecrAccessRoleName),
		PolicyArn: aws.String(ecrAccessPolicyArn),
	}); err != nil {
		return "", errors.Errorf("failed to attach ECR access policy: %w", err)
	}

	out, err := d.iam.GetRole(ctx, &iamsdk.GetRoleInput{RoleName: aws.String(ecrAccessRoleName)})
	if err != nil {
		return "", errors.Errorf("failed to get App Runner ECR access role: %w", err)
	}
	if out.Role == nil {
		return "", errors.Errorf("App Runner ECR access role has no ARN")
	}
	return aws.ToString(out.Role.Arn), nil
}

// Deploy creates the App Runner service, or triggers a fresh deployment of the
// existing one. Returns the service ARN.
func (d *Deployer) Deploy(ctx context.Context, cfg ServiceConfig) (string, error) {
	// Sensitive vars go into Secrets Manager, referenced by ARN, and the service
	// gets an instance role allowing it to read them.
	if len(cfg.Secrets) > 0 {
		arns, err := d.ensureSecrets(ctx, cfg.Name, cfg.Secrets)
		if err != nil {
			return "", err
		}
		roleArn, err := d.ensureInstanceRole(ctx, cfg.Name, arnValues(arns))
		if err != nil {
			return "", err
		}
		cfg.secretARNs = arns
		cfg.instanceRoleArn = roleArn
	}

	existing, err := d.findService(ctx, cfg.Name)
	if err != nil {
		return "", err
	}
	if existing != "" {
		// UpdateService applies the new image ref / env / instance config AND
		// triggers a deployment. StartDeployment alone would re-pull the OLD
		// configured image — and prod pushes a fresh tag on every deploy.
		if _, err := d.ar.UpdateService(ctx, &arsdk.UpdateServiceInput{
			ServiceArn:            aws.String(existing),
			SourceConfiguration:   sourceConfiguration(cfg),
			InstanceConfiguration: instanceConfiguration(cfg),
		}); err != nil {
			return "", errors.Errorf("failed to update App Runner service: %w", err)
		}
		return existing, nil
	}

	out, err := d.ar.CreateService(ctx, CreateInput(cfg))
	if err != nil {
		return "", errors.Errorf("failed to create App Runner service: %w", err)
	}
	if out.Service == nil {
		return "", errors.Errorf("App Runner returned no service")
	}
	return aws.ToString(out.Service.ServiceArn), nil
}

// WaitForRunning polls the service until it reaches RUNNING (returning its
// public URL) or a terminal state, or until ctx is done. The caller sets a
// deadline on ctx to bound the wait.
func (d *Deployer) WaitForRunning(ctx context.Context, serviceArn string) (string, error) {
	interval := d.pollInterval
	if interval <= 0 {
		interval = 15 * time.Second // guard: a zero-value Deployer must not busy-spin
	}
	for {
		out, err := d.ar.DescribeService(ctx, &arsdk.DescribeServiceInput{ServiceArn: aws.String(serviceArn)})
		if err != nil {
			return "", errors.Errorf("failed to describe App Runner service: %w", err)
		}
		if out.Service == nil {
			return "", errors.Errorf("App Runner returned no service")
		}
		switch out.Service.Status {
		case artypes.ServiceStatusRunning:
			url := aws.ToString(out.Service.ServiceUrl)
			if url == "" {
				return "", errors.Errorf("App Runner service is RUNNING but reported no URL")
			}
			return url, nil
		case artypes.ServiceStatusCreateFailed, artypes.ServiceStatusDeleteFailed,
			artypes.ServiceStatusDeleted, artypes.ServiceStatusPaused:
			return "", errors.Errorf("App Runner service is in state %s — deploy failed", out.Service.Status)
		}
		// OPERATION_IN_PROGRESS (or any not-yet-terminal status): wait and re-poll,
		// unless the caller's ctx deadline/cancel gives up first.
		select {
		case <-ctx.Done():
			return "", errors.Errorf("stopped waiting for App Runner service to become RUNNING: %w", ctx.Err())
		case <-time.After(interval):
		}
	}
}

// findService returns the ARN of a service with the given name, or "" if none.
func (d *Deployer) findService(ctx context.Context, name string) (string, error) {
	var next *string
	for {
		out, err := d.ar.ListServices(ctx, &arsdk.ListServicesInput{NextToken: next})
		if err != nil {
			return "", errors.Errorf("failed to list App Runner services: %w", err)
		}
		for _, s := range out.ServiceSummaryList {
			if aws.ToString(s.ServiceName) == name {
				return aws.ToString(s.ServiceArn), nil
			}
		}
		if out.NextToken == nil {
			return "", nil
		}
		next = out.NextToken
	}
}

// CreateInput builds the App Runner CreateService input from a ServiceConfig.
func CreateInput(cfg ServiceConfig) *arsdk.CreateServiceInput {
	return &arsdk.CreateServiceInput{
		ServiceName:           aws.String(cfg.Name),
		SourceConfiguration:   sourceConfiguration(cfg),
		InstanceConfiguration: instanceConfiguration(cfg),
	}
}

// sourceConfiguration builds the image source + ECR auth, shared by create and update.
func sourceConfiguration(cfg ServiceConfig) *artypes.SourceConfiguration {
	imageCfg := &artypes.ImageConfiguration{Port: aws.String(cfg.Port)}
	if cfg.StartCommand != "" {
		imageCfg.StartCommand = aws.String(cfg.StartCommand)
	}
	if len(cfg.EnvVars) > 0 {
		imageCfg.RuntimeEnvironmentVariables = cfg.EnvVars
	}
	if len(cfg.secretARNs) > 0 {
		imageCfg.RuntimeEnvironmentSecrets = cfg.secretARNs
	}

	return &artypes.SourceConfiguration{
		AutoDeploymentsEnabled: aws.Bool(false),
		AuthenticationConfiguration: &artypes.AuthenticationConfiguration{
			AccessRoleArn: aws.String(cfg.AccessRoleArn),
		},
		ImageRepository: &artypes.ImageRepository{
			ImageIdentifier:     aws.String(cfg.ImageRef),
			ImageRepositoryType: artypes.ImageRepositoryTypeEcr,
			ImageConfiguration:  imageCfg,
		},
	}
}

func instanceConfiguration(cfg ServiceConfig) *artypes.InstanceConfiguration {
	ic := &artypes.InstanceConfiguration{Cpu: aws.String(cfg.CPU), Memory: aws.String(cfg.Memory)}
	if cfg.instanceRoleArn != "" {
		ic.InstanceRoleArn = aws.String(cfg.instanceRoleArn)
	}
	return ic
}
