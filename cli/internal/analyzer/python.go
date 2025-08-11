package analyzer

import (
	"fmt"
	"io/fs"
	"strings"
)

// PythonAnalyzer implements the Analyzer interface for Python projects
type PythonAnalyzer struct {
	ProjectFS fs.FS
	Cache     *AnalysisCache
}

// NewPythonAnalyzer creates a new Python analyzer instance
func NewPythonAnalyzer(projectFS fs.FS) Analyzer {
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

	return &ProjectSpec{
		Name:                projectName,
		Language:            "python",
		ServiceRequirements: serviceRequirements,
	}, nil
}
