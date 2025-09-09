package analyzer

import (
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	// Comprehensive Python environment variable regex with single capture group
	// Matches common Python environment variable patterns including multi-line
	pyEnvVarRegex = `(?s)(?:` +
		// Standard os.environ patterns
		`os\.environ\[\s*["']([A-Za-z_][A-Za-z0-9_]*)["']\]|` +
		`os\.environ\.get\(\s*["']([A-Za-z_][A-Za-z0-9_]*)["']|` +
		`os\.environ\.setdefault\(\s*["']([A-Za-z_][A-Za-z0-9_]*)["']|` +
		`os\.getenv\(\s*["']([A-Za-z_][A-Za-z0-9_]*)["']|` +

		// Config patterns (decouple, django-environ, etc) - handles multi-line
		`config\(\s*["']([A-Za-z_][A-Za-z0-9_]*)["']|` +

		// Environs patterns
		`env\.[a-zA-Z_]+\(\s*["']([A-Za-z_][A-Za-z0-9_]*)["']|` +
		`env\(\s*["']([A-Za-z_][A-Za-z0-9_]*)["']|` +

		// Pydantic Field patterns
		`Field\([^)]*?\benv\s*=\s*["']([A-Za-z_][A-Za-z0-9_]*)["']|` +

		// Settings and getattr patterns
		`getattr\(\s*settings\s*,\s*["']([A-Za-z_][A-Za-z0-9_]*)["']|` +
		`settings\.([A-Za-z_][A-Za-z0-9_]*)\b` +
		`)`

	pyRouteRegex = `(?i)` +
		// Flask patterns
		`@app\.route\(\s*["']([^"']*)["']\s*,\s*methods\s*=\s*\[["']([^"']+)["']\]|` +
		`@app\.route\(\s*["']([^"']*)["']|` +
		// Django URL patterns - capture path (allow empty)
		`path\(\s*["']([^"']*)["']|` +
		`url\(\s*r?["']([^"']*)["']|` +
		// FastAPI patterns
		`@app\.(get|post|put|delete|patch|head|options)\(\s*["']([^"']*)["']|` +
		// FastAPI with APIRouter
		`@router\.(get|post|put|delete|patch|head|options)\(\s*["']([^"']*)["']|` +
		// FastAPI with custom app variable names
		`@\w+\.(get|post|put|delete|patch|head|options)\(\s*["']([^"']*)["']|` +
		// Starlette Route patterns
		`Route\(\s*["']([^"']*)["']\s*,\s*[^,]+\s*,\s*methods\s*=\s*\[["']([^"']+)["']\]|` +
		// Tornado patterns
		`\(\s*r?["']([^"']*)["']\s*,\s*\w+Handler\s*\)`

	// Router mounting patterns for FastAPI, Flask Blueprints, etc.
	pyRouterMountRegex = `(?i)` +
		// FastAPI include_router patterns
		`app\.include_router\(\s*([^,\s]+)(?:\s*,\s*prefix\s*=\s*["']([^"']*)["'])?|` +
		// Flask Blueprint registration
		`app\.register_blueprint\(\s*([^,\s]+)(?:\s*,\s*url_prefix\s*=\s*["']([^"']*)["'])?|` +
		// Django include patterns
		`path\(\s*["']([^"']*)["']\s*,\s*include\(\s*["']([^"']*)["']\)|` +
		// Starlette mount patterns
		`app\.mount\(\s*["']([^"']*)["']\s*,\s*([^)]+)\)`
)

// PythonAnalyzer implements the Analyzer interface for Python projects
type PythonAnalyzer struct {
	ProjectFS projectFS
	Cache     *AnalysisCache
}

type procfileCommands struct {
	web     string
	release string
	others  map[string]string
}

// NewPythonAnalyzer creates a new Python analyzer instance
func NewPythonAnalyzer(projectFS projectFS) Analyzer {
	return &PythonAnalyzer{
		ProjectFS: projectFS,
		Cache: &AnalysisCache{
			cache: make(map[string]any),
		},
	}
}

