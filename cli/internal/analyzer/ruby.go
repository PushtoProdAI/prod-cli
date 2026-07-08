package analyzer

import (
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-errors/errors"
)

// Ruby env access: ENV["X"], ENV['X'], ENV.fetch("X", ...). The var name lands in one of the
// capture groups; scanFileForCandidates picks the first non-empty one.
const rubyEnvVarRegex = `ENV\.fetch\(\s*["']([A-Za-z_][A-Za-z0-9_]*)["']|` +
	`ENV\[\s*["']([A-Za-z_][A-Za-z0-9_]*)["']\]`

// HTTP routes across Rails' router DSL (config/routes.rb) and Sinatra's classic DSL. The
// verb/path or the resource symbol lands in a capture group; RubyRouteProcessor sorts it out.
const rubyRouteRegex = `(?i)` +
	// `root "home#index"` / `root to: "home#index"` → GET /
	`\b(root)\s+(?:to:\s*)?["']([^"']+)["']|` +
	// Rails REST helpers: `resources :users` / `resource :profile` → a GET /<name> route
	`\b(resources?)\s+:([A-Za-z_][A-Za-z0-9_]*)|` +
	// verb routes shared by Rails (`get "posts"`) and Sinatra (`get "/health" do`)
	`\b(get|post|put|patch|delete)\s+["']([^"']+)["']`

// rubyServiceMarkers maps a gem to the backing service it implies. SQLite is intentionally
// omitted — it's file-local, not a provisioned service (mirrors goServiceMarkers).
var rubyServiceMarkers = []struct {
	gem     string
	service ServiceRequirement
}{
	{"pg", ServicePostgres},
	{"postgresql", ServicePostgres},
	{"mysql2", ServiceMySQL},
	{"redis", ServiceRedis},
	{"mongoid", ServiceMongo},
}

// RubyAnalyzer implements Analyzer for Ruby projects. Rails and Sinatra are the two shapes it
// recognizes; the ruby.dockerfile template does the build (bundle install, and — for Rails —
// asset precompilation), so BuildCommand/StartCommand are advisory.
type RubyAnalyzer struct {
	ProjectFS projectFS
}

// NewRubyAnalyzer creates a Ruby analyzer instance.
func NewRubyAnalyzer(projectFS projectFS) Analyzer {
	return &RubyAnalyzer{ProjectFS: projectFS}
}

// CanHandle reports whether this looks like a Ruby project: a Gemfile at the root, a *.gemspec,
// or failing those any .rb file at the root.
func (r *RubyAnalyzer) CanHandle() (bool, error) {
	if _, err := fs.Stat(r.ProjectFS, "Gemfile"); err == nil {
		return true, nil
	}
	entries, err := fs.ReadDir(r.ProjectFS, ".")
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".gemspec") || strings.HasSuffix(name, ".rb") {
			return true, nil
		}
	}
	return false, nil
}

// Analyze produces the project spec, detecting Rails vs Sinatra to pick the framework marker,
// build/start commands, and (for Rails) the migration command.
func (r *RubyAnalyzer) Analyze() (*ProjectSpec, error) {
	gems := r.gems()
	isRails := r.isRails()
	isSinatra := hasGem(gems, "sinatra")

	ignoreDirs := []string{"vendor", ".git", "tmp", "log", "node_modules", ".bundle", "coverage"}
	exts := []string{".rb", ".ru"}

	envVars, err := walkProjectForCandidates(r.ProjectFS, exts, ignoreDirs, regexp.MustCompile(rubyEnvVarRegex), 3, 5)
	if err != nil {
		return nil, errors.Errorf("failed to scan Ruby env vars: %w", err)
	}

	routes, err := walkProjectForRoutes(r.ProjectFS, exts, ignoreDirs, regexp.MustCompile(rubyRouteRegex), NewRubyRouteProcessor(), 3, 5)
	if err != nil {
		return nil, errors.Errorf("failed to scan Ruby routes: %w", err)
	}

	services := r.detectServices(gems)

	// Framework marker drives Rails-specific behavior downstream (asset precompile in the
	// Dockerfile, migration command). Provider mirrors the "framework" convention Python uses.
	buildCmd := "bundle install"
	var startCmd, migrationCmd string
	switch {
	case isRails:
		services = append(services, ServiceRequirement{Type: "framework", Provider: "rails"})
		// Rails reads PORT from the environment, so binding to 0.0.0.0 is enough here.
		startCmd = "bundle exec rails server -b 0.0.0.0"
		migrationCmd = "bin/rails db:migrate"
	case isSinatra:
		services = append(services, ServiceRequirement{Type: "framework", Provider: "sinatra"})
		startCmd = r.sinatraStartCommand()
	}

	migrationContext := r.collectMigrationContext(gems, services)

	// Ruby web apps almost always carry a framework/web-server gem, so the shape detector won't
	// mislabel them a worker; this still lets an MCP/agent-shaped Ruby app be recognized.
	detectedShape := DetectAgentShape(gems, isRails || isSinatra)

	return &ProjectSpec{
		Name:                r.projectName(),
		Language:            "ruby",
		ServiceRequirements: services,
		BuildCommand:        buildCmd,
		StartCommand:        startCmd,
		MigrationCommand:    migrationCmd,
		EnvVars:             envVars,
		Routes:              routes,
		MigrationContext:    migrationContext,
		DetectedShape:       detectedShape,
	}, nil
}

