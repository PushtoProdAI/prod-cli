package gcprun

import (
	"context"
	"io"
	"os"
	"strconv"

	"github.com/go-errors/errors"
	"github.com/pushtoprodai/prod-cli/internal/auth"
	"github.com/pushtoprodai/prod-cli/internal/deployment"
	"github.com/pushtoprodai/prod-cli/internal/deployment/managedcontainer"
	prodreg "github.com/pushtoprodai/prod-cli/internal/registry"
	"golang.org/x/oauth2"
)

const (
	defaultPort   int64 = 8080
	defaultCPU          = "1000m" // 1 vCPU
	defaultMemory       = "512Mi"
	defaultARRepo       = "prod" // Artifact Registry repository (override: PROD_GCP_AR_REPO)
)

// Deployment deploys a project to Google Cloud Run.
type Deployment struct {
	spec      *deployment.DeploymentSpec
	dockerGen *deployment.DockerGenerator
	writer    io.Writer
}

var (
	_ deployment.Deployable = (*Deployment)(nil)
	_ deployment.Destroyer  = (*Deployment)(nil)
)

// NewCloudRunDeployment builds a Cloud Run deployable for a project spec.
func NewCloudRunDeployment(spec *deployment.DeploymentSpec, dockerGen *deployment.DockerGenerator, writer io.Writer) *Deployment {
	return &Deployment{spec: spec, dockerGen: dockerGen, writer: writer}
}

// Deploy runs the shared managed-container flow with Cloud Run as the provider.
func (d *Deployment) Deploy(ctx context.Context) ([]deployment.CreatedResource, error) {
	return managedcontainer.Run(ctx, d, d.spec, d.dockerGen)
}

// ResourceType is the primary CreatedResource type for Cloud Run.
func (d *Deployment) ResourceType() string { return "cloudrun_service" }

// Prepare resolves ADC credentials, ensures the Artifact Registry, and returns a
// deploy step that creates/updates the Cloud Run service and waits until Ready.
func (d *Deployment) Prepare(ctx context.Context, spec *deployment.DeploymentSpec) (prodreg.Registry, managedcontainer.DeployFunc, error) {
	ts, project, region, err := auth.NewGCPAuth(d.writer).Config(ctx)
	if err != nil {
		return nil, nil, err
	}

	arRepo := os.Getenv("PROD_GCP_AR_REPO")
	if arRepo == "" {
		arRepo = defaultARRepo
	}
	reg, err := prodreg.NewGAR(ctx, ts, project, region, arRepo)
	if err != nil {
		return nil, nil, err
	}
	dep, err := New(ctx, ts, project, region)
	if err != nil {
		return nil, nil, err
	}

	deploy := func(ctx context.Context, imageRef string) (managedcontainer.DeployResult, error) {
		name := prodreg.Sanitize(spec.Name)
		plain, sensitive := partitionEnvVars(spec.EnvVars)
		plain["PORT"] = strconv.FormatInt(defaultPort, 10)

		// Store sensitive env in Secret Manager and reference it, rather than setting
		// it inline on the service config.
		secretEnv, err := provisionSecrets(ctx, ts, project, name, sensitive)
		if err != nil {
			return managedcontainer.DeployResult{}, err
		}

		if _, err := dep.Deploy(ctx, ServiceConfig{
			Name:      name,
			ImageRef:  imageRef,
			Port:      defaultPort,
			CPU:       defaultCPU,
			Memory:    defaultMemory,
			EnvVars:   plain,
			SecretEnv: secretEnv,
		}); err != nil {
			return managedcontainer.DeployResult{}, err
		}
		url, err := dep.WaitForReady(ctx, dep.ServicePath(name))
		if err != nil {
			return managedcontainer.DeployResult{}, err
		}
		return managedcontainer.DeployResult{ID: dep.ServicePath(name), Name: name, URL: url}, nil
	}
	return reg, deploy, nil
}

// GetPreviousDeployment returns the revision to roll back to (the one before the
// current deploy), or nil if there's nothing to roll back to.
func (d *Deployment) GetPreviousDeployment(ctx context.Context) (*deployment.DeploymentInfo, error) {
	dep, name, err := d.deployer(ctx)
	if err != nil {
		return nil, err
	}
	rev, err := dep.PreviousRevision(ctx, name)
	if err != nil {
		return nil, err
	}
	if rev == "" {
		return nil, nil
	}
	return &deployment.DeploymentInfo{ID: rev, Status: "previous revision"}, nil
}

// Rollback routes all traffic back to targetRevision (Cloud Run keeps every
// revision, so rollback is instant — no rebuild).
func (d *Deployment) Rollback(ctx context.Context, targetRevision string) error {
	if targetRevision == "" {
		return errors.Errorf("no previous Cloud Run revision to roll back to")
	}
	dep, name, err := d.deployer(ctx)
	if err != nil {
		return err
	}
	return dep.RouteAllTraffic(ctx, name, targetRevision)
}

// Destroy deletes the Cloud Run service.
func (d *Deployment) Destroy(ctx context.Context) error {
	dep, name, err := d.deployer(ctx)
	if err != nil {
		return err
	}
	return dep.Delete(ctx, name)
}

// deployer resolves the user's GCP credentials and builds a Deployer + the service
// name, shared by the rollback methods.
func (d *Deployment) deployer(ctx context.Context) (*Deployer, string, error) {
	ts, project, region, err := auth.NewGCPAuth(d.writer).Config(ctx)
	if err != nil {
		return nil, "", err
	}
	dep, err := New(ctx, ts, project, region)
	if err != nil {
		return nil, "", err
	}
	return dep, prodreg.Sanitize(d.spec.Name), nil
}

// partitionEnvVars splits env vars into non-sensitive (set inline on the container)
// and sensitive (stored in Secret Manager and referenced). The caller adds PORT to the
// plain set so the app listens where Cloud Run routes.
func partitionEnvVars(vars []deployment.EnvVar) (plain, sensitive map[string]string) {
	plain = map[string]string{}
	sensitive = map[string]string{}
	for _, v := range vars {
		if v.Sensitive {
			sensitive[v.Name] = v.Value
		} else {
			plain[v.Name] = v.Value
		}
	}
	return plain, sensitive
}

// provisionSecrets stores each sensitive value in Secret Manager, grants the Cloud Run
// runtime service account access, and returns an env-name → secret-path map for
// SecretKeyRef. Returns an empty map when there are no secrets (no API calls made).
func provisionSecrets(ctx context.Context, ts oauth2.TokenSource, project, appName string, sensitive map[string]string) (map[string]string, error) {
	secretEnv := map[string]string{}
	if len(sensitive) == 0 {
		return secretEnv, nil
	}
	sm, err := newSecretManager(ctx, ts, project)
	if err != nil {
		return nil, err
	}
	// Resolve the runtime SA once — every secret is granted to the same account.
	sa, err := sm.runtimeServiceAccount(ctx)
	if err != nil {
		return nil, err
	}
	for name, value := range sensitive {
		secretName, err := sm.EnsureSecret(ctx, secretID(appName, name), value)
		if err != nil {
			return nil, err
		}
		// Grant BEFORE the service references the secret, so the container can read it
		// at startup. IAM is eventually consistent; Cloud Run's readiness polling
		// absorbs brief propagation lag.
		if err := sm.GrantAccessor(ctx, secretName, sa); err != nil {
			return nil, err
		}
		secretEnv[name] = secretName
	}
	return secretEnv, nil
}