// CanHandle determines if this analyzer can handle the project
func (p *PythonAnalyzer) CanHandle() (bool, error) {
	// Check for Python-specific files
	pythonFiles := []string{
		"requirements.txt",
		"Pipfile",
		"pyproject.toml",
		"setup.py",
		".python-version",
		"runtime.txt",
	}

	for _, file := range pythonFiles {
		if _, err := fs.Stat(p.ProjectFS, file); err == nil {
			return true, nil
		}
	}

	// Check for Python files in the project
	entries, err := fs.ReadDir(p.ProjectFS, ".")
	if err != nil {
		return false, err
	}

	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".py") {
			return true, nil
		}
	}

	return false, nil
}

// Analyze performs the Python project analysis
func (p *PythonAnalyzer) Analyze() (*ProjectSpec, error) {
	runtime, err := p.detectRuntime()
	if err != nil {
		return nil, fmt.Errorf("failed to detect runtime: %w", err)
	}

	dependencies, err := p.extractDependencies()
	if err != nil {
		return nil, fmt.Errorf("failed to extract dependencies: %w", err)
	}

	buildCmd, err := p.extractBuildCommand()
	if err != nil {
		log.Printf("Could not determine build command: %v", err)
	}

	_, err = p.detectFramework(dependencies)
	if err != nil {
		return nil, fmt.Errorf("failed to detect framework: %w", err)
	}

	serviceRequirements, err := p.extractServiceRequirements(dependencies)
	if err != nil {
		return nil, fmt.Errorf("failed to extract service requirements: %w", err)
	}

	runCmd, err := p.extractRunCommand()
	if err != nil {
		// if we can't detect the run command we'll log and eventually try to infer it downstream
		log.Printf("Could not find a start command: %v", err)
	}
	var launchCtx LaunchContext
	if runCmd == "" {
		// if we can't straight up get a run command, let's build up a launch context that can be further analyzed
		launchFiles, err := findLauncherFiles(p.ProjectFS.rootPath)
		if err != nil {
			// we couldn't statically find a run command or build a context, so we should err
			return nil, err
		}
		launchers := make([]LauncherFile, len(launchFiles))
		for i, f := range launchFiles {
			snippet, err := readSnippet(f, 50)
			if err != nil {
				// same as above, without any context we can't determine a run command
				return nil, err
			}
			path, _ := filepath.Rel(p.ProjectFS.rootPath, f)
			launchers[i] = LauncherFile{
				Name:    path,
				Content: snippet,
			}
		}
		// add the README for extra context
		data, err := getReadmeContents(p.ProjectFS)
		if err != nil {
			// just log, readme was a nice to have for additional context but not necessary
			log.Printf("Could not read readme file: %v", err)
		}
		launchCtx = LaunchContext{
			Launchers: launchers,
			Readme:    data,
		}
	}
	// predeploy, err := p.extractPreDeploy()

	projectName := p.extractProjectName(runtime, dependencies)

	re := regexp.MustCompile(pyEnvVarRegex)
	ignoreDirs := []string{"venv", ".venv", "env", ".env", "__pycache__", ".git", ".pytest_cache", ".mypy_cache"}
	envVars, err := walkProjectForCandidates(p.ProjectFS, []string{".py"}, ignoreDirs, re, 3, 5)
	if err != nil {
		return nil, err
	}

	// Filter out false positives
	envVars = p.filterEnvVarFalsePositives(envVars)

	// First pass: Extract router mounting information
	processor := NewPythonRouteProcessor()
	err = p.extractRouterMounts(processor)
	if err != nil {
		log.Printf("Warning: Could not extract router mounts: %v", err)
	}

	// Second pass: Extract routes with prefix information
	routeRe := regexp.MustCompile(pyRouteRegex)
	routes, err := walkProjectForRoutes(p.ProjectFS, []string{".py"}, ignoreDirs, routeRe, processor, 3, 5)
	if err != nil {
		return nil, err
	}

	return &ProjectSpec{
		Name:                projectName,
		Language:            "python",
		ServiceRequirements: serviceRequirements,
		BuildCommand:        buildCmd,
		EnvVars:             envVars,
		Routes:              routes,
		StartCommand:        runCmd,
		LaunchContext:       launchCtx,
	}, nil
}

