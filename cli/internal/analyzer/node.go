package analyzer

import (
	"encoding/json"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-errors/errors"
)

const (
	nodeEnvVarRegex = `\b(?:(?:process\.env|import\.meta\.env)\??\.([A-Za-z_][A-Za-z0-9_]*)|{[^}]*\b([A-Za-z_][A-Za-z0-9_]*)\b[^}]*}\s*=\s*(?:process\.env|import\.meta\.env))`

	nodeRouteRegex = `(?i)(?:` +
		// Express.js app methods - must start with app. or have router.
		`(?:app|router)\.(?:(get|post|put|delete|patch|head|options|all))\s*\(\s*["']([^"'\s,]+)["']|` +
		// Fastify route definitions
		`\.route\(\s*{\s*method:\s*["']([^"']+)["']\s*,\s*url:\s*["']([^"']+)["']|` +
		// Hapi.js server routes
		`server\.route\(\s*{\s*method:\s*["']([^"']+)["']\s*,\s*path:\s*["']([^"']+)["']|` +
		// Next.js API routes (export function patterns) - only match if path starts with /
		`export\s+(?:async\s+)?function\s+(GET|POST|PUT|DELETE|PATCH|HEAD|OPTIONS)\s*\(` +
		`)`
)

type PackageJson struct {
	Name            string            `json:"name"`
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

type NodeAnalyzer struct {
	ProjectFS projectFS
}

var NodeServiceMappings = map[string]ServiceRequirement{
	// Database drivers
	"pg":       ServicePostgres,
	"postgres": ServicePostgres,
	"mysql":    ServiceMySQL,
	"mysql2":   ServiceMySQL,
	"mongodb":  ServiceMongo,
	"mongoose": ServiceMongo,
	"sqlite3":  ServiceSQLite,

	// Cache/Session stores
	"redis":         ServiceRedis,
	"ioredis":       ServiceRedis,
	"connect-redis": ServiceRedis,
	"memcached":     {Type: "cache", Provider: "memcached"},

	// Queue systems
	"bull":       {Type: "queue", Provider: "redis"},
	"bull-queue": {Type: "queue", Provider: "redis"},
	"agenda":     {Type: "queue", Provider: "mongodb"},
	"kue":        {Type: "queue", Provider: "redis"},
	"amqplib":    {Type: "queue", Provider: "rabbitmq"},

	// Search engines
	"@elastic/elasticsearch": {Type: "search", Provider: "elasticsearch"},
	"elasticsearch":          {Type: "search", Provider: "elasticsearch"},

	// Email services
	"nodemailer": {Type: "email", Provider: "smtp"},
	"sendgrid":   {Type: "email", Provider: "sendgrid"},

	// File storage
	"aws-sdk":            {Type: "storage", Provider: "s3"},
	"@aws-sdk/client-s3": {Type: "storage", Provider: "s3"},

	// Monitoring
	"newrelic":     {Type: "monitoring", Provider: "newrelic"},
	"@sentry/node": {Type: "monitoring", Provider: "sentry"},
}

func NewNodeAnalyzer(projectFS projectFS) Analyzer {
	return &NodeAnalyzer{
		ProjectFS: projectFS,
	}
}

func (n *NodeAnalyzer) CanHandle() (bool, error) {
	if _, err := fs.Stat(n.ProjectFS, "package.json"); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		} else {
			return false, err
		}
	}

	return true, nil
}

