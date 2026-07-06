// Package aca deploys container images to Azure Container Apps using the user's own
// Azure credentials — ensure a resource group + a managed environment, then create or
// update a container app that pulls from the user's Azure Container Registry. The
// Azure analogue of the App Runner / Cloud Run adapters: no backend, no central account.
package aca

import (
	"context"
	"fmt"
	"strings"
	"time"

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
	revs          *armappcontainers.ContainerAppsRevisionsClient
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
	revs, err := armappcontainers.NewContainerAppsRevisionsClient(subscription, cred, nil)
	if err != nil {
		return nil, errors.Errorf("failed to build Container Apps revisions client: %w", err)
	}
	groups, err := armresources.NewResourceGroupsClient(subscription, cred, nil)
	if err != nil {
		return nil, errors.Errorf("failed to build resource group client: %w", err)
	}
	return &Deployer{apps: apps, envs: envs, revs: revs, groups: groups, resourceGroup: resourceGroup, location: location}, nil
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
	CPU           float64           // cores, e.g. 0.5
	Memory        string            // e.g. "1Gi"
	PlainEnv      map[string]string // non-sensitive env vars, set inline
	SecretEnv     map[string]string // sensitive env vars, stored as Container Apps secrets

	// Pull credentials for the app to fetch the image from the user's ACR.
	RegistryServer   string
	RegistryUsername string
	RegistryPassword string
}