func (p *PythonAnalyzer) extractBuildCommand() (string, error) {
	// Use centralized dependency management detection
	depMgmt := p.detectDependencyManagement()

	switch depMgmt {
	case DepMgmtPoetry:
		return "poetry install --only=main", nil
	case DepMgmtHatch:
		return "pip install -e .", nil
	case DepMgmtPipenv:
		return "pipenv install --deploy", nil
	case DepMgmtPipTools, DepMgmtRequirementsTxt:
		return "pip install -r requirements.txt", nil
	case DepMgmtPEP621, DepMgmtSetupPy:
		return "pip install .", nil
	default:
		return "", fmt.Errorf("no recognized dependency management approach found")
	}
}

func (p *PythonAnalyzer) filterEnvVarFalsePositives(candidates []EnvVarCandidate) []EnvVarCandidate {
	// Common false positive patterns - these are typically not environment variables
	falsePositives := map[string]bool{
		// Framework/library-specific attributes
		"fastapi_kwargs":  true,
		"django_settings": true,
		"flask_config":    true,
		"app_config":      true,

		// Common Python object attributes
		"class_name":    true,
		"method_name":   true,
		"function_name": true,

		// Common configuration attributes that are usually not env vars
		"debug":    true,
		"testing":  true,
		"config":   true,
		"settings": true,

		// Common application-specific config attributes
		"all_cors_origins":         true,
		"emails_enabled":           true,
		"cors_origins":             true,
		"email_backend":            true,
		"static_url":               true,
		"media_url":                true,
		"allowed_hosts":            true,
		"csrf_cookie":              true,
		"session_cookie":           true,
		"login_url":                true,
		"logout_url":               true,
		"time_zone":                true,
		"use_tz":                   true,
		"language_code":            true,
		"installed_apps":           true,
		"middleware":               true,
		"root_urlconf":             true,
		"wsgi_application":         true,
		"templates":                true,
		"databases":                true,
		"auth_password_validators": true,
	}

	// Additional heuristics for filtering
	isLikelyEnvVar := func(name string) bool {
		// Skip obvious false positives
		if falsePositives[strings.ToLower(name)] {
			return false
		}

		// Environment variables are typically UPPER_CASE or contain underscores
		// and don't contain common Python keywords
		if strings.Contains(strings.ToLower(name), "kwargs") ||
			strings.Contains(strings.ToLower(name), "config") && len(name) < 10 ||
			strings.Contains(strings.ToLower(name), "settings") && len(name) < 12 {
			return false
		}

		// If it's all lowercase with no underscores, probably not an env var
		if name == strings.ToLower(name) && !strings.Contains(name, "_") {
			return false
		}

		return true
	}

	filtered := make([]EnvVarCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if isLikelyEnvVar(candidate.VarName) {
			filtered = append(filtered, candidate)
		}
	}

	return filtered
}

func (p *PythonAnalyzer) extractRunCommand() (string, error) {
	// TODO: should we extract this to a separate workflow that infers the run command? This is probably language/framework agnostic
	// if we have a Procfile, we can try to see if there is a web command we can use for the start command
	// For now, static analysis will happen here and then downstream we can do further LLM based analysis
	if data, err := fs.ReadFile(p.ProjectFS, "Procfile"); err == nil {
		content := string(data)
		cmds := parseProcfile(content)
		return cmds.web, nil
	}

	return "", nil
}

func (p *PythonAnalyzer) extractPreDeploy() (string, error) {
	return "", nil
}

func parseProcfile(contents string) procfileCommands {
	lines := strings.Split(contents, "\n")
	result := procfileCommands{others: make(map[string]string)}

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		procType := strings.TrimSpace(parts[0])
		cmd := strings.TrimSpace(parts[1])

		switch procType {
		case "web":
			result.web = cmd
		case "release":
			result.release = cmd
		default:
			result.others[procType] = cmd
		}
	}
	return result
}

