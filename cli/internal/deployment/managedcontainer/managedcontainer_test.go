package managedcontainer

import (
	"context"
	"testing"

	"github.com/go-errors/errors"
	"github.com/pushtoprodai/prod-cli/internal/deployment"
	"github.com/pushtoprodai/prod-cli/internal/registry"
)

type fakeRegistry struct{}

func (fakeRegistry) Name() string { return "fake" }
func (fakeRegistry) Credentials(context.Context, string) (registry.Credentials, error) {
	return registry.Credentials{}, nil
}
func (fakeRegistry) Ref(string, string) (string, error) { return "", nil }

type fakeProvider struct {
	prepareErr   error
	deployFn     DeployFunc
	resourceType string
}

func (f *fakeProvider) Prepare(context.Context, *deployment.DeploymentSpec) (registry.Registry, DeployFunc, error) {
	if f.prepareErr != nil {
		return nil, nil, f.prepareErr
	}
	return fakeRegistry{}, f.deployFn, nil
}
func (f *fakeProvider) ResourceType() string { return f.resourceType }

type fakeBuilder struct {
	pushErr  error
	imageRef string
}

func (f fakeBuilder) BuildAndPushToRegistry(context.Context, *deployment.DeploymentSpec, string, registry.Registry) (*deployment.DockerBuildResult, *deployment.DockerPushResult, error) {
	if f.pushErr != nil {
		return nil, nil, f.pushErr
	}
	return nil, &deployment.DockerPushResult{PushedImageURL: f.imageRef}, nil
}

func TestRunShapesPrimaryResource(t *testing.T) {
	var gotImage string
	p := &fakeProvider{
		resourceType: "cloudrun_service",
		deployFn: func(_ context.Context, imageRef string) (DeployResult, error) {
			gotImage = imageRef
			return DeployResult{ID: "svc-id", Name: "my-app", URL: "https://my-app.run.app"}, nil
		},
	}
	spec := &deployment.DeploymentSpec{Name: "my-app"}

	res, err := Run(context.Background(), p, spec, fakeBuilder{imageRef: "registry/my-app:1"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The pushed image ref must reach the deploy step.
	if gotImage != "registry/my-app:1" {
		t.Errorf("deploy step got image %q, want the pushed ref", gotImage)
	}
	if len(res) != 1 {
		t.Fatalf("want exactly one created resource, got %d", len(res))
	}
	r := res[0]
	// The base GUARANTEES Primary — the generic deploy workflow finds the service by it.
	if !r.Primary {
		t.Error("created resource must be Primary")
	}
	if r.Type != "cloudrun_service" || r.ID != "svc-id" || r.Name != "my-app" {
		t.Errorf("resource = %+v", r)
	}
	if r.Metadata["url"] != "https://my-app.run.app" {
		t.Errorf("url metadata = %v", r.Metadata["url"])
	}
}

func TestRunPropagatesErrors(t *testing.T) {
	spec := &deployment.DeploymentSpec{Name: "x"}
	okDeploy := func(context.Context, string) (DeployResult, error) { return DeployResult{}, nil }

	if _, err := Run(context.Background(), &fakeProvider{prepareErr: errors.Errorf("no creds")}, spec, fakeBuilder{}); err == nil {
		t.Error("a Prepare error must propagate")
	}
	if _, err := Run(context.Background(), &fakeProvider{deployFn: okDeploy}, spec, fakeBuilder{pushErr: errors.Errorf("push failed")}); err == nil {
		t.Error("a build/push error must propagate")
	}
	deployErr := func(context.Context, string) (DeployResult, error) {
		return DeployResult{}, errors.Errorf("deploy failed")
	}
	if _, err := Run(context.Background(), &fakeProvider{deployFn: deployErr}, spec, fakeBuilder{imageRef: "r"}); err == nil {
		t.Error("a deploy error must propagate")
	}
}
