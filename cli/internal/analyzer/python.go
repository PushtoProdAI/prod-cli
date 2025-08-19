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

	return &ProjectSpec{
		Name:                projectName,
		Language:            "python",
		ServiceRequirements: serviceRequirements,
		EnvVars:             envVars,
	}, nil
}