// extractRouterMounts scans for router mounting patterns and populates the processor's RouterMounts
func (p *PythonAnalyzer) extractRouterMounts(processor *PythonRouteProcessor) error {
	mountRe := regexp.MustCompile(pyRouterMountRegex)
	ignoreDirs := []string{"venv", ".venv", "env", ".env", "__pycache__", ".git", ".pytest_cache", ".mypy_cache"}

	return filepath.WalkDir(p.ProjectFS.rootPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			for _, ignore := range ignoreDirs {
				if strings.Contains(path, ignore) {
					return filepath.SkipDir
				}
			}
			return nil
		}

		if !strings.HasSuffix(path, ".py") {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		fullContent := string(content)

		matches := mountRe.FindAllStringSubmatch(fullContent, -1)
		matchIndices := mountRe.FindAllStringSubmatchIndex(fullContent, -1)

		for i, match := range matches {
			if len(match) < 2 {
				continue
			}

			// Calculate line number
			matchStart := matchIndices[i][0]
			lineNum := strings.Count(fullContent[:matchStart], "\n") + 1

			// Extract router name and prefix from match
			var routerName, prefix string

			if strings.Contains(match[0], "include_router") {
				// FastAPI: app.include_router(notes.router, prefix="/notes")
				routerName = match[1] // e.g., "notes.router"
				if len(match) > 2 && match[2] != "" {
					prefix = match[2] // e.g., "/notes"
				}
			} else if strings.Contains(match[0], "register_blueprint") {
				// Flask: app.register_blueprint(notes_bp, url_prefix="/notes")
				routerName = match[1]
				if len(match) > 2 && match[2] != "" {
					prefix = match[2]
				}
			} else if strings.Contains(match[0], "path(") && strings.Contains(match[0], "include") {
				// Django: path('api/', include('notes.urls'))
				prefix = match[1]
				if len(match) > 2 {
					routerName = match[2]
				}
			} else if strings.Contains(match[0], "mount") {
				// Starlette: app.mount("/api", sub_app)
				prefix = match[1]
				routerName = match[2]
			}

			if routerName != "" {
				// Extract module name from router reference (e.g., "notes.router" -> "notes")
				moduleName := routerName
				if dotIndex := strings.Index(routerName, "."); dotIndex != -1 {
					moduleName = routerName[:dotIndex]
				}

				processor.RouterMounts[moduleName] = RouterMount{
					RouterName: routerName,
					Prefix:     prefix,
					File:       path,
					Line:       lineNum,
				}
			}
		}

		return nil
	})
}

// RouterMount represents a router mount with prefix information
type RouterMount struct {
	RouterName string // The router variable name (e.g., "notes.router", "ping.router")
	Prefix     string // The prefix path (e.g., "/notes", "/api/v1")
	File       string // File where the mount is defined
	Line       int    // Line number
}

// PythonRouteProcessor handles Python-specific route processing including Django special cases
type PythonRouteProcessor struct {
	RouterMounts map[string]RouterMount // Map router module/file to mount info
}

func NewPythonRouteProcessor() *PythonRouteProcessor {
	return &PythonRouteProcessor{
		RouterMounts: make(map[string]RouterMount),
	}
}

func (p *PythonRouteProcessor) ProcessMatch(match RouteMatch, filePath string) []RouteCandidate {
	// Handle Django empty path case: path('') -> GET /
	if p.isDjangoEmptyPath(match) {
		basePath := "/"
		// Apply prefix if this file has a router mount
		if prefix := p.getRouterPrefix(filePath); prefix != "" {
			basePath = p.combinePaths(prefix, basePath)
		}

		return []RouteCandidate{{
			Method:  "GET",
			Path:    basePath, // Convert Django empty path to standard root path with prefix
			File:    filePath,
			Line:    match.Line,
			Context: match.Context,
		}}
	}

	// Extract method and path from capture groups
	capturedValues := make([]string, 0)
	for _, group := range match.CaptureGroups {
		if group != "" {
			capturedValues = append(capturedValues, group)
		}
	}

	if len(capturedValues) == 0 {
		return nil
	}

	method := ""
	routePath := ""

	// Check if this is a Django path() pattern
	isDjango := strings.Contains(match.FullMatch, "path(")

	// Try to determine which is method and which is path
	for _, val := range capturedValues {
		upperVal := strings.ToUpper(val)

		// Check if this looks like an HTTP method
		if p.isHTTPMethod(upperVal) {
			method = upperVal
		} else if p.isValidPythonRoutePath(val, isDjango) {
			routePath = val
		} else if routePath == "" && method != "" && p.isValidPythonRoutePath(val, isDjango) {
			routePath = val
		}
	}

	// Default to GET method if none specified
	if method == "" {
		method = "GET"
	}

	// If we haven't found a route path yet, take the first/only captured value
	if routePath == "" && len(capturedValues) >= 1 {
		potentialPath := capturedValues[0]
		if p.isValidPythonRoutePath(potentialPath, isDjango) {
			routePath = potentialPath
		}
	}

	// Validate the final route
	if routePath == "" || !p.isValidPythonRoutePath(routePath, isDjango) {
		return nil
	}

	// Apply router prefix if this file has a mounted router
	finalPath := routePath
	if prefix := p.getRouterPrefix(filePath); prefix != "" {
		finalPath = p.combinePaths(prefix, routePath)
	}

	return []RouteCandidate{{
		Method:  method,
		Path:    finalPath,
		File:    filePath,
		Line:    match.Line,
		Context: match.Context,
	}}
}

