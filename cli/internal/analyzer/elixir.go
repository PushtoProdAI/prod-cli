package analyzer

import (
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-errors/errors"
)

// Elixir env access: System.get_env("X"), System.fetch_env("X"), System.fetch_env!("X"). The var
// name lands in the single capture group.
const elixirEnvVarRegex = `System\.(?:get_env|fetch_env!?)\(\s*"([A-Za-z_][A-Za-z0-9_]*)"`

// HTTP routes across the Phoenix router DSL (in *_web/router.ex, usually inside `scope` blocks):
// `get "/…"`, `post`, `put`, `patch`, `delete`, and `live "/…"` (LiveView). The verb macro and the
// path each land in a capture group; ElixirRouteProcessor maps the macro to a method (`live` → GET,
// since a LiveView mounts over an HTTP GET). Phoenix route paths are absolute, so no relative-path
// fixups are needed beyond the processor's guards.
const elixirRouteRegex = `(?m)^\s*(get|post|put|patch|delete|live)\s+"([^"]+)"`

// elixirDepRegex captures the atom name of each Mix dependency tuple `{:dep_name, ...}`. Dep atoms
// are lowercase snake_case; the leading `{` keeps it from matching plain atoms like the `app:`
// value or option atoms elsewhere in mix.exs.
var elixirDepRegex = regexp.MustCompile(`\{\s*:([a-z][a-zA-Z0-9_]*)\s*,`)

// elixirAppRegex captures the `app:` atom from the `def project` keyword list in mix.exs. This is
// the OTP application name, which is ALSO the name Mix gives the assembled release directory
// (_build/prod/rel/<app>) — so the Dockerfile's release-copy path keys off it.
var elixirAppRegex = regexp.MustCompile(`(?m)app:\s*:([a-z_][a-zA-Z0-9_]*)`)

// elixirServiceMarkers maps a Hex package to the backing service it implies. Ecto's Postgres
// adapter is postgrex (also pulled by ecto_sql for Postgres apps); MySQL is myxql; Redis is redix.
var elixirServiceMarkers = []struct {
	dep     string
	service ServiceRequirement
}{
	{"postgrex", ServicePostgres},
	{"ecto_sql", ServicePostgres},
	{"myxql", ServiceMySQL},
	{"redix", ServiceRedis},
}

// elixirFrameworks maps a web-framework/server package to its provider label. Order matters:
// Phoenix wins (it sits on top of Plug + a server), then the standalone servers Bandit/Cowboy and
// plain Plug for thinner apps.
var elixirFrameworks = []struct {
	dep      string
	provider string
}{
	{"phoenix", "phoenix"},
	{"bandit", "bandit"},
	{"plug_cowboy", "plug"},
	{"plug", "plug"},
	{"cowboy", "cowboy"},
}

// ElixirAnalyzer implements Analyzer for Elixir projects, Phoenix first. Elixir deploys as an OTP
// release (like a self-contained binary), so the elixir.dockerfile template does the heavy lifting
// — mix deps.get/compile, `mix assets.deploy` for Phoenix, then `mix release` — and
// BuildCommand/StartCommand here are advisory (plan display).
type ElixirAnalyzer struct {
	ProjectFS projectFS
}

// NewElixirAnalyzer creates an Elixir analyzer instance.
func NewElixirAnalyzer(projectFS projectFS) Analyzer {
	return &ElixirAnalyzer{ProjectFS: projectFS}
}

// CanHandle reports whether this looks like an Elixir project: a mix.exs at the root. A bare
// .ex/.exs file is intentionally NOT enough — plenty of non-Elixir repos carry an incidental
// script — so the build manifest is the sole signal (mirrors Ruby/Rust/Java precision).
func (e *ElixirAnalyzer) CanHandle() (bool, error) {
	if _, err := fs.Stat(e.ProjectFS, "mix.exs"); err == nil {
		return true, nil
	}
	return false, nil
}

// Analyze produces the project spec. The elixir.dockerfile template hard-codes the build (deps,
// assets, `mix release`) and runs the release, so BuildCommand/StartCommand are informational.
func (e *ElixirAnalyzer) Analyze() (*ProjectSpec, error) {
	deps := e.dependencies()

	// _build and deps are rebuilt inside the image; .elixir_ls is editor state.
	ignoreDirs := []string{"_build", "deps", ".elixir_ls", ".git", "node_modules"}
	exts := []string{".ex", ".exs"}

	envVars, err := walkProjectForCandidates(e.ProjectFS, exts, ignoreDirs, regexp.MustCompile(elixirEnvVarRegex), 3, 5)
	if err != nil {
		return nil, errors.Errorf("failed to scan Elixir env vars: %w", err)
	}

	routes, err := walkProjectForRoutes(e.ProjectFS, exts, ignoreDirs, regexp.MustCompile(elixirRouteRegex), NewElixirRouteProcessor(), 3, 5)
	if err != nil {
		return nil, errors.Errorf("failed to scan Elixir routes: %w", err)
	}

	services := e.detectServices(deps)

	// Framework marker mirrors the "framework" convention used by Ruby/Python. It also tells the
	// Dockerfile generator to run Phoenix's asset pipeline + set PHX_SERVER, and tells
	// DetectAgentShape this app serves HTTP so it isn't mislabeled a worker.
	framework := e.detectFramework(deps)
	if framework != "" {
		services = append(services, ServiceRequirement{Type: "framework", Provider: framework})
	}

	migrationContext := e.collectMigrationContext(deps)

	detectedShape := DetectAgentShape(deps, framework != "")

	return &ProjectSpec{
		Name:                e.appName(),
		Language:            "elixir",
		ServiceRequirements: services,
		// Advisory: the Dockerfile hard-codes `mix release` (after deps.get/compile and, for
		// Phoenix, assets.deploy) and starts the release, so these are for the plan display.
		BuildCommand: "mix release",
		// `bin/server` is Phoenix's release start script (generated by `mix phx.gen.release`); the
		// template's CMD runs it for Phoenix and `bin/<app> start` for a plain release.
		StartCommand:     "bin/server",
		EnvVars:          envVars,
		Routes:           routes,
		MigrationContext: migrationContext,
		DetectedShape:    detectedShape,
	}, nil
}

