package analyzer

import (
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-errors/errors"
)

// Rust env access: std::env::var("X") / env::var("X") (and the _os variants). The var name
// lands in the single capture group.
const rustEnvVarRegex = `(?:std::)?env::var(?:_os)?\(\s*"([A-Za-z_][A-Za-z0-9_]*)"`

// HTTP routes across the popular Rust web frameworks. Axum and Actix register paths with
// `.route("/…", …)`; Poem uses `.at("/…", …)`; Actix and Rocket also carry attribute-macro
// handlers `#[get("/…")]`. Rust route paths are always absolute, so DefaultRouteProcessor's
// leading-"/" rule fits — no custom processor needed.
const rustRouteRegex = `\.route\(\s*"([^"]*)"|` +
	`\.at\(\s*"([^"]*)"|` +
	`#\[(get|post|put|delete|patch|head|options)\(\s*"([^"]*)"`

// depNameLineRegex captures the crate name at the start of a dependency line, covering both
// `axum = "0.7"` / `sqlx = { … }` and the `axum.workspace = true` shorthand.
var depNameLineRegex = regexp.MustCompile(`^([A-Za-z0-9_-]+)\s*[.=]`)

// rustServiceMarkers maps a crate to the backing service it implies. SQLite is intentionally
// omitted — it's file-local, not a provisioned service (mirrors goServiceMarkers).
var rustServiceMarkers = []struct {
	crate   string
	service ServiceRequirement
}{
	{"sqlx", ServicePostgres},
	{"tokio-postgres", ServicePostgres},
	{"sea-orm", ServicePostgres},
	{"diesel", ServicePostgres},
	{"redis", ServiceRedis},
}

// rustFrameworks maps a web-framework crate to its provider label. Order matters: actix-web is
// checked before the bare "actix" so the more specific crate wins.
var rustFrameworks = []struct {
	crate    string
	provider string
}{
	{"axum", "axum"},
	{"actix-web", "actix"},
	{"actix", "actix"},
	{"rocket", "rocket"},
	{"poem", "poem"},
}

// RustAnalyzer implements Analyzer for Rust projects. Rust compiles to a single static binary
// (like Go), so the rust.dockerfile template does the heavy lifting — `cargo build --release`
// then a distroless runtime — and BuildCommand/StartCommand here are advisory.
type RustAnalyzer struct {
	ProjectFS projectFS
}

// NewRustAnalyzer creates a Rust analyzer instance.
func NewRustAnalyzer(projectFS projectFS) Analyzer {
	return &RustAnalyzer{ProjectFS: projectFS}
}

// CanHandle reports whether this looks like a Rust project: a Cargo.toml at the root. A bare
// .rs file is intentionally NOT enough — plenty of non-Rust repos carry an incidental script —
// so the manifest is the sole signal (mirrors Ruby's manifest-only precision).
func (r *RustAnalyzer) CanHandle() (bool, error) {
	if _, err := fs.Stat(r.ProjectFS, "Cargo.toml"); err == nil {
		return true, nil
	}
	return false, nil
}

// Analyze produces the project spec. The rust.dockerfile template runs `cargo build --release`
// and runs the compiled binary, so BuildCommand/StartCommand are informational.
func (r *RustAnalyzer) Analyze() (*ProjectSpec, error) {
	deps := r.dependencies()
	ignoreDirs := []string{"target", ".git", "vendor"}
	exts := []string{".rs"}

	envVars, err := walkProjectForCandidates(r.ProjectFS, exts, ignoreDirs, regexp.MustCompile(rustEnvVarRegex), 3, 5)
	if err != nil {
		return nil, errors.Errorf("failed to scan Rust env vars: %w", err)
	}

	routes, err := walkProjectForRoutes(r.ProjectFS, exts, ignoreDirs, regexp.MustCompile(rustRouteRegex), NewDefaultRouteProcessor(), 3, 5)
	if err != nil {
		return nil, errors.Errorf("failed to scan Rust routes: %w", err)
	}

	services := r.detectServices(deps)

	// Framework marker mirrors the "framework" convention used by Python/Ruby. Detecting a web
	// framework also tells DetectAgentShape this app serves HTTP, so it won't be mislabeled a
	// worker.
	framework := r.detectFramework(deps)
	if framework != "" {
		services = append(services, ServiceRequirement{Type: "framework", Provider: framework})
	}

	binName := r.binaryName()

	migrationContext := r.collectMigrationContext(deps)

	detectedShape := DetectAgentShape(deps, framework != "")

	return &ProjectSpec{
		Name:                binName,
		Language:            "rust",
		ServiceRequirements: services,
		// Advisory: the Dockerfile hard-codes `cargo build --release` and runs the copied binary,
		// so these are used for the plan display, not the build.
		BuildCommand:     "cargo build --release",
		StartCommand:     "./target/release/" + binName,
		EnvVars:          envVars,
		Routes:           routes,
		MigrationContext: migrationContext,
		DetectedShape:    detectedShape,
	}, nil
}

