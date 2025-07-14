package analyzer

import (
	"bufio"
	"fmt"
	"io/fs"
	"regexp"
	"strings"
	"sync"

	"github.com/pelletier/go-toml/v2"
)

// PythonAnalyzer implements the Analyzer interface for Python projects
type PythonAnalyzer struct {
	ProjectFS fs.FS
	Cache     *AnalysisCache
}

// AnalysisCache provides caching for file analysis
type AnalysisCache struct {
	mu    sync.RWMutex
	cache map[string]interface{}
}

// RuntimeInfo contains Python runtime information
type RuntimeInfo struct {
	Version        string `json:"version"`
	Source         string `json:"source"`          // .python-version, runtime.txt, pyproject.toml, etc.
	PackageManager string `json:"package_manager"` // pip, poetry, pipenv
}

// FrameworkInfo contains detected framework information
type FrameworkInfo struct {
	Name     string `json:"name"` // django, flask, fastapi, etc.
	Version  string `json:"version"`
	Detected bool   `json:"detected"`
}

// Dependency represents a Python package dependency
type Dependency struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Source  string `json:"source"` // requirements.txt, Pipfile, pyproject.toml
}

// PyProjectToml represents the structure of pyproject.toml
type PyProjectToml struct {
	Project struct {
		Name         string   `toml:"name"`
		Version      string   `toml:"version"`
		Dependencies []string `toml:"dependencies"`
	} `toml:"project"`
	BuildSystem struct {
		Requires []string `toml:"requires"`
	} `toml:"build-system"`
	Tool struct {
		Poetry struct {
			Name         string                 `toml:"name"`
			Version      string                 `toml:"version"`
			Dependencies map[string]interface{} `toml:"dependencies"`
		} `toml:"poetry"`
	} `toml:"tool"`
}

// Pipfile represents the structure of Pipfile
type Pipfile struct {
	Packages    map[string]interface{} `toml:"packages"`
	DevPackages map[string]interface{} `toml:"dev-packages"`
}

// Python service mappings for common packages
var PythonServiceMappings = map[string]ServiceRequirement{
	// Database drivers
	"psycopg2":               ServicePostgres,
	"psycopg2-binary":        ServicePostgres,
	"asyncpg":                ServicePostgres,
	"pymysql":                ServiceMySQL,
	"mysql-connector-python": ServiceMySQL,
	"pymongo":                ServiceMongo,
	"motor":                  ServiceMongo,
	"sqlite3":                ServiceSQLite,

	// Cache/Session stores
	"redis":        ServiceRedis,
	"aioredis":     ServiceRedis,
	"django-redis": ServiceRedis,

	// Web frameworks
	"django":   {Type: "framework", Provider: "django"},
	"flask":    {Type: "framework", Provider: "flask"},
	"fastapi":  {Type: "framework", Provider: "fastapi"},
	"uvicorn":  {Type: "framework", Provider: "fastapi"},
	"gunicorn": {Type: "framework", Provider: "wsgi"},

	// Database ORMs
	"sqlalchemy": {Type: "orm", Provider: "sqlalchemy"},
	"django-orm": {Type: "orm", Provider: "django"},
	"peewee":     {Type: "orm", Provider: "peewee"},

	// Search engines
	"elasticsearch": {Type: "search", Provider: "elasticsearch"},
	"opensearch-py": {Type: "search", Provider: "opensearch"},

	// Email services
	"django-email": {Type: "email", Provider: "django"},
	"sendgrid":     {Type: "email", Provider: "sendgrid"},

	// File storage
	"boto3":                {Type: "storage", Provider: "s3"},
	"google-cloud-storage": {Type: "storage", Provider: "gcs"},

	// Monitoring
	"newrelic":   {Type: "monitoring", Provider: "newrelic"},
	"sentry-sdk": {Type: "monitoring", Provider: "sentry"},
}

