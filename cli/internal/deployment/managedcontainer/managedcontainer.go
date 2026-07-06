// Package managedcontainer is the shared deploy flow for managed-container clouds —
// App Runner, Cloud Run, and Azure Container Apps. They differ only in their cloud
// API calls; the skeleton (resolve credentials → get a registry → build+push the
// image → create/update the service → shape the result) is identical. A cloud
// implements Provider (its API calls) and the base owns the rest, including
// guaranteeing the primary CreatedResource the generic deploy workflow looks for.
package managedcontainer

import (
	"context"

	"github.com/go-errors/errors"
	"github.com/pushtoprodai/prod-cli/internal/deployment"
	"github.com/pushtoprodai/prod-cli/internal/registry"
)

// DeployResult is what a cloud's deploy step reports back: the created service's id,
// name, and public https URL.
type DeployResult struct {
	ID   string
	Name string
	URL  string
}

// DeployFunc creates or updates the service from a pushed image and returns it once
// serving. It owns all cloud-specific work — access roles, secrets, environments,
// public access, and readiness polling.
type DeployFunc func(ctx context.Context, imageRef string) (DeployResult, error)

// Provider is the cloud-specific half of a managed-container deploy.
type Provider interface {
	// Prepare resolves the user's credentials and returns the image registry to push
	// to plus a deploy function. Credentials are resolved here (once), not per step.
	Prepare(ctx context.Context, spec *deployment.DeploymentSpec) (registry.Registry, DeployFunc, error)
	// ResourceType is the CreatedResource Type for this cloud, e.g. "cloudrun_service".
	ResourceType() string
}

// imageBuilder is the build-and-push seam — satisfied by *deployment.DockerGenerator.
// An interface so Run is unit-testable without a real docker build.
type imageBuilder interface {
	BuildAndPushToRegistry(ctx context.Context, spec *deployment.DeploymentSpec, buildContext string, reg registry.Registry) (*deployment.DockerBuildResult, *deployment.DockerPushResult, error)
}

// Run is the shared managed-container deploy flow. A new container cloud implements
// Provider only — this owns build+push, error wrapping, and the primary-resource
// shaping (so a new cloud can't forget Primary, which the generic deploy workflow
// relies on to find the service and its URL).
func Run(ctx context.Context, p Provider, spec *deployment.DeploymentSpec, builder imageBuilder) ([]deployment.CreatedResource, error) {
	reg, deploy, err := p.Prepare(ctx, spec)
	if err != nil {
		return nil, err
	}

	buildContext, _ := spec.Metadata["buildContext"].(string)
	_, pushResult, err := builder.BuildAndPushToRegistry(ctx, spec, buildContext, reg)
	if err != nil {
		return nil, errors.Errorf("failed to build and push image to your container registry: %w", err)
	}

	res, err := deploy(ctx, pushResult.PushedImageURL)
	if err != nil {
		return nil, err
	}
	return primaryResource(res, p.ResourceType()), nil
}

// primaryResource shapes the standard CreatedResource for a container service, always
// marked Primary with the URL in Metadata.
func primaryResource(res DeployResult, resourceType string) []deployment.CreatedResource {
	return []deployment.CreatedResource{{
		ID:       res.ID,
		Type:     resourceType,
		Name:     res.Name,
		Primary:  true,
		Metadata: map[string]any{"url": res.URL},
	}}
}
