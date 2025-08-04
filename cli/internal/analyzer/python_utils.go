package analyzer

import (
	"io/fs"
	"regexp"
	"sync"

	"github.com/pelletier/go-toml/v2"
)

type AnalysisCache struct {
	mu    sync.RWMutex
	cache map[string]any
}

func (p *PythonAnalyzer) extractProjectName(_ *RuntimeInfo, _ []Dependency) string {
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
		re := regexp.MustCompile(`name\s*=\s*['\"]([^'\"]+)['\"]`)
		matches := re.FindStringSubmatch(string(data))
		if len(matches) >= 2 {
			return matches[1]
		}
	}

	// Default to "python-project"
	return "python-project"
}
