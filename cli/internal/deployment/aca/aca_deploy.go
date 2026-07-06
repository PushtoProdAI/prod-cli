package aca

import (
	"context"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/go-errors/errors"
	"github.com/pushtoprodai/prod-cli/internal/auth"
	"github.com/pushtoprodai/prod-cli/internal/deployment"
	"github.com/pushtoprodai/prod-cli/internal/deployment/managedcontainer"
	prodreg "github.com/pushtoprodai/prod-cli/internal/registry"
)

const (
	defaultPort   int32   = 8080
	defaultCPU    float64 = 0.5 // cores (0.5 CPU + 1Gi is a valid Container Apps combo)
	defaultMemory         = "1Gi"
	defaultEnv            = "prod-env" // Container Apps environment (override: PROD_AZURE_ACA_ENV)
)

// Deployment deploys a project to Azure Container Apps.
type Deployment struct {
	spec      *deployment.DeploymentSpec
	dockerGen *deployment.DockerGenerator
	writer    io.Writer
}

var _ deployment.Deployable = (*Deployment)(nil)

// NewContainerAppsDeployment builds a Container Apps deployable for a project spec.
func NewContainerAppsDeployment(spec *deployment.DeploymentSpec, dockerGen *deployment.DockerGenerator, writer io.Writer) *Deployment {
	return &Deployment{spec: spec, dockerGen: dockerGen, writer: writer}
}

// Deploy runs the shared managed-container flow with Container Apps as the provider.
func (d *Deployment) Deploy(ctx context.Context) ([]deployment.CreatedResource, error) {
	return managedcontainer.Run(ctx, d, d.spec, d.dockerGen)
}

// ResourceType is the primary CreatedResource type for Container Apps.
func (d *Deployment) ResourceType() string { return "containerapp" }

// Prepare resolves Azure credentials, ensures the resource group + ACR, and returns a
// deploy step that ensures the managed environment and creates/updates the app.
func (d *Deployment) Prepare(ctx context.Context, spec *deployment.DeploymentSpec) (prodreg.Registry, managedcontainer.DeployFunc, error) {
	cred, subscription, resourceGroup, location, err := auth.NewAzureAuth(d.writer).Config(ctx)
	if err != nil {
		return nil, nil, err
	}
	dep, err := New(cred, subscription, resourceGroup, location)
	if err != nil {
		return nil, nil, err
	}
	// The ACR lives in the resource group, so ensure the group before the registry.
	if err := dep.EnsureResourceGroup(ctx); err != nil {
		return nil, nil, err
	}
	acrName := os.Getenv("PROD_AZURE_ACR")
	if acrName == "" {
		acrName = deriveACRName(resourceGroup)
	}
	reg, err := prodreg.NewACR(cred, subscription, resourceGroup, location, acrName)
	if err != nil {
		return nil, nil, err
	}

	deploy := func(ctx context.Context, imageRef string) (managedcontainer.DeployResult, error) {
		// Pull credentials for the app to fetch the image from the user's ACR.
		acrCreds, err := reg.Credentials(ctx, spec.Name)
		if err != nil {
			return managedcontainer.DeployResult{}, err
		}
		envName := os.Getenv("PROD_AZURE_ACA_ENV")
		if envName == "" {
			envName = defaultEnv
		}
		envID, err := dep.EnsureEnvironment(ctx, envName)
		if err != nil {
			return managedcontainer.DeployResult{}, err
		}

		name := containerAppName(spec.Name)
		plainEnv, secretEnv := partitionEnv(spec.EnvVars)
		url, err := dep.Deploy(ctx, AppConfig{
			Name:             name,
			EnvironmentID:    envID,
			Image:            imageRef,
			Port:             defaultPort,
			CPU:              defaultCPU,
			Memory:           defaultMemory,
			PlainEnv:         plainEnv,
			SecretEnv:        secretEnv,
			RegistryServer:   acrCreds.URL,
			RegistryUsername: acrCreds.Username,
			RegistryPassword: acrCreds.Token,
		})
		if err != nil {
			return managedcontainer.DeployResult{}, err
		}
		return managedcontainer.DeployResult{ID: name, Name: name, URL: url}, nil
	}
	return reg, deploy, nil
}

// GetPreviousDeployment returns the revision to roll back to (the active revision
// before the current one), or nil if there's nothing to roll back to.
func (d *Deployment) GetPreviousDeployment(ctx context.Context) (*deployment.DeploymentInfo, error) {
	dep, name, err := d.deployer(ctx)
	if err != nil {
		return nil, err
	}
	rev, err := dep.PreviousRevision(ctx, name)
	if err != nil {
		return nil, err
	}
	if rev == "" {
		return nil, nil
	}
	return &deployment.DeploymentInfo{ID: rev, Status: "previous revision"}, nil
}

// Rollback routes all traffic back to targetRevision (Container Apps keeps revisions,
// so rollback is instant — no rebuild).
func (d *Deployment) Rollback(ctx context.Context, targetRevision string) error {
	if targetRevision == "" {
		return errors.Errorf("no previous Container App revision to roll back to")
	}
	dep, name, err := d.deployer(ctx)
	if err != nil {
		return err
	}
	return dep.RollbackToRevision(ctx, name, targetRevision)
}

// deployer resolves the user's Azure credentials and builds a Deployer + the app
// name, shared by the rollback methods.
func (d *Deployment) deployer(ctx context.Context) (*Deployer, string, error) {
	cred, subscription, resourceGroup, location, err := auth.NewAzureAuth(d.writer).Config(ctx)
	if err != nil {
		return nil, "", err
	}
	dep, err := New(cred, subscription, resourceGroup, location)
	if err != nil {
		return nil, "", err
	}
	return dep, containerAppName(d.spec.Name), nil
}

// partitionEnv splits env vars into non-sensitive (set inline on the container) and
// sensitive (stored as Container Apps secrets and referenced by name). PORT is forced
// into the plain set so the app listens where the ingress routes.
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
	plain["PORT"] = strconv.FormatInt(int64(defaultPort), 10)
	return plain, secret
}

// containerAppName produces a valid Container Apps name from a project name:
// lowercase alphanumeric + hyphens, starting with a letter, ending alphanumeric,
// ≤32 chars. Azure rejects anything else.
func containerAppName(raw string) string {
	name := prodreg.Sanitize(raw)
	if name == "" || name[0] < 'a' || name[0] > 'z' {
		name = "app-" + name // guarantee a letter start
	}
	if len(name) > 32 {
		name = name[:32]
	}
	name = strings.Trim(name, "-") // can't start/end with a hyphen (incl. after truncation)
	if name == "" {
		name = "app"
	}
	return name
}

// deriveACRName builds a best-effort globally-unique-ish ACR name (5-50 alphanumeric,
// no hyphens) from the resource group. ACR names are global, so PROD_AZURE_ACR should
// be set for anything real; this is the fallback.
func deriveACRName(resourceGroup string) string {
	var b strings.Builder
	b.WriteString("prodacr")
	for _, r := range strings.ToLower(resourceGroup) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	name := b.String()
	if len(name) > 50 {
		name = name[:50]
	}
	return name
}