// isRails reports whether the project is a Rails app: a bin/rails binstub or a
// config/application.rb is the canonical signal.
func (r *RubyAnalyzer) isRails() bool {
	if _, err := fs.Stat(r.ProjectFS, "bin/rails"); err == nil {
		return true
	}
	if _, err := fs.Stat(r.ProjectFS, "config/application.rb"); err == nil {
		return true
	}
	return false
}

// sinatraStartCommand prefers `rackup` when a config.ru is present (it binds host/port cleanly),
// otherwise runs the first plausible entrypoint .rb directly.
func (r *RubyAnalyzer) sinatraStartCommand() string {
	if _, err := fs.Stat(r.ProjectFS, "config.ru"); err == nil {
		return "bundle exec rackup -o 0.0.0.0 -p ${PORT:-3000}"
	}
	for _, name := range []string{"app.rb", "main.rb", "server.rb", "web.rb"} {
		if _, err := fs.Stat(r.ProjectFS, name); err == nil {
			return "bundle exec ruby " + name
		}
	}
	return "bundle exec ruby app.rb"
}

// projectName is the Rails application module name (config/application.rb's `module Foo`) when
// available, else the project directory's basename.
func (r *RubyAnalyzer) projectName() string {
	if data, err := fs.ReadFile(r.ProjectFS, "config/application.rb"); err == nil {
		re := regexp.MustCompile(`(?m)^\s*module\s+([A-Za-z_][A-Za-z0-9_]*)`)
		if m := re.FindStringSubmatch(string(data)); m != nil {
			return m[1]
		}
	}
	return filepath.Base(r.ProjectFS.rootPath)
}

// gems returns the gem names declared in the Gemfile (`gem 'name'`), lowercased.
func (r *RubyAnalyzer) gems() []string {
	data, err := fs.ReadFile(r.ProjectFS, "Gemfile")
	if err != nil {
		return nil
	}
	re := regexp.MustCompile(`(?m)^\s*gem\s+["']([A-Za-z0-9_.-]+)["']`)
	var gems []string
	for _, m := range re.FindAllStringSubmatch(string(data), -1) {
		gems = append(gems, strings.ToLower(m[1]))
	}
	return gems
}

// detectServices maps known DB/cache gems to backing-service requirements.
func (r *RubyAnalyzer) detectServices(gems []string) []ServiceRequirement {
	var services []ServiceRequirement
	seen := map[ServiceRequirement]bool{}
	for _, m := range rubyServiceMarkers {
		if hasGem(gems, m.gem) && !seen[m.service] {
			seen[m.service] = true
			services = append(services, m.service)
		}
	}
	return services
}

// collectMigrationContext gathers ActiveRecord/other Ruby migration signals so the planner can
// decide whether to run migrations.
func (r *RubyAnalyzer) collectMigrationContext(gems []string, serviceRequirements []ServiceRequirement) MigrationContext {
	migrationContext := MigrationContext{
		MigrationFiles: []string{},
		ORMTools:       []string{},
		ConfigFiles:    make(map[string]string),
		PackageScripts: make(map[string]string),
	}

	detectedTools := DetectORMTools(gems, "ruby")
	migrationFiles, _ := FindMigrationFiles(r.ProjectFS.rootPath)
	migrationContext.MigrationFiles = migrationFiles
	migrationContext.ORMTools = FilterConfiguredMigrationTools(detectedTools, migrationFiles, r.ProjectFS.rootPath)

	return migrationContext
}

// hasGem reports whether name is present in the parsed gem list.
func hasGem(gems []string, name string) bool {
	for _, g := range gems {
		if g == name {
			return true
		}
	}
	return false
}

// RubyRouteProcessor turns Rails/Sinatra router matches into RouteCandidates: normalizing
// relative Rails paths (`get "posts"` → /posts), expanding `resources :x` to /x, and mapping
// `root` to /.
type RubyRouteProcessor struct{}

// NewRubyRouteProcessor creates a Ruby route processor.
func NewRubyRouteProcessor() *RubyRouteProcessor { return &RubyRouteProcessor{} }

func (p *RubyRouteProcessor) ProcessMatch(match RouteMatch, filePath string) []RouteCandidate {
	groups := make([]string, 0, len(match.CaptureGroups))
	for _, g := range match.CaptureGroups {
		if g != "" {
			groups = append(groups, g)
		}
	}
	if len(groups) == 0 {
		return nil
	}

	lead := strings.ToLower(groups[0])
	method := "GET"
	var routePath string

	switch lead {
	case "root":
		routePath = "/"
	case "resource", "resources":
		if len(groups) < 2 {
			return nil
		}
		routePath = "/" + groups[len(groups)-1]
	default:
		// verb route: groups are [verb, path]
		if len(groups) >= 2 {
			method = strings.ToUpper(groups[0])
			routePath = groups[1]
		} else {
			routePath = groups[0]
		}
	}

	// Rails paths are often relative; normalize to an absolute path.
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
