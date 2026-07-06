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

// Deploy resolves the user's Azure credentials, ensures the resource group + ACR +
// managed environment, builds+pushes the image, and creates/updates the container app.
func (d *Deployment) Deploy(ctx context.Context) ([]deployment.CreatedResource, error) {
	cred, subscription, resourceGroup, location, err := auth.NewAzureAuth(d.writer).Config(ctx)
	if err != nil {
		return nil, err
	}

	dep, err := New(cred, subscription, resourceGroup, location)
	if err != nil {
		return nil, err
	}
	if err := dep.EnsureResourceGroup(ctx); err != nil {
		return nil, err
	}

	// Build locally and push to the user's Azure Container Registry.
	acrName := os.Getenv("PROD_AZURE_ACR")
	if acrName == "" {
		acrName = deriveACRName(resourceGroup)
	}
	reg, err := prodreg.NewACR(cred, subscription, resourceGroup, location, acrName)
	if err != nil {
		return nil, err
	}
	buildContext, _ := d.spec.Metadata["buildContext"].(string)
	_, pushResult, err := d.dockerGen.BuildAndPushToRegistry(ctx, d.spec, buildContext, reg)
	if err != nil {
		return nil, errors.Errorf("failed to build and push image to Azure Container Registry: %w", err)
	}

	// The app needs pull credentials for the registry.
	acrCreds, err := reg.Credentials(ctx, d.spec.Name)
	if err != nil {
		return nil, err
	}

	envName := os.Getenv("PROD_AZURE_ACA_ENV")
	if envName == "" {
		envName = defaultEnv
	}
	envID, err := dep.EnsureEnvironment(ctx, envName)
	if err != nil {
		return nil, err
	}

	name := containerAppName(d.spec.Name)
	url, err := dep.Deploy(ctx, AppConfig{
		Name:             name,
		EnvironmentID:    envID,
		Image:            pushResult.PushedImageURL,
		Port:             defaultPort,
		CPU:              defaultCPU,
		Memory:           defaultMemory,
		EnvVars:          envMap(d.spec.EnvVars),
		RegistryServer:   acrCreds.URL,
		RegistryUsername: acrCreds.Username,
		RegistryPassword: acrCreds.Token,
	})
	if err != nil {
		return nil, err
	}

	return []deployment.CreatedResource{{
		ID:       name,
		Type:     "containerapp",
		Name:     name,
		Primary:  true,
		Metadata: map[string]any{"url": url},
	}}, nil
}

// GetPreviousDeployment is not yet implemented for Container Apps.
func (d *Deployment) GetPreviousDeployment(_ context.Context) (*deployment.DeploymentInfo, error) {
	return nil, nil
}

// Rollback is not yet implemented for Container Apps. (Container Apps keeps
// revisions, so real traffic-to-revision rollback is a planned fast-follow.)
func (d *Deployment) Rollback(_ context.Context, _ string) error {
	return errors.Errorf("Azure Container Apps rollback isn't supported yet")
}

// envMap flattens env vars and forces PORT to the container port so the app listens
// where the ingress routes. (Sensitive values are set as plain env for now; Key
// Vault / secret refs are a planned fast-follow.)
func envMap(vars []deployment.EnvVar) map[string]string {
	m := map[string]string{}
	for _, v := range vars {
		m[v.Name] = v.Value
	}
	m["PORT"] = strconv.FormatInt(int64(defaultPort), 10)
	return m
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
