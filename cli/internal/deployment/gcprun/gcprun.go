// Package gcprun deploys container images to Google Cloud Run using the user's
// own GCP credentials (ADC) — build locally, push to the user's Artifact
// Registry, then create or update a managed Cloud Run service. The GCP analogue
// of the App Runner adapter: no backend, no central account.
package gcprun

import (
	"context"
	"fmt"
	"time"

	"github.com/go-errors/errors"
	"golang.org/x/oauth2"
	"google.golang.org/api/option"
	run "google.golang.org/api/run/v2"
)

const readyTimeout = 15 * time.Minute

// Deployer creates/updates Cloud Run services via the run.googleapis.com v2 API.
type Deployer struct {
	svc     *run.Service
	project string
	region  string
}

// New builds a Cloud Run deployer from the user's ADC token source.
func New(ctx context.Context, ts oauth2.TokenSource, project, region string) (*Deployer, error) {
	svc, err := run.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return nil, errors.Errorf("failed to build Cloud Run client: %w", err)
	}
	return &Deployer{svc: svc, project: project, region: region}, nil
}

// ServiceConfig is the subset of a Cloud Run service prod sets.
type ServiceConfig struct {
	Name     string
	ImageRef string
	Port     int64
	CPU      string // e.g. "1000m"
	Memory   string // e.g. "512Mi"
	EnvVars  map[string]string
}

func (d *Deployer) parent() string {
	return fmt.Sprintf("projects/%s/locations/%s", d.project, d.region)
}

// ServicePath is the fully-qualified resource name of the service.
func (d *Deployer) ServicePath(name string) string {
	return fmt.Sprintf("%s/services/%s", d.parent(), name)
}

// buildService maps a ServiceConfig to a Cloud Run v2 service resource.
func buildService(cfg ServiceConfig) *run.GoogleCloudRunV2Service {
	env := make([]*run.GoogleCloudRunV2EnvVar, 0, len(cfg.EnvVars))
	for k, v := range cfg.EnvVars {
		env = append(env, &run.GoogleCloudRunV2EnvVar{Name: k, Value: v})
	}
	return &run.GoogleCloudRunV2Service{
		Template: &run.GoogleCloudRunV2RevisionTemplate{
			Containers: []*run.GoogleCloudRunV2Container{{
				Image: cfg.ImageRef,
				Env:   env,
				Ports: []*run.GoogleCloudRunV2ContainerPort{{ContainerPort: cfg.Port}},
				Resources: &run.GoogleCloudRunV2ResourceRequirements{
					Limits: map[string]string{"cpu": cfg.CPU, "memory": cfg.Memory},
				},
			}},
		},
	}
}

// Deploy creates the Cloud Run service, or updates it (a new revision) if it
// already exists, makes it publicly invocable, and returns the service path.
func (d *Deployer) Deploy(ctx context.Context, cfg ServiceConfig) (string, error) {
	name := d.ServicePath(cfg.Name)
	svcResource := buildService(cfg)

	// Idempotent upsert: Patch with AllowMissing creates the service if absent and
	// updates it (a new revision) if present. This avoids a get-then-branch that
	// couldn't tell a 404 from a transient/permission error on the Get.
	if _, err := d.svc.Projects.Locations.Services.
		Patch(name, svcResource).AllowMissing(true).Context(ctx).Do(); err != nil {
		return "", errors.Errorf("failed to deploy Cloud Run service (is run.googleapis.com enabled?): %w", err)
	}

	if err := d.allowUnauthenticated(ctx, name); err != nil {
		return "", err
	}
	return name, nil
}

// allowUnauthenticated grants allUsers the run.invoker role so the service's URL
// is publicly reachable (Cloud Run requires auth by default). This replaces the
// service's invoker policy; for a prod-created service that's fine, but it would
// overwrite any extra invoker bindings a user added out of band.
func (d *Deployer) allowUnauthenticated(ctx context.Context, name string) error {
	_, err := d.svc.Projects.Locations.Services.SetIamPolicy(name, &run.GoogleIamV1SetIamPolicyRequest{
		Policy: &run.GoogleIamV1Policy{
			Bindings: []*run.GoogleIamV1Binding{{
				Role:    "roles/run.invoker",
				Members: []string{"allUsers"},
			}},
		},
	}).Context(ctx).Do()
	if err != nil {
		return errors.Errorf("failed to make Cloud Run service publicly reachable: %w", err)
	}
	return nil
}

// WaitForReady polls until the service's Ready condition succeeds, then returns
// its (https) URL.
func (d *Deployer) WaitForReady(ctx context.Context, name string) (string, error) {
	deadline := time.Now().Add(readyTimeout)
	for {
		svc, err := d.svc.Projects.Locations.Services.Get(name).Context(ctx).Do()
		if err != nil {
			return "", errors.Errorf("failed to poll Cloud Run service: %w", err)
		}
		for _, c := range svc.Conditions {
			if c.Type == "Ready" {
				switch c.State {
				case "CONDITION_SUCCEEDED":
					return svc.Uri, nil
				case "CONDITION_FAILED":
					return "", errors.Errorf("Cloud Run service failed to become ready: %s", c.Message)
				}
			}
		}
		if time.Now().After(deadline) {
			return "", errors.Errorf("timed out waiting for Cloud Run service %q to be ready", name)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}