// dependencies returns the Hex package atoms declared in mix.exs (`{:name, …}`), lowercased.
func (e *ElixirAnalyzer) dependencies() []string {
	data, err := fs.ReadFile(e.ProjectFS, "mix.exs")
	if err != nil {
		return nil
	}
	var deps []string
	seen := map[string]bool{}
	for _, m := range elixirDepRegex.FindAllStringSubmatch(string(data), -1) {
		name := strings.ToLower(m[1])
		if !seen[name] {
			seen[name] = true
			deps = append(deps, name)
		}
	}
	return deps
}

// appName is the OTP application name from mix.exs's `app:` key, falling back to the project
// directory basename. Mix names the release directory after this, so it must match the
// _build/prod/rel/<app> path the Dockerfile copies out.
func (e *ElixirAnalyzer) appName() string {
	if data, err := fs.ReadFile(e.ProjectFS, "mix.exs"); err == nil {
		if m := elixirAppRegex.FindStringSubmatch(string(data)); m != nil {
			return m[1]
		}
	}
	return filepath.Base(e.ProjectFS.rootPath)
}

// detectServices maps known DB/cache packages to backing-service requirements.
func (e *ElixirAnalyzer) detectServices(deps []string) []ServiceRequirement {
	var services []ServiceRequirement
	seen := map[ServiceRequirement]bool{}
	for _, m := range elixirServiceMarkers {
		if hasDep(deps, m.dep) && !seen[m.service] {
			seen[m.service] = true
			services = append(services, m.service)
		}
	}
	return services
}

// detectFramework returns the provider label of the first recognized web-framework/server package.
func (e *ElixirAnalyzer) detectFramework(deps []string) string {
	for _, f := range elixirFrameworks {
		if hasDep(deps, f.dep) {
			return f.provider
		}
	}
	return ""
}

// collectMigrationContext gathers Ecto migration signals so the planner can decide whether to run
// migrations. Ecto migrations run via the release's `bin/migrate` script (which calls
// App.Release.migrate/0) — NOT `mix ecto.migrate`, since Mix isn't present in an assembled release
// — so the analyzer sets no MigrationCommand; the detected tool is still reported. Ecto keeps
// migrations under priv/repo/migrations rather than a top-level migrations/ dir, so
// FilterConfiguredMigrationTools has an elixir-specific case (see migration.go).
func (e *ElixirAnalyzer) collectMigrationContext(deps []string) MigrationContext {
	migrationContext := MigrationContext{
		MigrationFiles: []string{},
		ORMTools:       []string{},
		ConfigFiles:    make(map[string]string),
		PackageScripts: make(map[string]string),
	}

	detectedTools := DetectORMTools(deps, "elixir")
	migrationFiles, _ := FindMigrationFiles(e.ProjectFS.rootPath)
	migrationContext.MigrationFiles = migrationFiles
	migrationContext.ORMTools = FilterConfiguredMigrationTools(detectedTools, migrationFiles, e.ProjectFS.rootPath)

	return migrationContext
}

// hasDep reports whether name is present in the parsed dependency list.
func hasDep(deps []string, name string) bool {
	for _, d := range deps {
		if d == name {
			return true
		}
	}
	return false
}

// ElixirRouteProcessor turns Phoenix router-macro matches into RouteCandidates, mapping the macro
// to an HTTP verb (`live` → GET, since a LiveView mounts over an HTTP GET) and validating the path.
type ElixirRouteProcessor struct{}

// NewElixirRouteProcessor creates an Elixir route processor.
func NewElixirRouteProcessor() *ElixirRouteProcessor { return &ElixirRouteProcessor{} }

// elixirMacroMethod maps a Phoenix router macro to its HTTP verb.
var elixirMacroMethod = map[string]string{
	"get":    "GET",
	"post":   "POST",
	"put":    "PUT",
	"patch":  "PATCH",
	"delete": "DELETE",
	// A LiveView route is reached by an HTTP GET that mounts the live socket.
	"live": "GET",
}

func (p *ElixirRouteProcessor) ProcessMatch(match RouteMatch, filePath string) []RouteCandidate {
	if len(match.CaptureGroups) < 2 {
		return nil
	}
	macro := strings.ToLower(match.CaptureGroups[0])
	routePath := match.CaptureGroups[1]

	method, ok := elixirMacroMethod[macro]
	if !ok {
		return nil
	}

	// Phoenix router paths are absolute; normalize a relative one and drop implausible values.
	if routePath == "" {
		routePath = "/"
	}
	if !strings.HasPrefix(routePath, "/") {
		routePath = "/" + routePath
	}
	if strings.Contains(routePath, " ") || len(routePath) > 100 {
		return nil
	}

	return []RouteCandidate{{
		Method:  method,
		Path:    routePath,
		File:    filePath,
		Line:    match.Line,
		Context: match.Context,
	}}
}
