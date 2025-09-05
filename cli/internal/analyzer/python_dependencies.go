package analyzer

import (
	"bufio"
	"io/fs"
	"regexp"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Dependency represents a Python package dependency
type Dependency struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Source  string `json:"source"`
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
			Name         string         `toml:"name"`
			Version      string         `toml:"version"`
			Dependencies map[string]any `toml:"dependencies"`
		} `toml:"poetry"`
	} `toml:"tool"`
}

// Pipfile represents the structure of Pipfile
type Pipfile struct {
	Packages    map[string]any `toml:"packages"`
	DevPackages map[string]any `toml:"dev-packages"`
}

// DependencyManagementType represents the detected dependency management approach
type DependencyManagementType int

const (
	DepMgmtUnknown DependencyManagementType = iota
	DepMgmtPoetry
	DepMgmtPipenv
	DepMgmtPipTools
	DepMgmtRequirementsTxt
	DepMgmtPEP621
	DepMgmtSetupPy
	DepMgmtHatch
)

func (p *PythonAnalyzer) hasPoetryConfig() bool {
	data, err := fs.ReadFile(p.ProjectFS, "pyproject.toml")
	if err != nil {
		return false
	}

	// Simple check for poetry section
	content := string(data)
	return strings.Contains(content, "[tool.poetry]")
}

func (p *PythonAnalyzer) hasPEP621Config() bool {
	data, err := fs.ReadFile(p.ProjectFS, "pyproject.toml")
	if err != nil {
		return false
	}

	// Simple check for PEP 621 project section with dependencies
	content := string(data)
	return strings.Contains(content, "[project]") && strings.Contains(content, "dependencies")
}

func (p *PythonAnalyzer) hasHatchConfig() bool {
	data, err := fs.ReadFile(p.ProjectFS, "pyproject.toml")
	if err != nil {
		return false
	}

	// Check for hatch-specific configuration
	content := string(data)
	return strings.Contains(content, "[tool.hatch]") || strings.Contains(content, "hatchling")
}

// detectDependencyManagement determines which dependency management approach is used
func (p *PythonAnalyzer) detectDependencyManagement() DependencyManagementType {
	// Check for Poetry (pyproject.toml with tool.poetry section)
	if p.hasPoetryConfig() {
		return DepMgmtPoetry
	}

	// Check for Hatch (pyproject.toml with tool.hatch section or hatchling)
	if p.hasHatchConfig() {
		return DepMgmtHatch
	}

	// Check for Pipenv (Pipfile)
	if _, err := fs.Stat(p.ProjectFS, "Pipfile"); err == nil {
		return DepMgmtPipenv
	}

	// Check for pip-tools (requirements.in)
	if _, err := fs.Stat(p.ProjectFS, "requirements.in"); err == nil {
		return DepMgmtPipTools
	}

	// Check for standard requirements.txt
	if _, err := fs.Stat(p.ProjectFS, "requirements.txt"); err == nil {
		return DepMgmtRequirementsTxt
	}

	// Check for pyproject.toml with PEP 621 dependencies
	if p.hasPEP621Config() {
		return DepMgmtPEP621
	}

	// Check for setup.py
	if _, err := fs.Stat(p.ProjectFS, "setup.py"); err == nil {
		return DepMgmtSetupPy
	}

	return DepMgmtUnknown
}

func (p *PythonAnalyzer) extractDependencies() ([]Dependency, error) {
	var dependencies []Dependency

	depMgmt := p.detectDependencyManagement()

	switch depMgmt {
	case DepMgmtPoetry:
		if deps, err := p.parsePyProjectToml(); err == nil {
			dependencies = append(dependencies, deps...)
		}
	case DepMgmtHatch:
		if deps, err := p.parsePyProjectToml(); err == nil {
			dependencies = append(dependencies, deps...)
		}
	case DepMgmtPipenv:
		if deps, err := p.parsePipfile(); err == nil {
			dependencies = append(dependencies, deps...)
		}
	case DepMgmtPipTools, DepMgmtRequirementsTxt:
		if deps, err := p.parseRequirementsTxt(); err == nil {
			dependencies = append(dependencies, deps...)
		}
	case DepMgmtPEP621:
		if deps, err := p.parsePyProjectToml(); err == nil {
			dependencies = append(dependencies, deps...)
		}
	case DepMgmtSetupPy:
		if deps, err := p.parseSetupPy(); err == nil {
			dependencies = append(dependencies, deps...)
		}
	default:
		// Fallback: try all parsers if we couldn't detect the management type
		// This maintains backward compatibility for edge cases
		if deps, err := p.parseRequirementsTxt(); err == nil {
			dependencies = append(dependencies, deps...)
		}
		if deps, err := p.parsePipfile(); err == nil {
			dependencies = append(dependencies, deps...)
		}
		if deps, err := p.parsePyProjectToml(); err == nil {
			dependencies = append(dependencies, deps...)
		}
		if deps, err := p.parseSetupPy(); err == nil {
			dependencies = append(dependencies, deps...)
		}
	}

	return dependencies, nil
}

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
		reqRe := regexp.MustCompile(`['\"]([^'\"]+)['\"]`)
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

		return dependencies, nil
	}

	return dependencies, nil
}
