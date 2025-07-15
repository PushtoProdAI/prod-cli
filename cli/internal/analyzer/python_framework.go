package analyzer

import (
	"io/fs"
	"strings"
)

// FrameworkInfo contains detected framework information
type FrameworkInfo struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Detected bool   `json:"detected"`
}

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