func (n *NodeAnalyzer) Analyze() (*ProjectSpec, error) {
	pkgJson, err := unmarshalPkgJson(n.ProjectFS)
	if err != nil {
		return nil, err
	}

	if pkgJson == nil {
		return nil, errors.Errorf("package.json could not be parsed")
	}

	serviceRequirements, err := getDepsPkgJson(pkgJson)
	if err != nil {
		return nil, err
	}

	re := regexp.MustCompile(nodeEnvVarRegex)
	envVars, err := walkProjectForCandidates(n.ProjectFS, []string{".js", ".ts", ".tsx", ".jsx"}, []string{"node_modules", ".next", ".netlify"}, re, 3, 5)
	if err != nil {
		return nil, err
	}

	routeRe := regexp.MustCompile(nodeRouteRegex)
	processor := NewDefaultRouteProcessor()
	routes, err := walkProjectForRoutes(n.ProjectFS, []string{".js", ".ts", ".tsx", ".jsx"}, []string{"node_modules"}, routeRe, processor, 3, 5)
	if err != nil {
		return nil, err
	}

	buildOutputCandidate, err := findBuildOutputCandidate(n.ProjectFS.rootPath)
	if err != nil {
		return nil, err
	}

	// Detect runtime framework separately from build tools and add as service requirement
	runtimeFramework := findRuntimeFramework(n.ProjectFS.rootPath)
	if runtimeFramework != "" {
		serviceRequirements = append(serviceRequirements, ServiceRequirement{
			Type:     "framework",
			Provider: runtimeFramework,
		})
	}

	// Determine build commands based on package.json scripts
	// the start/run will be determined through LLM analysis
	buildCommand := ""

	if pkgJson.Scripts != nil {
		if pkgJson.Scripts["build"] != "" {
			buildCommand = "npm run build"
		}
	}

	var launchCtx LaunchContext
	snippet, err := extractScriptsJSON(pkgJson)
	if err != nil {
		return nil, errors.New("could not extract scripts from package.json")
	}
	path, _ := filepath.Rel(n.ProjectFS.rootPath, "package.json")
	lf := LauncherFile{
		Name:    path,
		Content: snippet,
	}

	// add the README for extra context
	data, err := getReadmeContents(n.ProjectFS)
	if err != nil {
		// just log, readme was a nice to have for additional context but not necessary
		slog.Info("Could not read readme file", "error", err)
	}
	launchCtx = LaunchContext{
		Launchers: []LauncherFile{lf},
		Readme:    data,
	}

	name := pkgJson.Name
	if name == "" {
		base := filepath.Base(n.ProjectFS.rootPath)
		if base != "" {
			name = base
		}

	}

	return &ProjectSpec{
		Name:                name,
		Language:            "node",
		ServiceRequirements: serviceRequirements,
		BuildCommand:        buildCommand,
		EnvVars:             envVars,
		Routes:              routes,
		BuildOutput:         buildOutputCandidate,
		LaunchContext:       launchCtx,
	}, nil
}

func getDepsPkgJson(pkgJson *PackageJson) ([]ServiceRequirement, error) {
	var services []ServiceRequirement

	if pkgJson.Dependencies != nil {
		for dep := range pkgJson.Dependencies {
			if service, exists := NodeServiceMappings[dep]; exists {
				services = append(services, service)
			}
		}
	}

	return services, nil
}

func unmarshalPkgJson(projectFS fs.FS) (*PackageJson, error) {
	data, err := fs.ReadFile(projectFS, "package.json")
	if err != nil {
		return nil, err
	}

	var pkg PackageJson

	if err = json.Unmarshal(data, &pkg); err != nil {
		return nil, err
	}

	return &pkg, nil
}