// Deploy creates or updates the container app (external ingress on Port, pulling
// from the user's registry) and returns its public https URL once provisioned.
func (d *Deployer) Deploy(ctx context.Context, cfg AppConfig) (string, error) {
	// Non-sensitive vars are set inline; sensitive vars are stored as Container Apps
	// secrets and referenced by name, so they never appear in the app's plain config.
	env := make([]*armappcontainers.EnvironmentVar, 0, len(cfg.PlainEnv)+len(cfg.SecretEnv))
	for k, v := range cfg.PlainEnv {
		env = append(env, &armappcontainers.EnvironmentVar{Name: to.Ptr(k), Value: to.Ptr(v)})
	}
	secrets := []*armappcontainers.Secret{{
		Name:  to.Ptr(registrySecretName),
		Value: to.Ptr(cfg.RegistryPassword),
	}}
	for k, v := range cfg.SecretEnv {
		sn := secretName(k)
		secrets = append(secrets, &armappcontainers.Secret{Name: to.Ptr(sn), Value: to.Ptr(v)})
		env = append(env, &armappcontainers.EnvironmentVar{Name: to.Ptr(k), SecretRef: to.Ptr(sn)})
	}

	app := armappcontainers.ContainerApp{
		Location: to.Ptr(d.location),
		Properties: &armappcontainers.ContainerAppProperties{
			ManagedEnvironmentID: to.Ptr(cfg.EnvironmentID),
			Configuration: &armappcontainers.Configuration{
				// Multiple-revision mode keeps prior revisions around so rollback can
				// shift traffic back to one; each deploy routes 100% to the new revision.
				ActiveRevisionsMode: to.Ptr(armappcontainers.ActiveRevisionsModeMultiple),
				Ingress: &armappcontainers.Ingress{
					External:   to.Ptr(true),
					TargetPort: to.Ptr(cfg.Port),
					Traffic: []*armappcontainers.TrafficWeight{{
						LatestRevision: to.Ptr(true),
						Weight:         to.Ptr(int32(100)),
					}},
				},
				Registries: []*armappcontainers.RegistryCredentials{{
					Server:            to.Ptr(cfg.RegistryServer),
					Username:          to.Ptr(cfg.RegistryUsername),
					PasswordSecretRef: to.Ptr(registrySecretName),
				}},
				Secrets: secrets,
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

// PreviousRevision returns the name of the revision to roll back to: the newest
// active revision older than the one currently serving traffic. Returns "" if there's
// nothing to roll back to.
func (d *Deployer) PreviousRevision(ctx context.Context, appName string) (string, error) {
	pager := d.revs.NewListRevisionsPager(d.resourceGroup, appName, nil)
	var revs []*armappcontainers.Revision
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return "", errors.Errorf("failed to list Container App revisions: %w", err)
		}
		revs = append(revs, page.Value...)
	}
	return previousActiveRevision(revs, servingRevisionName(revs)), nil
}

// servingRevisionName returns the revision currently receiving traffic: the one with
// the highest positive weight (an explicit pin), else the newest active revision (the
// default "route to latest"). This mirrors the Cloud Run twin so previousActiveRevision
// never mistakes the current revision for its own predecessor.
func servingRevisionName(revs []*armappcontainers.Revision) string {
	var best *armappcontainers.Revision
	for _, r := range revs {
		if r == nil || r.Properties == nil || r.Properties.TrafficWeight == nil {
			continue
		}
		if best == nil || *r.Properties.TrafficWeight > *best.Properties.TrafficWeight {
			best = r
		}
	}
	if best != nil && best.Properties.TrafficWeight != nil && *best.Properties.TrafficWeight > 0 {
		return revName(best)
	}
	return newestActiveRevision(revs, "")
}

// previousActiveRevision returns the newest active revision older than `current`.
// Pure so it's unit-testable.
func previousActiveRevision(revs []*armappcontainers.Revision, current string) string {
	var currentCreate *time.Time
	for _, r := range revs {
		if r != nil && r.Properties != nil && revName(r) == current {
			currentCreate = r.Properties.CreatedTime
			break
		}
	}
	return newestActiveRevision(revs, current, currentCreate)
}

// newestActiveRevision returns the newest active revision, excluding `exclude` and
// (if olderThan is a non-nil time) any revision not strictly older than it.
func newestActiveRevision(revs []*armappcontainers.Revision, exclude string, olderThan ...*time.Time) string {
	var cutoff *time.Time
	if len(olderThan) > 0 {
		cutoff = olderThan[0]
	}
	var best *armappcontainers.Revision
	for _, r := range revs {
		if r == nil || r.Properties == nil || revName(r) == exclude {
			continue
		}
		if r.Properties.Active == nil || !*r.Properties.Active || r.Properties.CreatedTime == nil {
			continue
		}
		if cutoff != nil && !r.Properties.CreatedTime.Before(*cutoff) {
			continue
		}
		if best == nil || r.Properties.CreatedTime.After(*best.Properties.CreatedTime) {
			best = r
		}
	}
	return revName(best)
}

// revName is the revision's short name (the ARM Name field is already the short
// revision id for a revision sub-resource).
func revName(r *armappcontainers.Revision) string {
	if r == nil || r.Name == nil {
		return ""
	}
	return *r.Name
}

// RollbackToRevision routes 100% of the app's traffic to a specific revision. It GETs
// the app and updates only the ingress traffic, so the rest of the config is preserved.
func (d *Deployer) RollbackToRevision(ctx context.Context, appName, revision string) error {
	got, err := d.apps.Get(ctx, d.resourceGroup, appName, nil)
	if err != nil {
		return errors.Errorf("failed to load Container App for rollback: %w", err)
	}
	app := got.ContainerApp
	if app.Properties == nil || app.Properties.Configuration == nil || app.Properties.Configuration.Ingress == nil {
		return errors.Errorf("Container App %q has no ingress to roll back", appName)
	}
	app.Properties.Configuration.Ingress.Traffic = []*armappcontainers.TrafficWeight{{
		RevisionName: to.Ptr(revision),
		Weight:       to.Ptr(int32(100)),
	}}

	poller, err := d.apps.BeginCreateOrUpdate(ctx, d.resourceGroup, appName, app, nil)
	if err != nil {
		return errors.Errorf("failed to route Container App traffic to revision %q: %w", revision, err)
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		return errors.Errorf("failed waiting for Container App rollback to %q: %w", revision, err)
	}
	return nil
}

// secretName maps an env-var name to a valid Container Apps secret name: lowercase
// alphanumeric and '-', starting and ending alphanumeric (e.g. "DATABASE_URL" →
// "database-url").
func secretName(envVar string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(envVar) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		name = "secret"
	}
	return name
}

// ingressFqdn extracts the app's public hostname from a ContainerApp result.
func ingressFqdn(app armappcontainers.ContainerApp) string {
	if app.Properties == nil || app.Properties.Configuration == nil || app.Properties.Configuration.Ingress == nil || app.Properties.Configuration.Ingress.Fqdn == nil {
		return ""
	}
	return *app.Properties.Configuration.Ingress.Fqdn
}