// dependencies returns the crate names declared under Cargo.toml's dependency tables
// ([dependencies], [dev-dependencies], [build-dependencies], and their `[dependencies.foo]`
// sub-table form), lowercased.
func (r *RustAnalyzer) dependencies() []string {
	data, err := fs.ReadFile(r.ProjectFS, "Cargo.toml")
	if err != nil {
		return nil
	}

	var deps []string
	seen := map[string]bool{}
	add := func(name string) {
		name = strings.ToLower(strings.TrimSpace(name))
		if name != "" && !seen[name] {
			seen[name] = true
			deps = append(deps, name)
		}
	}

	inDeps := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			// A new section header. Figure out whether it's a dependency table.
			section := strings.Trim(trimmed, "[]")
			switch {
			case section == "dependencies" || section == "dev-dependencies" || section == "build-dependencies":
				inDeps = true
			case strings.HasPrefix(section, "dependencies.") ||
				strings.HasPrefix(section, "dev-dependencies.") ||
				strings.HasPrefix(section, "build-dependencies."):
				// `[dependencies.axum]` table form — the crate name is the header suffix; the
				// lines that follow are version/features, not further crate names.
				inDeps = false
				add(section[strings.Index(section, ".")+1:])
			default:
				inDeps = false
			}
			continue
		}
		if inDeps {
			if m := depNameLineRegex.FindStringSubmatch(trimmed); m != nil {
				add(m[1])
			}
		}
	}
	return deps
}

// binaryName is the compiled binary's name: the first `[[bin]]` name, else the `[package]`
// name, else the project directory basename. Cargo names the binary after this, so it's what
// the Dockerfile copies out of target/release.
func (r *RustAnalyzer) binaryName() string {
	data, err := fs.ReadFile(r.ProjectFS, "Cargo.toml")
	if err != nil {
		return filepath.Base(r.ProjectFS.rootPath)
	}
	content := string(data)
	if name := nameInSection(content, "[[bin]]"); name != "" {
		return name
	}
	if name := nameInSection(content, "[package]"); name != "" {
		return name
	}
	return filepath.Base(r.ProjectFS.rootPath)
}

// nameInSection returns the value of a `name = "…"` key that appears after the given section
// header and before the next section header.
func nameInSection(content, header string) string {
	nameRe := regexp.MustCompile(`^\s*name\s*=\s*["']([^"']+)["']`)
	inSection := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inSection = trimmed == header
			continue
		}
		if inSection {
			if m := nameRe.FindStringSubmatch(line); m != nil {
				return m[1]
			}
		}
	}
	return ""
}

// detectServices maps known DB/cache crates to backing-service requirements.
func (r *RustAnalyzer) detectServices(deps []string) []ServiceRequirement {
	var services []ServiceRequirement
	seen := map[ServiceRequirement]bool{}
	for _, m := range rustServiceMarkers {
		if hasCrate(deps, m.crate) && !seen[m.service] {
			seen[m.service] = true
			services = append(services, m.service)
		}
	}
	return services
}

// detectFramework returns the provider label of the first recognized web framework crate.
func (r *RustAnalyzer) detectFramework(deps []string) string {
	for _, f := range rustFrameworks {
		if hasCrate(deps, f.crate) {
			return f.provider
		}
	}
	return ""
}

// collectMigrationContext gathers SQLx/SeaORM/Diesel migration signals so the planner can
// decide whether to run migrations. These tools keep migrations under a `migrations/` dir,
// which FindMigrationFiles already recognizes, so FilterConfiguredMigrationTools's default
// branch includes them when that directory exists.
func (r *RustAnalyzer) collectMigrationContext(deps []string) MigrationContext {
	migrationContext := MigrationContext{
		MigrationFiles: []string{},
		ORMTools:       []string{},
		ConfigFiles:    make(map[string]string),
		PackageScripts: make(map[string]string),
	}

	detectedTools := DetectORMTools(deps, "rust")
	migrationFiles, _ := FindMigrationFiles(r.ProjectFS.rootPath)
	migrationContext.MigrationFiles = migrationFiles
	migrationContext.ORMTools = FilterConfiguredMigrationTools(detectedTools, migrationFiles, r.ProjectFS.rootPath)

	return migrationContext
}

// hasCrate reports whether name is present in the parsed dependency list.
func hasCrate(deps []string, name string) bool {
	for _, d := range deps {
		if d == name {
			return true
		}
	}
	return false
}