func findBuildOutputCandidate(root string) (BuildOutputCandidate, error) {
	// Check for Next.js first (prioritize next.config.ts over .js)
	nextConfigTS := filepath.Join(root, "next.config.ts")
	nextConfigJS := filepath.Join(root, "next.config.js")

	if exists(nextConfigTS) {
		contents, _ := os.ReadFile(nextConfigTS)
		return BuildOutputCandidate{
			Path:           ".next",
			Source:         "next.config.ts",
			Framework:      "Next.js",
			ConfigContents: string(contents),
		}, nil
	}

	if exists(nextConfigJS) {
		contents, _ := os.ReadFile(nextConfigJS)
		return BuildOutputCandidate{
			Path:           ".next",
			Source:         "next.config.js",
			Framework:      "Next.js",
			ConfigContents: string(contents),
		}, nil
	}

	if exists(filepath.Join(root, "remix.config.js")) {
		contents, _ := os.ReadFile(filepath.Join(root, "remix.config.js"))
		return BuildOutputCandidate{
			Path:           "build",
			Source:         "remix.config.js",
			Framework:      "Remix",
			ConfigContents: string(contents),
		}, nil
	}

	if exists(filepath.Join(root, "vite.config.js")) || exists(filepath.Join(root, "vite.config.ts")) {
		path := "vite.config.js"
		if exists(filepath.Join(root, "vite.config.ts")) {
			path = "vite.config.ts"
		}
		contents, _ := os.ReadFile(filepath.Join(root, path))
		return BuildOutputCandidate{
			Path:           "dist", // Vite default
			Source:         path,
			Framework:      "Vite",
			ConfigContents: string(contents),
		}, nil
	}

	if exists(filepath.Join(root, "angular.json")) {
		contents, _ := os.ReadFile(filepath.Join(root, "angular.json"))
		return BuildOutputCandidate{
			Path:           "dist", // Angular defaults to "dist/<project>"
			Source:         "angular.json",
			Framework:      "Angular",
			ConfigContents: string(contents),
		}, nil
	}

	if exists(filepath.Join(root, "tsconfig.json")) {
		contents, _ := os.ReadFile(filepath.Join(root, "tsconfig.json"))
		if outDir, err := parseTSConfigOutDir(contents); err == nil && outDir != "" {
			return BuildOutputCandidate{
				Path:           outDir,
				Source:         "tsconfig.json",
				Framework:      "TypeScript",
				ConfigContents: string(contents),
			}, nil
		}
	}

	if exists(filepath.Join(root, "astro.config.mjs")) ||
		exists(filepath.Join(root, "astro.config.ts")) ||
		exists(filepath.Join(root, "astro.config.js")) ||
		exists(filepath.Join(root, "astro.config.cjs")) {
		path := firstExisting(root, []string{
			"astro.config.mjs", "astro.config.ts", "astro.config.js", "astro.config.cjs",
		})
		contents, _ := os.ReadFile(filepath.Join(root, path))
		return BuildOutputCandidate{
			Path:           "dist",
			Source:         path,
			Framework:      "Astro",
			ConfigContents: string(contents),
		}, nil
	}

	if exists(filepath.Join(root, "nuxt.config.ts")) ||
		exists(filepath.Join(root, "nuxt.config.js")) ||
		exists(filepath.Join(root, "nuxt.config.mjs")) ||
		exists(filepath.Join(root, "nuxt.config.cjs")) {
		path := firstExisting(root, []string{
			"nuxt.config.ts", "nuxt.config.js", "nuxt.config.mjs", "nuxt.config.cjs",
		})
		contents, _ := os.ReadFile(filepath.Join(root, path))
		return BuildOutputCandidate{
			Path:           ".output",
			Source:         path,
			Framework:      "Nuxt",
			ConfigContents: string(contents),
		}, nil
	}

	// fallback defaults
	defaults := []string{"dist", "build", "lib"}
	for _, d := range defaults {
		if exists(filepath.Join(root, d)) {
			return BuildOutputCandidate{
				Path:           d,
				Source:         "filesystem-default",
				Framework:      "Unknown",
				ConfigContents: "",
			}, nil
		}
	}

	// No build output found - this is fine for projects without build steps
	return BuildOutputCandidate{
		Path:           "",
		Source:         "no-build",
		Framework:      "None",
		ConfigContents: "",
	}, nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func firstExisting(root string, files []string) string {
	for _, f := range files {
		if exists(filepath.Join(root, f)) {
			return f
		}
	}
	return ""
}

func parseTSConfigOutDir(data []byte) (string, error) {
	var cfg struct {
		CompilerOptions struct {
			OutDir string `json:"outDir"`
		} `json:"compilerOptions"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", err
	}
	return cfg.CompilerOptions.OutDir, nil
}

func extractScriptsJSON(pkg *PackageJson) (string, error) {
	// Wrap only the scripts in a new object
	wrapper := map[string]map[string]string{
		"scripts": pkg.Scripts,
	}

	bytes, err := json.MarshalIndent(wrapper, "", "  ")
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

// findRuntimeFramework detects the runtime framework (what runs the app) as opposed to build tools
// Detection order matters: more specific frameworks are checked first (e.g., SvelteKit before generic Vue)
func findRuntimeFramework(root string) string {
	// Check for SvelteKit (has svelte.config.js|ts)
	if exists(filepath.Join(root, "svelte.config.ts")) || exists(filepath.Join(root, "svelte.config.js")) {
		return "SvelteKit"
	}

	// Check for Next.js (has next.config.js|ts)
	if exists(filepath.Join(root, "next.config.ts")) || exists(filepath.Join(root, "next.config.js")) {
		return "Next.js"
	}

	// Check for Nuxt (has nuxt.config.*)
	if exists(filepath.Join(root, "nuxt.config.ts")) ||
		exists(filepath.Join(root, "nuxt.config.js")) ||
		exists(filepath.Join(root, "nuxt.config.mjs")) ||
		exists(filepath.Join(root, "nuxt.config.cjs")) {
		return "Nuxt"
	}

	// Check for Remix (has remix.config.js|ts or vite.config with remix)
	if exists(filepath.Join(root, "remix.config.js")) || exists(filepath.Join(root, "remix.config.ts")) {
		return "Remix"
	}

	// Check for modern Remix with vite.config containing remix plugin
	viteConfigPath := ""
	if exists(filepath.Join(root, "vite.config.ts")) {
		viteConfigPath = filepath.Join(root, "vite.config.ts")
	} else if exists(filepath.Join(root, "vite.config.js")) {
		viteConfigPath = filepath.Join(root, "vite.config.js")
	}
	if viteConfigPath != "" {
		if content, err := os.ReadFile(viteConfigPath); err == nil {
			contentStr := string(content)
			if strings.Contains(contentStr, "@remix-run/dev") || strings.Contains(contentStr, "remix()") {
				return "Remix"
			}
		}
	}

	// Check for Astro (has astro.config.*)
	if exists(filepath.Join(root, "astro.config.mjs")) ||
		exists(filepath.Join(root, "astro.config.ts")) ||
		exists(filepath.Join(root, "astro.config.js")) ||
		exists(filepath.Join(root, "astro.config.cjs")) {
		return "Astro"
	}

	// Check for Angular (has angular.json)
	if exists(filepath.Join(root, "angular.json")) {
		return "Angular"
	}

	// Check for Vue (has vue.config.js, but not if Nuxt is already detected)
	if exists(filepath.Join(root, "vue.config.js")) || exists(filepath.Join(root, "vue.config.ts")) {
		return "Vue"
	}

	// Check for Vue via package.json dependencies (if no other framework config found)
	if hasVueDependency(root) {
		return "Vue"
	}

	// No runtime framework detected
	return ""
}

// hasVueDependency checks if the project has Vue as a dependency in package.json
// This helps detect "pure" Vue projects that don't have vue.config.js
func hasVueDependency(root string) bool {
	packageJsonPath := filepath.Join(root, "package.json")
	if !exists(packageJsonPath) {
		return false
	}

	data, err := os.ReadFile(packageJsonPath)
	if err != nil {
		return false
	}

	var pkg PackageJson
	if err := json.Unmarshal(data, &pkg); err != nil {
		return false
	}

	// Check dependencies
	if pkg.Dependencies != nil {
		if _, hasVue := pkg.Dependencies["vue"]; hasVue {
			return true
		}
	}

	// Check devDependencies
	if pkg.DevDependencies != nil {
		if _, hasVue := pkg.DevDependencies["vue"]; hasVue {
			return true
		}
	}

	return false
}
