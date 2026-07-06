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
	"github.com/pushtoprodai/prod-cli/internal/deployment/managedcontainer"
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

var (
	_ deployment.Deployable = (*Deployment)(nil)
	_ deployment.Destroyer  = (*Deployment)(nil)
)

// NewAppRunnerDeployment builds an App Runner deployable for a project spec.
func NewAppRunnerDeployment(spec *deployment.DeploymentSpec, dockerGen *deployment.DockerGenerator, writer io.Writer) *Deployment {
	return &Deployment{spec: spec, dockerGen: dockerGen, writer: writer}
}

// Deploy runs the shared managed-container flow with App Runner as the provider.
func (d *Deployment) Deploy(ctx context.Context) ([]deployment.CreatedResource, error) {
	return managedcontainer.Run(ctx, d, d.spec, d.dockerGen)
}

// ResourceType is the primary CreatedResource type for App Runner.
func (d *Deployment) ResourceType() string { return "apprunner_service" }

// Prepare resolves AWS credentials, ensures ECR, and returns a deploy step that
// creates/redeploys the App Runner service (with an ECR access role) and waits until
// RUNNING.
func (d *Deployment) Prepare(ctx context.Context, spec *deployment.DeploymentSpec) (prodreg.Registry, managedcontainer.DeployFunc, error) {
	cfg, accountID, err := auth.NewAWSAuth(d.writer).Config(ctx)
	if err != nil {
		return nil, nil, err
	}
	reg := prodreg.NewECR(cfg, accountID)
	dep := apprunner.New(cfg)

	deploy := func(ctx context.Context, imageRef string) (managedcontainer.DeployResult, error) {
		accessRole, err := dep.EnsureAccessRole(ctx)
		if err != nil {
			return managedcontainer.DeployResult{}, err
		}
		plain, secrets := splitEnvVars(spec.EnvVars)
		name := prodreg.Sanitize(spec.Name)
		serviceArn, err := dep.Deploy(ctx, apprunner.ServiceConfig{
			Name:          name,
			ImageRef:      imageRef,
			AccessRoleArn: accessRole,
			Port:          defaultPort,
			CPU:           defaultCPU,
			Memory:        defaultMemory,
			StartCommand:  spec.StartCommand,
			EnvVars:       plain,
			Secrets:       secrets,
		})
		if err != nil {
			return managedcontainer.DeployResult{}, err
		}

		waitCtx, cancel := context.WithTimeout(ctx, waitTimeout)
		defer cancel()
		serviceURL, err := dep.WaitForRunning(waitCtx, serviceArn)
		if err != nil {
			return managedcontainer.DeployResult{}, err
		}
		return managedcontainer.DeployResult{
			ID: serviceArn, Name: name, URL: "https://" + serviceURL,
			Identifiers: map[string]string{"region": cfg.Region, "account": accountID},
		}, nil
	}
	return reg, deploy, nil
}

// GetPreviousDeployment is not yet implemented for App Runner.
func (d *Deployment) GetPreviousDeployment(_ context.Context) (*deployment.DeploymentInfo, error) {
	return nil, nil
}

// Rollback is not yet supported for App Runner.
func (d *Deployment) Rollback(_ context.Context, _ string) error {
	return errors.Errorf("App Runner rollback isn't supported yet")
}

// Destroy deletes the App Runner service.
func (d *Deployment) Destroy(ctx context.Context) error {
	cfg, _, err := auth.NewAWSAuth(d.writer).Config(ctx)
	if err != nil {
		return err
	}
	return apprunner.New(cfg).Delete(ctx, prodreg.Sanitize(d.spec.Name))
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