// isDjangoEmptyPath detects Django's path(”, ...) pattern
func (p *PythonRouteProcessor) isDjangoEmptyPath(match RouteMatch) bool {
	// Check if this is a Django path with empty string
	if !strings.Contains(match.FullMatch, "path('") {
		return false
	}

	// Check if all capture groups are empty (indicating path('') pattern)
	allEmpty := true
	for _, group := range match.CaptureGroups {
		if group != "" {
			allEmpty = false
			break
		}
	}

	return allEmpty && len(match.CaptureGroups) > 0
}

// isHTTPMethod checks if a string is a valid HTTP method
func (p *PythonRouteProcessor) isHTTPMethod(method string) bool {
	switch method {
	case "GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS", "ALL":
		return true
	default:
		return false
	}
}

// isValidPythonRoutePath checks if a path is valid for Python web frameworks
func (p *PythonRouteProcessor) isValidPythonRoutePath(path string, isDjango bool) bool {
	// Allow empty path (Django's path('', ...) represents root)
	if path == "" {
		return true
	}

	// For Django, allow relative paths; for Flask/FastAPI, require absolute paths
	if isDjango {
		// Django can have relative paths
		if !strings.HasPrefix(path, "/") && !p.isDjangoStylePath(path, isDjango) {
			return false
		}
	} else {
		// Flask/FastAPI typically use absolute paths starting with /
		if !strings.HasPrefix(path, "/") {
			return false
		}
	}

	// Must not contain spaces (route paths shouldn't have spaces)
	if strings.Contains(path, " ") {
		return false
	}

	// Should not be extremely long (likely not a route)
	if len(path) > 100 {
		return false
	}

	return true
}

// isDjangoStylePath checks if a path looks like a Django URL pattern
func (p *PythonRouteProcessor) isDjangoStylePath(path string, isDjango bool) bool {
	if !isDjango {
		return false
	}

	// Django paths can be relative (no leading /) and include patterns like:
	// "users/", "admin/", "api/v1/", etc.
	if path == "" {
		return true // Empty path is valid in Django
	}

	// Allow simple patterns without leading slash
	if strings.Contains(path, "/") || path == "admin" || strings.HasSuffix(path, "/") {
		return true
	}

	// Single word paths are valid in Django (like "admin", "api", etc.)
	if len(path) > 0 && len(path) < 20 && !strings.Contains(path, " ") {
		return true
	}

	return false
}

// getRouterPrefix determines the prefix for a router file based on mounted routers
func (p *PythonRouteProcessor) getRouterPrefix(filePath string) string {
	// Extract module name from file path (e.g., "/path/to/notes.py" -> "notes")
	fileName := filepath.Base(filePath)
	moduleName := strings.TrimSuffix(fileName, ".py")

	// Look for router mount by module name
	if mount, exists := p.RouterMounts[moduleName]; exists {
		return mount.Prefix
	}

	return ""
}

// combinePaths safely combines a prefix and a route path
func (p *PythonRouteProcessor) combinePaths(prefix, path string) string {
	// Handle empty cases
	if prefix == "" {
		return path
	}
	if path == "" || path == "/" {
		return prefix
	}

	// Ensure prefix starts with / and doesn't end with /
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	if strings.HasSuffix(prefix, "/") {
		prefix = strings.TrimSuffix(prefix, "/")
	}

	// Ensure path starts with /
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	return prefix + path
}
