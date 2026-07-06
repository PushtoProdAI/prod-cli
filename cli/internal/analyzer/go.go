package analyzer

import (
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-errors/errors"
)

// Go env access patterns: os.Getenv("X") / os.LookupEnv("X").
const goEnvVarRegex = `os\.(?:Getenv|LookupEnv)\(\s*"([A-Za-z_][A-Za-z0-9_]*)"`

// HTTP routes across the stdlib and the popular Go frameworks (gin, echo, chi,
// fiber, gorilla/mux). The method verb or a HandleFunc path lands in a capture group;
// DefaultRouteProcessor sorts out which group is the path.
const goRouteRegex = `(?:` +
	`\.HandleFunc\(\s*"([^"]*)"|` +
	`\.Handle\(\s*"([^"]*)"|` +
	`\.(GET|POST|PUT|DELETE|PATCH|HEAD|OPTIONS|Get|Post|Put|Delete|Patch|Head|Options)\(\s*"([^"]*)"` +
	`)`

// goServiceMarkers maps a backing-service driver (matched as a substring of go.mod)
// to the service it implies. SQLite is intentionally omitted — it's file-local, not a
// provisioned service.
var goServiceMarkers = []struct {
	marker  string
	service ServiceRequirement
}{
	{"lib/pq", ServicePostgres},
	{"jackc/pgx", ServicePostgres},
	{"gorm.io/driver/postgres", ServicePostgres},
	{"go-sql-driver/mysql", ServiceMySQL},
	{"gorm.io/driver/mysql", ServiceMySQL},
	{"go.mongodb.org/mongo-driver", ServiceMongo},
	{"redis/go-redis", ServiceRedis},
	{"go-redis/redis", ServiceRedis},
	{"gomodule/redigo", ServiceRedis},
}

// GoAnalyzer implements Analyzer for Go projects. Go builds to a single static
// binary, so the heavy lifting is in the Dockerfile template; the analyzer supplies
// the name, backing services, env vars, and routes.
type GoAnalyzer struct {
	ProjectFS projectFS
}

// NewGoAnalyzer creates a Go analyzer instance.
func NewGoAnalyzer(projectFS projectFS) Analyzer {
	return &GoAnalyzer{ProjectFS: projectFS}
}

// CanHandle reports whether this looks like a Go project: a go.mod, or failing that
// any .go file at the root.
func (g *GoAnalyzer) CanHandle() (bool, error) {
	if _, err := fs.Stat(g.ProjectFS, "go.mod"); err == nil {
		return true, nil
	}
	entries, err := fs.ReadDir(g.ProjectFS, ".")
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
			return true, nil
		}
	}
	return false, nil
}

// Analyze produces the project spec. The go.dockerfile template runs `go build` and
// runs the binary, so BuildCommand/StartCommand are informational.
func (g *GoAnalyzer) Analyze() (*ProjectSpec, error) {
	ignoreDirs := []string{"vendor", ".git", "bin", "dist"}

	envVars, err := walkProjectForCandidates(g.ProjectFS, []string{".go"}, ignoreDirs, regexp.MustCompile(goEnvVarRegex), 3, 5)
	if err != nil {
		return nil, errors.Errorf("failed to scan Go env vars: %w", err)
	}

	routes, err := walkProjectForRoutes(g.ProjectFS, []string{".go"}, ignoreDirs, regexp.MustCompile(goRouteRegex), NewDefaultRouteProcessor(), 3, 5)
	if err != nil {
		return nil, errors.Errorf("failed to scan Go routes: %w", err)
	}

	return &ProjectSpec{
		Name:                g.projectName(),
		Language:            "go",
		ServiceRequirements: g.detectServices(),
		BuildCommand:        "go build -o main .",
		StartCommand:        "./main",
		EnvVars:             envVars,
		Routes:              routes,
	}, nil
}

// projectName is the module path's last segment (github.com/me/api → "api"), falling
// back to the project directory name.
func (g *GoAnalyzer) projectName() string {
	if mod := g.modulePath(); mod != "" {
		return filepath.Base(mod)
	}
	return filepath.Base(g.ProjectFS.rootPath)
}

func (g *GoAnalyzer) modulePath() string {
	data, err := fs.ReadFile(g.ProjectFS, "go.mod")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	return ""
}

// detectServices maps known DB/cache drivers in go.mod to backing-service
// requirements.
func (g *GoAnalyzer) detectServices() []ServiceRequirement {
	data, err := fs.ReadFile(g.ProjectFS, "go.mod")
	if err != nil {
		return nil
	}
	content := string(data)
	var services []ServiceRequirement
	seen := map[ServiceRequirement]bool{}
	for _, m := range goServiceMarkers {
		if strings.Contains(content, m.marker) && !seen[m.service] {
			seen[m.service] = true
			services = append(services, m.service)
		}
	}
	return services
}
