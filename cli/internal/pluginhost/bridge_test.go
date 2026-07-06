package pluginhost

import (
	"context"
	"testing"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
	"github.com/pushtoprodai/prod-cli/internal/deployment/managedcontainer"
	prodreg "github.com/pushtoprodai/prod-cli/internal/registry"
	"github.com/pushtoprodai/prod-cli/pkg/plugin"
)

type fakeProvider struct {
	gotReq         plugin.DeployRequest
	result         plugin.DeployResult
	prev           plugin.DeployInfo
	rollbackTarget string
}

func (f *fakeProvider) Metadata(context.Context) (plugin.Meta, error) {
	return plugin.Meta{Name: "Acme"}, nil
}

func (f *fakeProvider) RegistryInfo(_ context.Context, project string) (plugin.RegistryInfo, error) {
	return plugin.RegistryInfo{Host: "registry.acme.app", Repository: "team/" + project, Username: "u", Token: "t"}, nil
}

func (f *fakeProvider) CheckAuth(context.Context) (plugin.AuthStatus, error) {
	return plugin.AuthStatus{OK: true}, nil
}

func (f *fakeProvider) Deploy(_ context.Context, req plugin.DeployRequest) (plugin.DeployResult, error) {
	f.gotReq = req
	return f.result, nil
}

func (f *fakeProvider) PreviousDeployment(context.Context, string) (plugin.DeployInfo, error) {
	return f.prev, nil
}

func (f *fakeProvider) Rollback(_ context.Context, _, targetID string) error {
	f.rollbackTarget = targetID
	return nil
}

type fakeBuilder struct{ imageRef string }

func (b fakeBuilder) BuildAndPushToRegistry(context.Context, *deployment.DeploymentSpec, string, prodreg.Registry) (*deployment.DockerBuildResult, *deployment.DockerPushResult, error) {
	return nil, &deployment.DockerPushResult{PushedImageURL: b.imageRef}, nil
}

func TestPluginDeployFlow(t *testing.T) {
	fp := &fakeProvider{result: plugin.DeployResult{ID: "svc1", Name: "my-app", URL: "https://my-app.acme.app"}}
	pp := &pluginProvider{prov: fp, meta: plugin.Meta{Name: "Acme"}}
	spec := &deployment.DeploymentSpec{
		Name: "My-App",
		EnvVars: []deployment.EnvVar{
			{Name: "FOO", Value: "bar"},
			{Name: "SECRET", Value: "s", Sensitive: true},
		},
	}

	res, err := managedcontainer.Run(context.Background(), pp, spec, fakeBuilder{imageRef: "registry.acme.app/team/my-app:1"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res) != 1 || !res[0].Primary || res[0].Type != "plugin_service" {
		t.Fatalf("resource = %+v", res)
	}
	if res[0].Metadata["url"] != "https://my-app.acme.app" {
		t.Errorf("url = %v", res[0].Metadata["url"])
	}

	// The plugin's Deploy got the pushed image, the sanitized name, and split env.
	if fp.gotReq.ImageRef != "registry.acme.app/team/my-app:1" {
		t.Errorf("plugin got image %q", fp.gotReq.ImageRef)
	}
	if fp.gotReq.Name != "my-app" {
		t.Errorf("plugin got name %q, want sanitized my-app", fp.gotReq.Name)
	}
	if fp.gotReq.PlainEnv["FOO"] != "bar" || fp.gotReq.PlainEnv["PORT"] != "8080" {
		t.Errorf("plain env = %v", fp.gotReq.PlainEnv)
	}
	if _, leaked := fp.gotReq.PlainEnv["SECRET"]; leaked {
		t.Error("a sensitive var must not be in PlainEnv")
	}
	if fp.gotReq.SecretEnv["SECRET"] != "s" {
		t.Errorf("secret env = %v", fp.gotReq.SecretEnv)
	}
}

func TestPluginRegistry(t *testing.T) {
	r := &pluginRegistry{name: "Acme", info: plugin.RegistryInfo{Host: "registry.acme.app", Repository: "team/app", Username: "u", Token: "t"}}
	creds, _ := r.Credentials(context.Background(), "app")
	if creds.URL != "registry.acme.app" || creds.AuthServer != "registry.acme.app" || creds.Repository != "team/app" || creds.Username != "u" || creds.Token != "t" {
		t.Errorf("credentials = %+v", creds)
	}
	ref, _ := r.Ref("app", "1720")
	if ref != "registry.acme.app/team/app:1720" {
		t.Errorf("ref = %q", ref)
	}
}

func launchOf(fp *fakeProvider) LaunchFunc {
	return func() (plugin.Provider, func(), error) { return fp, func() {}, nil }
}

func TestPluginRollbackGate(t *testing.T) {
	// SupportsRollback=false → no rollback target surfaced.
	noRB := &pluginDeployable{launch: launchOf(&fakeProvider{prev: plugin.DeployInfo{ID: "old"}}), meta: plugin.Meta{SupportsRollback: false}, spec: &deployment.DeploymentSpec{Name: "x"}}
	if info, _ := noRB.GetPreviousDeployment(context.Background()); info != nil {
		t.Errorf("SupportsRollback=false should yield no previous, got %+v", info)
	}
	// SupportsRollback=true → surfaces the prior deployment.
	rb := &pluginDeployable{launch: launchOf(&fakeProvider{prev: plugin.DeployInfo{ID: "old", Status: "s"}}), meta: plugin.Meta{SupportsRollback: true}, spec: &deployment.DeploymentSpec{Name: "x"}}
	info, err := rb.GetPreviousDeployment(context.Background())
	if err != nil || info == nil || info.ID != "old" {
		t.Errorf("GetPreviousDeployment = %+v, %v", info, err)
	}
	if err := rb.Rollback(context.Background(), ""); err == nil {
		t.Error("an empty rollback target must error")
	}
}
