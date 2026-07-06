package gcprun

import (
	"context"
	"io"
	"os"
	"strconv"

	"github.com/go-errors/errors"
	"github.com/pushtoprodai/prod-cli/internal/auth"
	"github.com/pushtoprodai/prod-cli/internal/deployment"
	prodreg "github.com/pushtoprodai/prod-cli/internal/registry"
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

var _ deployment.Deployable = (*Deployment)(nil)

// NewCloudRunDeployment builds a Cloud Run deployable for a project spec.
func NewCloudRunDeployment(spec *deployment.DeploymentSpec, dockerGen *deployment.DockerGenerator, writer io.Writer) *Deployment {
	return &Deployment{spec: spec, dockerGen: dockerGen, writer: writer}
}

// Deploy resolves the user's GCP credentials (ADC), pushes the image to their
// Artifact Registry, and creates or updates the Cloud Run service, returning it
// once Ready.
func (d *Deployment) Deploy(ctx context.Context) ([]deployment.CreatedResource, error) {
	ts, project, region, err := auth.NewGCPAuth(d.writer).Config(ctx)
	if err != nil {
		return nil, err
	}

	// Build locally and push to the user's Artifact Registry.
	arRepo := os.Getenv("PROD_GCP_AR_REPO")
	if arRepo == "" {
		arRepo = defaultARRepo
	}
	reg, err := prodreg.NewGAR(ctx, ts, project, region, arRepo)
	if err != nil {
		return nil, err
	}
	buildContext, _ := d.spec.Metadata["buildContext"].(string)
	_, pushResult, err := d.dockerGen.BuildAndPushToRegistry(ctx, d.spec, buildContext, reg)
	if err != nil {
		return nil, errors.Errorf("failed to build and push image to Artifact Registry: %w", err)
	}

	dep, err := New(ctx, ts, project, region)
	if err != nil {
		return nil, err
	}

	name := prodreg.Sanitize(d.spec.Name)
	if _, err := dep.Deploy(ctx, ServiceConfig{
		Name:     name,
		ImageRef: pushResult.PushedImageURL,
		Port:     defaultPort,
		CPU:      defaultCPU,
		Memory:   defaultMemory,
		EnvVars:  envMap(d.spec.EnvVars),
	}); err != nil {
		return nil, err
	}

	url, err := dep.WaitForReady(ctx, dep.ServicePath(name))
	if err != nil {
		return nil, err
	}

	return []deployment.CreatedResource{{
		ID:       dep.ServicePath(name),
		Type:     "cloudrun_service",
		Name:     name,
		Primary:  true,
		Metadata: map[string]any{"url": url},
	}}, nil
}

// GetPreviousDeployment is not yet implemented for Cloud Run.
func (d *Deployment) GetPreviousDeployment(_ context.Context) (*deployment.DeploymentInfo, error) {
	return nil, nil
}

// Rollback is not yet implemented for Cloud Run. (Cloud Run keeps revisions, so
// real "route traffic to revision N" rollback is a planned fast-follow.)
func (d *Deployment) Rollback(_ context.Context, _ string) error {
	return errors.Errorf("Cloud Run rollback isn't supported yet")
}

// envMap flattens env vars and forces PORT to the container port so the app
// listens where Cloud Run routes. (Sensitive values are set as plain env for now;
// Secret Manager integration is a planned fast-follow.)
func envMap(vars []deployment.EnvVar) map[string]string {
	m := map[string]string{}
	for _, v := range vars {
		m[v.Name] = v.Value
	}
	m["PORT"] = strconv.FormatInt(defaultPort, 10)
	return m
}
