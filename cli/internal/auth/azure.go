package auth

import (
	"context"
	"io"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/go-errors/errors"
)

// azureMgmtScope is the scope for the Azure Resource Manager (Container Apps, ACR).
const azureMgmtScope = "https://management.azure.com/.default"

// AzureAuth resolves the user's Azure credentials from the DefaultAzureCredential
// chain: the Azure CLI (`az login`), environment variables (AZURE_CLIENT_ID/…),
// managed identity, etc. There is no backend and no central account — prod deploys
// into the user's own Azure subscription with their own credentials, like the az CLI.
type AzureAuth struct {
	out           io.Writer
	subscription  string // optional override; else AZURE_SUBSCRIPTION_ID
	resourceGroup string // optional override; else PROD_AZURE_RESOURCE_GROUP / prod-apps
	location      string // optional override; else PROD_AZURE_LOCATION / eastus
}

var _ AuthProvider = (*AzureAuth)(nil)

func NewAzureAuth(out io.Writer) *AzureAuth { return &AzureAuth{out: out} }

func (a *AzureAuth) SetSubscription(id string)  { a.subscription = id }
func (a *AzureAuth) SetResourceGroup(rg string) { a.resourceGroup = rg }
func (a *AzureAuth) SetLocation(loc string)     { a.location = loc }

// Config resolves the DefaultAzureCredential, subscription, resource group, and
// location. The Container Apps / ACR clients build from the returned credential.
func (a *AzureAuth) Config(ctx context.Context) (cred azcore.TokenCredential, subscription, resourceGroup, location string, err error) {
	c, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, "", "", "", errors.Errorf("no Azure credentials — run `az login` or set AZURE_CLIENT_ID/AZURE_TENANT_ID/AZURE_CLIENT_SECRET: %w", err)
	}

	subscription = firstNonEmpty(a.subscription, os.Getenv("AZURE_SUBSCRIPTION_ID"))
	if subscription == "" {
		return nil, "", "", "", errors.Errorf("no Azure subscription — set AZURE_SUBSCRIPTION_ID (see `az account show`)")
	}

	resourceGroup = firstNonEmpty(a.resourceGroup, os.Getenv("PROD_AZURE_RESOURCE_GROUP"), "prod-apps")
	location = firstNonEmpty(a.location, os.Getenv("PROD_AZURE_LOCATION"), "eastus")
	return c, subscription, resourceGroup, location, nil
}

// CheckAuthentication reports whether usable Azure credentials are configured by
// resolving the chain and acquiring a management token.
func (a *AzureAuth) CheckAuthentication(ctx context.Context) (bool, error) {
	cred, _, _, _, err := a.Config(ctx)
	if err != nil {
		return false, err
	}
	if _, err := cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{azureMgmtScope}}); err != nil {
		return false, errors.Errorf("Azure credentials found but not usable — run `az login`: %w", err)
	}
	return true, nil
}

func (a *AzureAuth) ValidateAPIKey(_ context.Context, _ string) (bool, error) { return false, nil }

func (a *AzureAuth) PerformOAuthLogin(_ context.Context) error {
	return errors.Errorf("Azure uses your local credentials — run `az login`; there's nothing to log in to here")
}

func (a *AzureAuth) APIKeyPrompt() string { return "" }
