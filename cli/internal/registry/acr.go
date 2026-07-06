package registry

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/go-errors/errors"
)

// acrRegistry pushes images to an Azure Container Registry in the user's own
// subscription. It ensures the registry exists (creating a Basic, admin-enabled
// registry on demand) and returns the admin credentials for the docker push.
// Admin credentials are the reliable v1 path; the AAD token-exchange flow (no admin
// user) is a follow-up.
type acrRegistry struct {
	client        *armcontainerregistry.RegistriesClient
	resourceGroup string
	registryName  string
	location      string
}

// NewACR builds an ACR registry from the user's Azure credentials.
func NewACR(cred azcore.TokenCredential, subscriptionID, resourceGroup, location, registryName string) (Registry, error) {
	client, err := armcontainerregistry.NewRegistriesClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, errors.Errorf("failed to build ACR client: %w", err)
	}
	return &acrRegistry{
		client:        client,
		resourceGroup: resourceGroup,
		registryName:  strings.ToLower(registryName),
		location:      location,
	}, nil
}

func (a *acrRegistry) Name() string { return "acr" }

// loginServer is the registry's docker host, e.g. "prodacr.azurecr.io".
func (a *acrRegistry) loginServer() string { return a.registryName + ".azurecr.io" }

func (a *acrRegistry) Ref(project, tag string) (string, error) {
	img := Sanitize(project)
	if img == "" {
		return "", errors.Errorf("invalid image name from project %q", project)
	}
	if !tagRe.MatchString(tag) {
		return "", errors.Errorf("invalid image tag %q", tag)
	}
	return fmt.Sprintf("%s/%s:%s", a.loginServer(), img, tag), nil
}

func (a *acrRegistry) Credentials(ctx context.Context, project string) (Credentials, error) {
	img := Sanitize(project)
	if img == "" {
		return Credentials{}, errors.Errorf("invalid image name from project %q", project)
	}

	loginServer, err := a.ensureRegistry(ctx)
	if err != nil {
		return Credentials{}, err
	}

	creds, err := a.client.ListCredentials(ctx, a.resourceGroup, a.registryName, nil)
	if err != nil {
		return Credentials{}, errors.Errorf("failed to get ACR admin credentials: %w", err)
	}
	if creds.Username == nil || len(creds.Passwords) == 0 || creds.Passwords[0].Value == nil {
		return Credentials{}, errors.Errorf("ACR %q returned no admin credentials", a.registryName)
	}

	return Credentials{
		URL:        loginServer,
		AuthServer: loginServer,
		Repository: img,
		Username:   *creds.Username,
		Token:      *creds.Passwords[0].Value,
	}, nil
}

// ensureRegistry returns the registry's login server, creating a Basic,
// admin-enabled registry if it doesn't exist yet.
func (a *acrRegistry) ensureRegistry(ctx context.Context) (string, error) {
	got, err := a.client.Get(ctx, a.resourceGroup, a.registryName, nil)
	if err == nil {
		if got.Properties != nil && got.Properties.LoginServer != nil {
			return *got.Properties.LoginServer, nil
		}
		return a.loginServer(), nil
	}

	// Only create on a genuine 404 — a permission/transient error shouldn't trigger
	// a create that then fails misleadingly.
	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 404 {
		return "", errors.Errorf("failed to look up ACR registry %q: %w", a.registryName, err)
	}

	poller, err := a.client.BeginCreate(ctx, a.resourceGroup, a.registryName, armcontainerregistry.Registry{
		Location: to.Ptr(a.location),
		SKU:      &armcontainerregistry.SKU{Name: to.Ptr(armcontainerregistry.SKUNameBasic)},
		Properties: &armcontainerregistry.RegistryProperties{
			AdminUserEnabled: to.Ptr(true),
		},
	}, nil)
	if err != nil {
		return "", errors.Errorf("failed to create ACR registry %q (does resource group %q exist?): %w", a.registryName, a.resourceGroup, err)
	}
	res, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return "", errors.Errorf("failed waiting for ACR registry %q to be created: %w", a.registryName, err)
	}
	if res.Properties != nil && res.Properties.LoginServer != nil {
		return *res.Properties.LoginServer, nil
	}
	return a.loginServer(), nil
}