// NewPythonAnalyzer creates a new Python analyzer instance
func NewPythonAnalyzer(projectFS fs.FS) Analyzer {
	return &PythonAnalyzer{
		ProjectFS: projectFS,
		Cache: &AnalysisCache{
			cache: make(map[string]interface{}),
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

	return &ProjectSpec{
		Name:                projectName,
		Language:            "python",
		ServiceRequirements: serviceRequirements,
	}, nil
}

// detectRuntime detects Python runtime version and package manager
func (p *PythonAnalyzer) detectRuntime() (*RuntimeInfo, error) {
	runtime := &RuntimeInfo{}

	// Check .python-version
	if data, err := fs.ReadFile(p.ProjectFS, ".python-version"); err == nil {
		runtime.Version = strings.TrimSpace(string(data))
		runtime.Source = ".python-version"
	}

	// Check runtime.txt
	if data, err := fs.ReadFile(p.ProjectFS, "runtime.txt"); err == nil {
		content := strings.TrimSpace(string(data))
		if strings.HasPrefix(content, "python-") {
			runtime.Version = strings.TrimPrefix(content, "python-")
			runtime.Source = "runtime.txt"
		}
	}

	// Check pyproject.toml
	if data, err := fs.ReadFile(p.ProjectFS, "pyproject.toml"); err == nil {
		var pyproject PyProjectToml
		if err := toml.Unmarshal(data, &pyproject); err == nil {
			// Check if it's a poetry project
			if pyproject.Tool.Poetry.Name != "" {
				runtime.PackageManager = "poetry"
				if runtime.Source == "" {
					runtime.Source = "pyproject.toml"
				}
			}
		}
	}

	// Check Pipfile
	if _, err := fs.Stat(p.ProjectFS, "Pipfile"); err == nil {
		runtime.PackageManager = "pipenv"
		if runtime.Source == "" {
			runtime.Source = "Pipfile"
		}
	}

	// Default to pip if no package manager detected
	if runtime.PackageManager == "" {
		runtime.PackageManager = "pip"
	}

	return runtime, nil
}

// extractDependencies extracts dependencies from various Python package files
func (p *PythonAnalyzer) extractDependencies() ([]Dependency, error) {
	var dependencies []Dependency

	// Parse requirements.txt
	if deps, err := p.parseRequirementsTxt(); err == nil {
		dependencies = append(dependencies, deps...)
	}

	// Parse Pipfile
	if deps, err := p.parsePipfile(); err == nil {
		dependencies = append(dependencies, deps...)
	}

	// Parse pyproject.toml
	if deps, err := p.parsePyProjectToml(); err == nil {
		dependencies = append(dependencies, deps...)
	}

	// Parse setup.py
	if deps, err := p.parseSetupPy(); err == nil {
		dependencies = append(dependencies, deps...)
	}

	return dependencies, nil
}

// parseRequirementsTxt parses requirements.txt file
func (p *PythonAnalyzer) parseRequirementsTxt() ([]Dependency, error) {
	data, err := fs.ReadFile(p.ProjectFS, "requirements.txt")
	if err != nil {
		return nil, err
	}

	var dependencies []Dependency
	scanner := bufio.NewScanner(strings.NewReader(string(data)))

	// Regex to match package specifications
	re := regexp.MustCompile(`^([a-zA-Z0-9_-]+)([<>=!~]+.*)?$`)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip comments and empty lines
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		matches := re.FindStringSubmatch(line)
		if len(matches) >= 2 {
			dependency := Dependency{
				Name:    matches[1],
				Version: strings.TrimSpace(matches[2]),
				Source:  "requirements.txt",
			}
			dependencies = append(dependencies, dependency)
		}
	}

	return dependencies, nil
}

// parsePipfile parses Pipfile
func (p *PythonAnalyzer) parsePipfile() ([]Dependency, error) {
	data, err := fs.ReadFile(p.ProjectFS, "Pipfile")
	if err != nil {
		return nil, err
	}

	var pipfile Pipfile
	if err := toml.Unmarshal(data, &pipfile); err != nil {
		return nil, err
	}

	var dependencies []Dependency

	// Parse packages
	for pkg, version := range pipfile.Packages {
		dependency := Dependency{
			Name:   pkg,
			Source: "Pipfile",
		}

		if versionStr, ok := version.(string); ok {
			dependency.Version = versionStr
		}

		dependencies = append(dependencies, dependency)
	}

	return dependencies, nil
}

// parsePyProjectToml parses pyproject.toml
func (p *PythonAnalyzer) parsePyProjectToml() ([]Dependency, error) {
	data, err := fs.ReadFile(p.ProjectFS, "pyproject.toml")
	if err != nil {
		return nil, err
	}

	var pyproject PyProjectToml
	if err := toml.Unmarshal(data, &pyproject); err != nil {
		return nil, err
	}

	var dependencies []Dependency

	// Parse poetry dependencies
	for pkg, version := range pyproject.Tool.Poetry.Dependencies {
		dependency := Dependency{
			Name:   pkg,
			Source: "pyproject.toml",
		}

		if versionStr, ok := version.(string); ok {
			dependency.Version = versionStr
		}

		dependencies = append(dependencies, dependency)
	}

	return dependencies, nil
}

// parseSetupPy parses setup.py (basic parsing)
func (p *PythonAnalyzer) parseSetupPy() ([]Dependency, error) {
	data, err := fs.ReadFile(p.ProjectFS, "setup.py")
	if err != nil {
		return nil, err
	}

	var dependencies []Dependency

	// Basic regex parsing for install_requires
	re := regexp.MustCompile(`install_requires\s*=\s*\[(.*?)\]`)
	matches := re.FindStringSubmatch(string(data))

	if len(matches) >= 2 {
		requires := matches[1]
		// Parse individual requirements
		reqRe := regexp.MustCompile(`['"]([^'"]+)['"]`)
		reqMatches := reqRe.FindAllStringSubmatch(requires, -1)

		for _, match := range reqMatches {
			if len(match) >= 2 {
				dependency := Dependency{
					Name:   match[1],
					Source: "setup.py",
				}
				dependencies = append(dependencies, dependency)
			}
		}
	}

	return dependencies, nil
}

// detectFramework detects the web framework being used
func (p *PythonAnalyzer) detectFramework(dependencies []Dependency) (*FrameworkInfo, error) {
	framework := &FrameworkInfo{}

	// Check dependencies for frameworks
	for _, dep := range dependencies {
		switch dep.Name {
		case "django":
			framework.Name = "django"
			framework.Version = dep.Version
			framework.Detected = true
			return framework, nil
		case "flask":
			framework.Name = "flask"
			framework.Version = dep.Version
			framework.Detected = true
			return framework, nil
		case "fastapi":
			framework.Name = "fastapi"
			framework.Version = dep.Version
			framework.Detected = true
			return framework, nil
		}
	}

	// Check for framework-specific files
	if _, err := fs.Stat(p.ProjectFS, "manage.py"); err == nil {
		framework.Name = "django"
		framework.Detected = true
		return framework, nil
	}

	if _, err := fs.Stat(p.ProjectFS, "app.py"); err == nil {
		// Could be Flask or FastAPI, check content
		if data, err := fs.ReadFile(p.ProjectFS, "app.py"); err == nil {
			content := string(data)
			if strings.Contains(content, "Flask") {
				framework.Name = "flask"
				framework.Detected = true
			} else if strings.Contains(content, "FastAPI") {
				framework.Name = "fastapi"
				framework.Detected = true
			}
		}
	}

	return framework, nil
}

// extractServiceRequirements extracts service requirements from dependencies
func (p *PythonAnalyzer) extractServiceRequirements(dependencies []Dependency) ([]ServiceRequirement, error) {
	var services []ServiceRequirement
	seen := make(map[string]bool)

	for _, dep := range dependencies {
		if service, exists := PythonServiceMappings[dep.Name]; exists {
			key := fmt.Sprintf("%s-%s", service.Type, service.Provider)
			if !seen[key] {
				services = append(services, service)
				seen[key] = true
			}
		}
	}

	return services, nil
}

// extractProjectName extracts the project name from various sources
func (p *PythonAnalyzer) extractProjectName(runtime *RuntimeInfo, dependencies []Dependency) string {
	// Try to get from pyproject.toml first
	if data, err := fs.ReadFile(p.ProjectFS, "pyproject.toml"); err == nil {
		var pyproject PyProjectToml
		if err := toml.Unmarshal(data, &pyproject); err == nil {
			if pyproject.Project.Name != "" {
				return pyproject.Project.Name
			}
			if pyproject.Tool.Poetry.Name != "" {
				return pyproject.Tool.Poetry.Name
			}
		}
	}

	// Try to get from setup.py
	if data, err := fs.ReadFile(p.ProjectFS, "setup.py"); err == nil {
		re := regexp.MustCompile(`name\s*=\s*['"]([^'"]+)['"]`)
		matches := re.FindStringSubmatch(string(data))
		if len(matches) >= 2 {
			return matches[1]
		}
	}

	// Default to "python-project"
	return "python-project"
}
