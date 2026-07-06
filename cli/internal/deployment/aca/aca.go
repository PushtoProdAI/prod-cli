// Package aca deploys container images to Azure Container Apps using the user's own
// Azure credentials — ensure a resource group + a managed environment, then create or
// update a container app that pulls from the user's Azure Container Registry. The
// Azure analogue of the App Runner / Cloud Run adapters: no backend, no central account.
package aca

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/go-errors/errors"
)

const registrySecretName = "prod-registry-password"

// Deployer creates/updates Container Apps via the Azure Resource Manager.
type Deployer struct {
	apps          *armappcontainers.ContainerAppsClient
	envs          *armappcontainers.ManagedEnvironmentsClient
	groups        *armresources.ResourceGroupsClient
	resourceGroup string
	location      string
}

// New builds a Container Apps deployer from the user's Azure credentials.
func New(cred azcore.TokenCredential, subscription, resourceGroup, location string) (*Deployer, error) {
	apps, err := armappcontainers.NewContainerAppsClient(subscription, cred, nil)
	if err != nil {
		return nil, errors.Errorf("failed to build Container Apps client: %w", err)
	}
	envs, err := armappcontainers.NewManagedEnvironmentsClient(subscription, cred, nil)
	if err != nil {
		return nil, errors.Errorf("failed to build Container Apps environment client: %w", err)
	}
	groups, err := armresources.NewResourceGroupsClient(subscription, cred, nil)
	if err != nil {
		return nil, errors.Errorf("failed to build resource group client: %w", err)
	}
	return &Deployer{apps: apps, envs: envs, groups: groups, resourceGroup: resourceGroup, location: location}, nil
}

// EnsureResourceGroup creates the resource group if it doesn't exist (idempotent).
func (d *Deployer) EnsureResourceGroup(ctx context.Context) error {
	if _, err := d.groups.CreateOrUpdate(ctx, d.resourceGroup, armresources.ResourceGroup{
		Location: to.Ptr(d.location),
	}, nil); err != nil {
		return errors.Errorf("failed to ensure resource group %q: %w", d.resourceGroup, err)
	}
	return nil
}

// EnsureEnvironment returns the managed environment's resource ID, creating a
// Consumption environment if it doesn't exist yet.
func (d *Deployer) EnsureEnvironment(ctx context.Context, envName string) (string, error) {
	got, err := d.envs.Get(ctx, d.resourceGroup, envName, nil)
	if err == nil {
		if got.ID != nil {
			return *got.ID, nil
		}
		return "", errors.Errorf("Container Apps environment %q has no ID", envName)
	}
	// Only create on a genuine 404 — a permission/transient error shouldn't trigger
	// a create that then fails misleadingly.
	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 404 {
		return "", errors.Errorf("failed to look up Container Apps environment %q: %w", envName, err)
	}

	poller, err := d.envs.BeginCreateOrUpdate(ctx, d.resourceGroup, envName, armappcontainers.ManagedEnvironment{
		Location:   to.Ptr(d.location),
		Properties: &armappcontainers.ManagedEnvironmentProperties{},
	}, nil)
	if err != nil {
		return "", errors.Errorf("failed to create Container Apps environment %q: %w", envName, err)
	}
	res, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return "", errors.Errorf("failed waiting for Container Apps environment %q: %w", envName, err)
	}
	if res.ID == nil {
		return "", errors.Errorf("Container Apps environment %q returned no ID", envName)
	}
	return *res.ID, nil
}

// AppConfig is the subset of a container app prod sets.
type AppConfig struct {
	Name          string
	EnvironmentID string
	Image         string
	Port          int32
	CPU           float64 // cores, e.g. 0.5
	Memory        string  // e.g. "1Gi"
	EnvVars       map[string]string

	// Pull credentials for the app to fetch the image from the user's ACR.
	RegistryServer   string
	RegistryUsername string
	RegistryPassword string
}

// Deploy creates or updates the container app (external ingress on Port, pulling
// from the user's registry) and returns its public https URL once provisioned.
func (d *Deployer) Deploy(ctx context.Context, cfg AppConfig) (string, error) {
	env := make([]*armappcontainers.EnvironmentVar, 0, len(cfg.EnvVars))
	for k, v := range cfg.EnvVars {
		env = append(env, &armappcontainers.EnvironmentVar{Name: to.Ptr(k), Value: to.Ptr(v)})
	}

	app := armappcontainers.ContainerApp{
		Location: to.Ptr(d.location),
		Properties: &armappcontainers.ContainerAppProperties{
			ManagedEnvironmentID: to.Ptr(cfg.EnvironmentID),
			Configuration: &armappcontainers.Configuration{
				Ingress: &armappcontainers.Ingress{
					External:   to.Ptr(true),
					TargetPort: to.Ptr(cfg.Port),
				},
				Registries: []*armappcontainers.RegistryCredentials{{
					Server:            to.Ptr(cfg.RegistryServer),
					Username:          to.Ptr(cfg.RegistryUsername),
					PasswordSecretRef: to.Ptr(registrySecretName),
				}},
				Secrets: []*armappcontainers.Secret{{
					Name:  to.Ptr(registrySecretName),
					Value: to.Ptr(cfg.RegistryPassword),
				}},
			},
			Template: &armappcontainers.Template{
				Containers: []*armappcontainers.Container{{
					Name:      to.Ptr(cfg.Name),
					Image:     to.Ptr(cfg.Image),
					Env:       env,
					Resources: &armappcontainers.ContainerResources{CPU: to.Ptr(cfg.CPU), Memory: to.Ptr(cfg.Memory)},
				}},
			},
		},
	}

	poller, err := d.apps.BeginCreateOrUpdate(ctx, d.resourceGroup, cfg.Name, app, nil)
	if err != nil {
		return "", errors.Errorf("failed to deploy Container App %q: %w", cfg.Name, err)
	}
	res, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return "", errors.Errorf("failed waiting for Container App %q: %w", cfg.Name, err)
	}

	fqdn := ingressFqdn(res.ContainerApp)
	if fqdn == "" {
		return "", errors.Errorf("Container App %q has no ingress URL", cfg.Name)
	}
	return fmt.Sprintf("https://%s", fqdn), nil
}

// ingressFqdn extracts the app's public hostname from a ContainerApp result.
func ingressFqdn(app armappcontainers.ContainerApp) string {
	if app.Properties == nil || app.Properties.Configuration == nil || app.Properties.Configuration.Ingress == nil || app.Properties.Configuration.Ingress.Fqdn == nil {
		return ""
	}
	return *app.Properties.Configuration.Ingress.Fqdn
}
