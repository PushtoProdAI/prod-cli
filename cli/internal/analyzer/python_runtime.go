package analyzer

import (
	"io/fs"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// RuntimeInfo contains Python runtime information
type RuntimeInfo struct {
	Version        string `json:"version"`
	Source         string `json:"source"`
	PackageManager string `json:"package_manager"`
}

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
