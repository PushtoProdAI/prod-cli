package analyzer

import (
	"fmt"
	"io/fs"
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
		`settings\.([A-Za-z_][A-Za-z0-9_]*)\b|` +

		// Environment variable references in assignments
		`["']([A-Za-z_][A-Za-z0-9_]*)["']\s*:\s*os\.environ\.get\(|` +
		`["']([A-Za-z_][A-Za-z0-9_]*)["']\s*:\s*config\(` +
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
		// Starlette Route patterns
		`Route\(\s*["']([^"']*)["']\s*,\s*[^,]+\s*,\s*methods\s*=\s*\[["']([^"']+)["']\]|` +
		// Tornado patterns
		`\(\s*r?["']([^"']*)["']\s*,\s*\w+Handler\s*\)`
)

// PythonAnalyzer implements the Analyzer interface for Python projects
type PythonAnalyzer struct {
	ProjectFS projectFS
	Cache     *AnalysisCache
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

	_, err = p.detectFramework(dependencies)
	if err != nil {
		return nil, fmt.Errorf("failed to detect framework: %w", err)
	}

	serviceRequirements, err := p.extractServiceRequirements(dependencies)
	if err != nil {
		return nil, fmt.Errorf("failed to extract service requirements: %w", err)
	}

	projectName := p.extractProjectName(runtime, dependencies)

	re := regexp.MustCompile(pyEnvVarRegex)
	ignoreDirs := []string{"venv", ".venv", "env", ".env", "__pycache__", ".git", ".pytest_cache", ".mypy_cache"}
	envVars, err := walkProjectForCandidates(p.ProjectFS, []string{".py"}, ignoreDirs, re, 3, 5)
	if err != nil {
		return nil, err
	}

	routeRe := regexp.MustCompile(pyRouteRegex)
	processor := NewPythonRouteProcessor()
	routes, err := walkProjectForRoutes(p.ProjectFS, []string{".py"}, ignoreDirs, routeRe, processor, 3, 5)
	if err != nil {
		return nil, err
	}

	return &ProjectSpec{
		Name:                projectName,
		Language:            "python",
		ServiceRequirements: serviceRequirements,
		EnvVars:             envVars,
		Routes:              routes,
	}, nil
}

// PythonRouteProcessor handles Python-specific route processing including Django special cases
type PythonRouteProcessor struct{}

func NewPythonRouteProcessor() *PythonRouteProcessor {
	return &PythonRouteProcessor{}
}

func (p *PythonRouteProcessor) ProcessMatch(match RouteMatch, filePath string) []RouteCandidate {
	// Handle Django empty path case: path('') -> GET /
	if p.isDjangoEmptyPath(match) {
		return []RouteCandidate{{
			Method:  "GET",
			Path:    "/", // Convert Django empty path to standard root path
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

	return []RouteCandidate{{
		Method:  method,
		Path:    routePath,
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
