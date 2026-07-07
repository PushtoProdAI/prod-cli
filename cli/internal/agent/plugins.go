package agent

import (
	"encoding/hex"
	"io"
	"log/slog"
	"strings"

	"github.com/go-errors/errors"
	"github.com/pushtoprodai/prod-cli/internal/auth"
	"github.com/pushtoprodai/prod-cli/internal/deployment"
	"github.com/pushtoprodai/prod-cli/internal/pluginhost"
	plugin "github.com/pushtoprodai/prod-plugin-sdk"
)

// RegisterDiscoveredPlugins reads the plugin manifest under homeDir and registers each
// installed provider plugin into the catalog. It is an EXPLICIT startup call (not an
// init(), so tests don't read the user's real ~/.prod). A plugin that fails to
// register is logged and skipped — a bad plugin never blocks prod. Idempotent.
func RegisterDiscoveredPlugins(homeDir string) {
	entries, err := pluginhost.LoadManifest(pluginhost.DefaultManifestPath(homeDir))
	if err != nil {
		slog.Warn("failed to load plugin manifest", "error", err)
		return
	}
	for _, e := range entries {
		if err := registerPlugin(e); err != nil {
			slog.Warn("skipping plugin", "name", e.Name, "error", err)
		}
	}
}

// registerPlugin turns one manifest entry into a catalog PlatformSpec whose factories
// launch the plugin subprocess lazily.
func registerPlugin(e pluginhost.Entry) error {
	if e.Name == "" || e.Path == "" {
		return errors.Errorf("manifest entry is missing a name or path")
	}
	platform := pluginPlatform(e.Name)
	if _, exists := LookupPlatform(platform); exists {
		return errors.Errorf("plugin %q collides with an already-registered platform", e.Name)
	}
	// A plugin's name and aliases must not shadow a built-in or another plugin.
	if _, ok := PlatformByString(e.Name); ok {
		return errors.Errorf("plugin name %q collides with an existing platform", e.Name)
	}
	aliases := lowerAll(e.Aliases)
	for _, a := range aliases {
		if _, ok := PlatformByString(a); ok {
			return errors.Errorf("plugin %q alias %q collides with an existing platform", e.Name, a)
		}
	}

	// A checksum is mandatory — the binary is verified against it at launch, so an
	// unverified plugin is never run (docs/plugins.md's trust model).
	if e.Checksum == "" {
		return errors.Errorf("plugin %q has no checksum — reinstall it", e.Name)
	}
	checksum, err := hex.DecodeString(e.Checksum)
	if err != nil || len(checksum) == 0 {
		return errors.Errorf("plugin %q has an invalid checksum", e.Name)
	}
	meta := plugin.Meta{Name: e.Name, Aliases: aliases, DomainSuffix: e.DomainSuffix, SupportsRollback: e.SupportsRollback}
	launch := func() (plugin.Provider, func(), error) { return pluginhost.Launch(e.Path, checksum) }

	RegisterPlatform(PlatformSpec{
		Platform:         platform,
		Name:             e.Name,
		Aliases:          aliases,
		DomainSuffix:     e.DomainSuffix,
		SupportsRollback: e.SupportsRollback,
		ManagedContainer: true, // plugins deploy through the shared container workflow
		NewDeployable: func(a *Activities, spec *deployment.DeploymentSpec) (deployment.Deployable, error) {
			dockerGen := deployment.NewDockerGenerator(a.uiWriter, spec.EnvVars)
			return pluginhost.NewDeployable(launch, meta, spec, dockerGen, a.uiWriter), nil
		},
		NewAuthProvider: func(out io.Writer) auth.AuthProvider {
			return pluginhost.NewAuthProvider(launch, out)
		},
		NewDetector: nil, // plugin deploys are create-or-update; no pre-detection
	})
	return nil
}

func lowerAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = strings.ToLower(strings.TrimSpace(s))
	}
	return out
}
