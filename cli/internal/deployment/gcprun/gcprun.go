// Package gcprun deploys container images to Google Cloud Run using the user's
// own GCP credentials (ADC) — build locally, push to the user's Artifact
// Registry, then create or update a managed Cloud Run service. The GCP analogue
// of the App Runner adapter: no backend, no central account.
package gcprun

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-errors/errors"
	"golang.org/x/oauth2"
	"google.golang.org/api/option"
	run "google.golang.org/api/run/v2"
)

const readyTimeout = 15 * time.Minute

// trafficToRevision is the Cloud Run v2 traffic-allocation type that pins traffic
// to a named revision (vs the latest).
const trafficToRevision = "TRAFFIC_TARGET_ALLOCATION_TYPE_REVISION"

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
	// SecretEnv maps an env var name to a Secret Manager secret resource path
	// (projects/<p>/secrets/<id>); referenced via SecretKeyRef instead of inline.
	SecretEnv map[string]string
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
	env := make([]*run.GoogleCloudRunV2EnvVar, 0, len(cfg.EnvVars)+len(cfg.SecretEnv))
	for k, v := range cfg.EnvVars {
		env = append(env, &run.GoogleCloudRunV2EnvVar{Name: k, Value: v})
	}
	for k, secretName := range cfg.SecretEnv {
		env = append(env, &run.GoogleCloudRunV2EnvVar{
			Name: k,
			ValueSource: &run.GoogleCloudRunV2EnvVarSource{
				SecretKeyRef: &run.GoogleCloudRunV2SecretKeySelector{Secret: secretName, Version: "latest"},
			},
		})
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

// Delete removes the Cloud Run service (best-effort teardown — the delete is an LRO
// we initiate but don't wait on).
func (d *Deployer) Delete(ctx context.Context, serviceName string) error {
	if _, err := d.svc.Projects.Locations.Services.Delete(d.ServicePath(serviceName)).Context(ctx).Do(); err != nil {
		return errors.Errorf("failed to delete Cloud Run service %q: %w", serviceName, err)
	}
	return nil
}

// PreviousRevision returns the short name of the revision to roll back to: the
// newest READY revision older than the one currently serving traffic. Returns "" if
// there's nothing to roll back to. Keying off the serving revision (not a blind
// "second-newest") makes repeated rollbacks walk back correctly and skips a failed
// latest deploy that never took traffic.
func (d *Deployer) PreviousRevision(ctx context.Context, serviceName string) (string, error) {
	name := d.ServicePath(serviceName)
	svc, err := d.svc.Projects.Locations.Services.Get(name).Context(ctx).Do()
	if err != nil {
		return "", errors.Errorf("failed to load Cloud Run service for rollback: %w", err)
	}
	resp, err := d.svc.Projects.Locations.Services.Revisions.List(name).Context(ctx).Do()
	if err != nil {
		return "", errors.Errorf("failed to list Cloud Run revisions: %w", err)
	}
	return previousReadyRevision(resp.Revisions, servingRevision(svc.Traffic, resp.Revisions)), nil
}

// servingRevision resolves the revision currently receiving traffic: an explicit
// 100% pin (set by a prior rollback), else the newest READY revision (the default
// "route to latest").
func servingRevision(traffic []*run.GoogleCloudRunV2TrafficTarget, revs []*run.GoogleCloudRunV2Revision) string {
	for _, t := range traffic {
		if t.Revision != "" && t.Percent >= 100 {
			return t.Revision
		}
	}
	return newestReadyRevision(revs, "")
}

// previousReadyRevision returns the newest READY revision older than `current`.
// Pure so it's unit-testable.
func previousReadyRevision(revs []*run.GoogleCloudRunV2Revision, current string) string {
	var currentCreate string
	for _, r := range revs {
		if shortRevisionName(r.Name) == current {
			currentCreate = r.CreateTime
			break
		}
	}
	return newestReadyRevision(revs, current, currentCreate)
}

// newestReadyRevision returns the short name of the newest READY revision, excluding
// `exclude` and (if olderThan is non-empty) any revision not strictly older than it.
func newestReadyRevision(revs []*run.GoogleCloudRunV2Revision, exclude string, olderThan ...string) string {
	var cutoff string
	if len(olderThan) > 0 {
		cutoff = olderThan[0]
	}
	var best *run.GoogleCloudRunV2Revision
	for _, r := range revs {
		if shortRevisionName(r.Name) == exclude || !revisionReady(r) {
			continue
		}
		if cutoff != "" && r.CreateTime >= cutoff {
			continue
		}
		if best == nil || r.CreateTime > best.CreateTime {
			best = r
		}
	}
	if best == nil {
		return ""
	}
	return shortRevisionName(best.Name)
}

// revisionReady reports whether a revision's Ready condition has succeeded.
func revisionReady(r *run.GoogleCloudRunV2Revision) bool {
	for _, c := range r.Conditions {
		if c.Type == "Ready" {
			return c.State == "CONDITION_SUCCEEDED"
		}
	}
	return false
}

// RouteAllTraffic points 100% of the service's traffic at a specific revision. It
// GETs the service and patches only Traffic, so the container template is preserved.
func (d *Deployer) RouteAllTraffic(ctx context.Context, serviceName, revision string) error {
	name := d.ServicePath(serviceName)
	svc, err := d.svc.Projects.Locations.Services.Get(name).Context(ctx).Do()
	if err != nil {
		return errors.Errorf("failed to load Cloud Run service for rollback: %w", err)
	}
	svc.Traffic = []*run.GoogleCloudRunV2TrafficTarget{{
		Type:     trafficToRevision,
		Revision: revision,
		Percent:  100,
	}}
	if _, err := d.svc.Projects.Locations.Services.Patch(name, svc).Context(ctx).Do(); err != nil {
		return errors.Errorf("failed to route Cloud Run traffic to revision %q: %w", revision, err)
	}
	return nil
}

// shortRevisionName strips the full resource path to the bare revision id that the
// traffic target expects.
func shortRevisionName(full string) string {
	if i := strings.LastIndex(full, "/"); i >= 0 {
		return full[i+1:]
	}
	return full
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
