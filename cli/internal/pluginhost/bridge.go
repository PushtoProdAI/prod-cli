// Package pluginhost bridges an external provider plugin into prod's deploy
// machinery: it adapts a pkg/plugin.Provider to the L2 managedcontainer.Provider and
// the registry.Registry / deployment.Deployable interfaces, so a plugin deploys
// through the exact same path as a built-in managed-container cloud. (Discovery,
// launch, and catalog registration live alongside; this file is the pure bridge.)
package pluginhost

import (
	"context"
	"fmt"
	"io"
	"strconv"

	"github.com/go-errors/errors"
	"github.com/pushtoprodai/prod-cli/internal/deployment"
	"github.com/pushtoprodai/prod-cli/internal/deployment/managedcontainer"
	prodreg "github.com/pushtoprodai/prod-cli/internal/registry"
	"github.com/pushtoprodai/prod-cli/pkg/plugin"
)

const defaultPort = 8080

// pluginRegistry adapts a plugin's RegistryInfo to registry.Registry so the host's
// docker build+push targets the plugin's registry with static credentials.
type pluginRegistry struct {
	name string
	info plugin.RegistryInfo
}

func (r *pluginRegistry) Name() string { return "plugin:" + r.name }

func (r *pluginRegistry) Ref(_, tag string) (string, error) {
	return fmt.Sprintf("%s/%s:%s", r.info.Host, r.info.Repository, tag), nil
}

func (r *pluginRegistry) Credentials(_ context.Context, _ string) (prodreg.Credentials, error) {
	return prodreg.Credentials{
		URL:        r.info.Host,
		AuthServer: r.info.Host,
		Repository: r.info.Repository,
		Username:   r.info.Username,
		Token:      r.info.Token,
	}, nil
}

// pluginProvider adapts a plugin.Provider to managedcontainer.Provider, so a plugin
// deploy runs the shared host build+push → deploy flow.
type pluginProvider struct {
	prov plugin.Provider
	meta plugin.Meta
}

func (p *pluginProvider) ResourceType() string { return "plugin_service" }

func (p *pluginProvider) Prepare(ctx context.Context, spec *deployment.DeploymentSpec) (prodreg.Registry, managedcontainer.DeployFunc, error) {
	project := prodreg.Sanitize(spec.Name)
	info, err := p.prov.RegistryInfo(ctx, project)
	if err != nil {
		return nil, nil, errors.Errorf("plugin %q registry info: %w", p.meta.Name, err)
	}
	reg := &pluginRegistry{name: p.meta.Name, info: info}

	deploy := func(ctx context.Context, imageRef string) (managedcontainer.DeployResult, error) {
		plain, secret := partitionEnv(spec.EnvVars)
		res, err := p.prov.Deploy(ctx, plugin.DeployRequest{
			ImageRef:  imageRef,
			Name:      project,
			Port:      defaultPort,
			PlainEnv:  plain,
			SecretEnv: secret,
		})
		if err != nil {
			return managedcontainer.DeployResult{}, err
		}
		return managedcontainer.DeployResult{ID: res.ID, Name: res.Name, URL: res.URL}, nil
	}
	return reg, deploy, nil
}

// pluginDeployable adapts a plugin.Provider to deployment.Deployable (the interface
// createDeployable returns), deploying via the managed-container base.
type pluginDeployable struct {
	prov      plugin.Provider
	meta      plugin.Meta
	spec      *deployment.DeploymentSpec
	dockerGen *deployment.DockerGenerator
	writer    io.Writer
}

var _ deployment.Deployable = (*pluginDeployable)(nil)

// NewDeployable builds a Deployable that deploys a project through a provider plugin.
func NewDeployable(prov plugin.Provider, meta plugin.Meta, spec *deployment.DeploymentSpec, dockerGen *deployment.DockerGenerator, writer io.Writer) deployment.Deployable {
	return &pluginDeployable{prov: prov, meta: meta, spec: spec, dockerGen: dockerGen, writer: writer}
}

func (d *pluginDeployable) Deploy(ctx context.Context) ([]deployment.CreatedResource, error) {
	return managedcontainer.Run(ctx, &pluginProvider{prov: d.prov, meta: d.meta}, d.spec, d.dockerGen)
}

func (d *pluginDeployable) GetPreviousDeployment(ctx context.Context) (*deployment.DeploymentInfo, error) {
	if !d.meta.SupportsRollback {
		return nil, nil
	}
	info, err := d.prov.PreviousDeployment(ctx, prodreg.Sanitize(d.spec.Name))
	if err != nil {
		return nil, err
	}
	if info.ID == "" {
		return nil, nil
	}
	return &deployment.DeploymentInfo{ID: info.ID, Status: info.Status}, nil
}

func (d *pluginDeployable) Rollback(ctx context.Context, targetID string) error {
	if targetID == "" {
		return errors.Errorf("no previous deployment to roll back to")
	}
	return d.prov.Rollback(ctx, prodreg.Sanitize(d.spec.Name), targetID)
}

// partitionEnv splits env vars into non-sensitive and sensitive, forcing PORT so the
// app listens where the plugin's ingress routes.
func partitionEnv(vars []deployment.EnvVar) (plain, secret map[string]string) {
	plain = map[string]string{}
	secret = map[string]string{}
	for _, v := range vars {
		if v.Sensitive {
			secret[v.Name] = v.Value
		} else {
			plain[v.Name] = v.Value
		}
	}
	plain["PORT"] = strconv.Itoa(defaultPort)
	return plain, secret
}
