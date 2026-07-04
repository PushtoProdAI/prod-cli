// Package aws deploys container images to AWS App Runner using the user's own
// AWS credentials — build locally, push to the user's ECR, then create or
// redeploy a managed App Runner service. There is no backend, no CloudFormation,
// and no central account.
package aws

import (
	"context"
	"io"
	"time"

	"github.com/go-errors/errors"
	"github.com/pushtoprodai/prod-cli/internal/auth"
	"github.com/pushtoprodai/prod-cli/internal/deployment"
	"github.com/pushtoprodai/prod-cli/internal/deployment/apprunner"
	prodreg "github.com/pushtoprodai/prod-cli/internal/registry"
)

const (
	defaultPort   = "8080"
	defaultCPU    = "1024" // 1 vCPU
	defaultMemory = "2048" // 2 GB
	waitTimeout   = 15 * time.Minute
)

// Deployment deploys a project to AWS App Runner.
type Deployment struct {
	spec      *deployment.DeploymentSpec
	dockerGen *deployment.DockerGenerator
	writer    io.Writer
}

var _ deployment.Deployable = (*Deployment)(nil)

// NewAppRunnerDeployment builds an App Runner deployable for a project spec.
func NewAppRunnerDeployment(spec *deployment.DeploymentSpec, dockerGen *deployment.DockerGenerator, writer io.Writer) *Deployment {
	return &Deployment{spec: spec, dockerGen: dockerGen, writer: writer}
}

// Deploy resolves the user's AWS credentials, pushes the image to their ECR, and
// creates or redeploys the App Runner service, returning it once RUNNING.
func (d *Deployment) Deploy(ctx context.Context) ([]deployment.CreatedResource, error) {
	cfg, accountID, err := auth.NewAWSAuth(d.writer).Config(ctx)
	if err != nil {
		return nil, err
	}

	// Build locally and push to the user's ECR.
	reg := prodreg.NewECR(cfg, accountID)
	buildContext, _ := d.spec.Metadata["buildContext"].(string)
	_, pushResult, err := d.dockerGen.BuildAndPushToRegistry(ctx, d.spec, buildContext, reg)
	if err != nil {
		return nil, errors.Errorf("failed to build and push image to ECR: %w", err)
	}

	// Deploy to App Runner (create or redeploy) with an ECR access role.
	dep := apprunner.New(cfg)
	accessRole, err := dep.EnsureAccessRole(ctx)
	if err != nil {
		return nil, err
	}

	plain, secrets := splitEnvVars(d.spec.EnvVars)
	name := prodreg.Sanitize(d.spec.Name)
	serviceArn, err := dep.Deploy(ctx, apprunner.ServiceConfig{
		Name:          name,
		ImageRef:      pushResult.PushedImageURL,
		AccessRoleArn: accessRole,
		Port:          defaultPort,
		CPU:           defaultCPU,
		Memory:        defaultMemory,
		StartCommand:  d.spec.StartCommand,
		EnvVars:       plain,
		Secrets:       secrets,
	})
	if err != nil {
		return nil, err
	}

	// Wait for the service to become RUNNING (bounded).
	waitCtx, cancel := context.WithTimeout(ctx, waitTimeout)
	defer cancel()
	serviceURL, err := dep.WaitForRunning(waitCtx, serviceArn)
	if err != nil {
		return nil, err
	}

	return []deployment.CreatedResource{{
		ID:       serviceArn,
		Type:     "apprunner_service",
		Name:     name,
		Metadata: map[string]any{"url": "https://" + serviceURL},
	}}, nil
}

// GetPreviousDeployment is not yet implemented for App Runner.
func (d *Deployment) GetPreviousDeployment(_ context.Context) (*deployment.DeploymentInfo, error) {
	return nil, nil
}

// Rollback is not yet supported for App Runner.
func (d *Deployment) Rollback(_ context.Context, _ string) error {
	return errors.Errorf("App Runner rollback isn't supported yet")
}

// splitEnvVars partitions env vars into plain (RuntimeEnvironmentVariables) and
// sensitive (stored in Secrets Manager). PORT is forced to the App Runner port so
// the app listens where App Runner routes.
func splitEnvVars(vars []deployment.EnvVar) (plain, secrets map[string]string) {
	plain = map[string]string{}
	secrets = map[string]string{}
	for _, v := range vars {
		if v.Sensitive {
			secrets[v.Name] = v.Value
		} else {
			plain[v.Name] = v.Value
		}
	}
	plain["PORT"] = defaultPort
	delete(secrets, "PORT")
	if len(secrets) == 0 {
		secrets = nil
	}
	return plain, secrets
}
