package plugincmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/conduitio/ecdysis"
	"github.com/go-errors/errors"
)

var pluginNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// PluginNewCommand scaffolds a new provider plugin as a standalone Go module.
type PluginNewCommand struct{ name string }

func (c *PluginNewCommand) Usage() string { return "new" }

func (c *PluginNewCommand) Docs() ecdysis.Docs {
	return ecdysis.Docs{
		Short: "Scaffold a new provider plugin",
		Long: "Generate a buildable provider-plugin module in ./prod-provider-<name>/ with the six\n" +
			"plugin.Provider methods stubbed. Implement them against your cloud's API, build it, then\n" +
			"`prod plugin install ./prod-provider-<name>` — no fork of prod required.",
	}
}

func (c *PluginNewCommand) Args(args []string) error {
	if len(args) != 1 {
		return errors.New("new requires exactly one argument: the plugin name (e.g. `prod plugin new acme`)")
	}
	c.name = args[0]
	return nil
}

func (c *PluginNewCommand) Execute(context.Context) error {
	name := strings.ToLower(strings.TrimSpace(c.name))
	if !pluginNameRE.MatchString(name) {
		return errors.Errorf("invalid plugin name %q: use lowercase letters, digits, and hyphens, starting with a letter (e.g. acme, acme-cloud)", c.name)
	}

	dir := "prod-provider-" + name
	if _, err := os.Stat(dir); err == nil {
		return errors.Errorf("%s already exists — pick another name or remove it first", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return errors.Errorf("failed to create %s: %w", dir, err)
	}

	files := map[string]string{
		"go.mod":     scaffoldGoMod(name),
		"main.go":    scaffoldMainGo(name),
		"README.md":  scaffoldReadme(name),
		".gitignore": dir + "\n",
	}
	for fname, content := range files {
		if err := os.WriteFile(filepath.Join(dir, fname), []byte(content), 0o644); err != nil {
			return errors.Errorf("failed to write %s: %w", fname, err)
		}
	}

	fmt.Printf(`Created ./%s/

Next steps:
  cd %s
  go mod tidy          # resolve the prod plugin SDK
  go build             # produces the %s binary
  prod plugin install ./%s

Then implement the six methods in main.go against your cloud's API — the stub deploys to a
placeholder URL so you can wire it up incrementally.
`, dir, dir, dir, dir)
	return nil
}

// scaffoldGoMod emits a minimal module file; `go mod tidy` resolves the SDK (a lean module
// that only pulls in hashicorp/go-plugin) from the import.
func scaffoldGoMod(name string) string {
	return fmt.Sprintf("module prod-provider-%s\n\ngo 1.25\n", name)
}

func title(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func scaffoldMainGo(name string) string {
	typeName := strings.ReplaceAll(name, "-", "") + "Provider"
	display := title(strings.ReplaceAll(name, "-", " "))
	return fmt.Sprintf(`// Command prod-provider-%[1]s is a prod provider plugin for %[2]s.
//
// Implement the six plugin.Provider methods against %[2]s's API, build the binary
// (go build), and install it with: prod plugin install ./prod-provider-%[1]s
// prod discovers and drives it over a subprocess — no fork of prod required.
package main

import (
	"context"
	"fmt"

	plugin "github.com/pushtoprodai/prod-plugin-sdk"
)

type %[3]s struct{}

// Metadata describes the platform. Called first — set the display name, the aliases the
// user might type, the hostname suffix, and whether rollback is supported.
func (%[3]s) Metadata(context.Context) (plugin.Meta, error) {
	return plugin.Meta{
		Name:             %[4]q,
		Aliases:          []string{%[1]q},
		DomainSuffix:     ".%[1]s.example",
		SupportsRollback: false,
		// Shapes declares which deploy shapes this provider serves. Omit (or leave as
		// web) for a normal URL-serving web service. For a worker/agent runtime that may
		// return no URL, declare it — e.g.:
		//   Shapes: []plugin.DeployShape{plugin.ShapeWorker, plugin.ShapeMCPServer},
		Shapes: []plugin.DeployShape{plugin.ShapeWeb},
	}, nil
}

// RegistryInfo returns the container registry + push credentials. prod builds and pushes
// the image there, then calls Deploy with the resulting image reference.
func (%[3]s) RegistryInfo(_ context.Context, project string) (plugin.RegistryInfo, error) {
	// TODO: return your registry host + short-lived push credentials.
	return plugin.RegistryInfo{
		Host:       "registry.%[1]s.example",
		Repository: project,
		Username:   "TODO",
		Token:      "TODO",
	}, nil
}

// CheckAuth reports whether the user's credentials are usable, so prod can fail fast with
// a clear message before building.
func (%[3]s) CheckAuth(context.Context) (plugin.AuthStatus, error) {
	// TODO: validate the user's credentials (e.g. read a token from the environment).
	return plugin.AuthStatus{OK: true, Detail: "TODO: validate credentials"}, nil
}

// Deploy creates or updates the service from the pushed image and returns it once live.
func (%[3]s) Deploy(_ context.Context, req plugin.DeployRequest) (plugin.DeployResult, error) {
	// TODO: create/update a service from req.ImageRef and wait until it serves. The stub
	// echoes a placeholder URL so you can install and run the plugin before it's finished.
	//
	// req.Shape is the shape the host resolved. For a non-HTTP shape (worker/cron) you may
	// skip allocating a public URL and return DeployResult{URL:""}; set DeployResult.Shape
	// to echo the shape you actually deployed (authoritative over req.Shape).
	return plugin.DeployResult{
		ID:   %[1]q + "-" + req.Name,
		Name: req.Name,
		URL:  fmt.Sprintf("https://%%s.%[1]s.example", req.Name),
	}, nil
}

// PreviousDeployment returns the currently-live revision, so prod can offer rollback.
func (%[3]s) PreviousDeployment(context.Context, string) (plugin.DeployInfo, error) {
	// TODO: return the previous revision's identifier (empty = no previous / first deploy).
	return plugin.DeployInfo{}, nil
}

// Rollback reverts the service to a previous revision.
func (%[3]s) Rollback(context.Context, string, string) error {
	// TODO: implement, and set SupportsRollback:true in Metadata once you do.
	return fmt.Errorf("%[1]s: rollback not implemented yet")
}

func main() {
	plugin.Serve(%[3]s{})
}
`, name, display, typeName, title(display))
}

func scaffoldReadme(name string) string {
	return fmt.Sprintf("# prod-provider-%[1]s\n\n"+
		"A [prod](https://github.com/PushtoProdAI/prod-cli) provider plugin that adds %[1]s as a deploy target.\n\n"+
		"## Build & install\n\n"+
		"```bash\n"+
		"go mod tidy\n"+
		"go build            # produces ./prod-provider-%[1]s\n"+
		"prod plugin install ./prod-provider-%[1]s\n"+
		"```\n\n"+
		"Then deploy to it like any built-in cloud:\n\n"+
		"```bash\n"+
		"prod \"deploy this to %[1]s\"\n"+
		"```\n\n"+
		"## Implement\n\n"+
		"Fill in the six `plugin.Provider` methods in `main.go` (each has a `TODO`). The stub is\n"+
		"buildable and installable as-is; `Deploy` returns a placeholder URL until you wire it to\n"+
		"your cloud's API.\n", name)
}
