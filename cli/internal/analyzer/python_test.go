package analyzer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPythonAnalyzer_CanHandle(t *testing.T) {
	tests := []struct {
		name     string
		files    map[string]string
		expected bool
	}{
		{
			name: "requirements.txt present",
			files: map[string]string{
				"requirements.txt": "flask==2.0.1\nredis==4.0.0",
			},
			expected: true,
		},
		{
			name: "Pipfile present",
			files: map[string]string{
				"Pipfile": `[[source]]
url = "https://pypi.org/simple"
verify_ssl = true
name = "pypi"

[packages]
flask = "*"
redis = "*"`,
			},
			expected: true,
		},
		{
			name: "pyproject.toml present",
			files: map[string]string{
				"pyproject.toml": `[tool.poetry]
name = "test-project"
version = "0.1.0"
dependencies = { flask = "*" }`,
			},
			expected: true,
		},
		{
			name: "setup.py present",
			files: map[string]string{
				"setup.py": `from setuptools import setup
setup(name="test-project")`,
			},
			expected: true,
		},
		{
			name: ".python-version present",
			files: map[string]string{
				".python-version": "3.9.0",
			},
			expected: true,
		},
		{
			name: "runtime.txt present",
			files: map[string]string{
				"runtime.txt": "python-3.9.0",
			},
			expected: true,
		},
		{
			name: "Python file present",
			files: map[string]string{
				"app.py": "print('Hello, World!')",
			},
			expected: true,
		},
		{
			name:     "No Python files",
			files:    map[string]string{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary directory
			tmpDir, err := os.MkdirTemp("", "python-analyzer-test")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(tmpDir)

			// Create test files
			for filename, content := range tt.files {
				filePath := filepath.Join(tmpDir, filename)
				if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
			}

			// Create analyzer
			projectFS := os.DirFS(tmpDir)
			analyzer := NewPythonAnalyzer(projectFS)

			// Test CanHandle
			canHandle, err := analyzer.CanHandle()
			if err != nil {
				t.Fatal(err)
			}

			if canHandle != tt.expected {
				t.Errorf("CanHandle() = %v, want %v", canHandle, tt.expected)
			}
		})
	}
}

func TestPythonAnalyzer_Analyze(t *testing.T) {
	tests := []struct {
		name             string
		files            map[string]string
		expectedName     string
		expectedLang     string
		expectedServices []ServiceRequirement
	}{
		{
			name: "Flask app with Redis",
			files: map[string]string{
				"requirements.txt": "flask==2.0.1\nredis==4.0.0\npsycopg2-binary==2.9.0",
				"app.py":           "from flask import Flask\napp = Flask(__name__)",
			},
			expectedName: "python-project",
			expectedLang: "python",
			expectedServices: []ServiceRequirement{
				{Type: "framework", Provider: "flask"},
				{Type: "cache", Provider: "redis"},
				{Type: "database", Provider: "postgresql"},
			},
		},
		{
			name: "Django app with PostgreSQL",
			files: map[string]string{
				"requirements.txt": "django==4.0.0\npsycopg2-binary==2.9.0",
				"manage.py":        "#!/usr/bin/env python",
			},
			expectedName: "python-project",
			expectedLang: "python",
			expectedServices: []ServiceRequirement{
				{Type: "framework", Provider: "django"},
				{Type: "database", Provider: "postgresql"},
			},
		},
		{
			name: "FastAPI app with MongoDB",
			files: map[string]string{
				"requirements.txt": "fastapi==0.68.0\nuvicorn==0.15.0\npymongo==4.0.0",
				"main.py":          "from fastapi import FastAPI\napp = FastAPI()",
			},
			expectedName: "python-project",
			expectedLang: "python",
			expectedServices: []ServiceRequirement{
				{Type: "framework", Provider: "fastapi"},
				{Type: "database", Provider: "mongodb"},
			},
		},
		{
			name: "Poetry project",
			files: map[string]string{
				"pyproject.toml": `[tool.poetry]
name = "my-poetry-project"
version = "0.1.0"

[tool.poetry.dependencies]
flask = "*"
redis = "*"`,
			},
			expectedName: "my-poetry-project",
			expectedLang: "python",
			expectedServices: []ServiceRequirement{
				{Type: "framework", Provider: "flask"},
				{Type: "cache", Provider: "redis"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary directory
			tmpDir, err := os.MkdirTemp("", "python-analyzer-test")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(tmpDir)

			// Create test files
			for filename, content := range tt.files {
				filePath := filepath.Join(tmpDir, filename)
				if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
			}

			// Create analyzer
			projectFS := os.DirFS(tmpDir)
			analyzer := NewPythonAnalyzer(projectFS)

			// Test Analyze
			result, err := analyzer.Analyze()
			if err != nil {
				t.Fatal(err)
			}

			// Check basic fields
			if result.Name != tt.expectedName {
				t.Errorf("Name = %v, want %v", result.Name, tt.expectedName)
			}

			if result.Language != tt.expectedLang {
				t.Errorf("Language = %v, want %v", result.Language, tt.expectedLang)
			}

			// Check service requirements
			if len(result.ServiceRequirements) != len(tt.expectedServices) {
				t.Errorf("ServiceRequirements count = %v, want %v", len(result.ServiceRequirements), len(tt.expectedServices))
			}

			// Check each expected service
			for _, expectedService := range tt.expectedServices {
				found := false
				for _, actualService := range result.ServiceRequirements {
					if actualService.Type == expectedService.Type && actualService.Provider == expectedService.Provider {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected service %+v not found in %+v", expectedService, result.ServiceRequirements)
				}
			}
		})
	}
}

func TestPythonAnalyzer_parseRequirementsTxt(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected []Dependency
	}{
		{
			name: "Basic requirements",
			content: `flask==2.0.1
redis>=4.0.0
psycopg2-binary==2.9.0`,
			expected: []Dependency{
				{Name: "flask", Version: "==2.0.1", Source: "requirements.txt"},
				{Name: "redis", Version: ">=4.0.0", Source: "requirements.txt"},
				{Name: "psycopg2-binary", Version: "==2.9.0", Source: "requirements.txt"},
			},
		},
		{
			name: "With comments and empty lines",
			content: `# Web framework
flask==2.0.1

# Database
psycopg2-binary==2.9.0`,
			expected: []Dependency{
				{Name: "flask", Version: "==2.0.1", Source: "requirements.txt"},
				{Name: "psycopg2-binary", Version: "==2.9.0", Source: "requirements.txt"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary directory
			tmpDir, err := os.MkdirTemp("", "python-analyzer-test")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(tmpDir)

			// Create requirements.txt
			reqPath := filepath.Join(tmpDir, "requirements.txt")
			if err := os.WriteFile(reqPath, []byte(tt.content), 0644); err != nil {
				t.Fatal(err)
			}

			// Create analyzer
			projectFS := os.DirFS(tmpDir)
			analyzer := NewPythonAnalyzer(projectFS).(*PythonAnalyzer)

			// Test parsing
			dependencies, err := analyzer.parseRequirementsTxt()
			if err != nil {
				t.Fatal(err)
			}

			// Check dependencies
			if len(dependencies) != len(tt.expected) {
				t.Errorf("Dependencies count = %v, want %v", len(dependencies), len(tt.expected))
			}

			for i, expected := range tt.expected {
				if i >= len(dependencies) {
					t.Errorf("Expected dependency %+v not found", expected)
					continue
				}

				actual := dependencies[i]
				if actual.Name != expected.Name {
					t.Errorf("Dependency[%d].Name = %v, want %v", i, actual.Name, expected.Name)
				}
				if actual.Version != expected.Version {
					t.Errorf("Dependency[%d].Version = %v, want %v", i, actual.Version, expected.Version)
				}
				if actual.Source != expected.Source {
					t.Errorf("Dependency[%d].Source = %v, want %v", i, actual.Source, expected.Source)
				}
			}
		})
	}
}

func TestPythonAnalyzer_detectFramework(t *testing.T) {
	tests := []struct {
		name             string
		dependencies     []Dependency
		files            map[string]string
		expectedName     string
		expectedDetected bool
	}{
		{
			name: "Django in dependencies",
			dependencies: []Dependency{
				{Name: "django", Version: "4.0.0"},
			},
			expectedName:     "django",
			expectedDetected: true,
		},
		{
			name: "Flask in dependencies",
			dependencies: []Dependency{
				{Name: "flask", Version: "2.0.1"},
			},
			expectedName:     "flask",
			expectedDetected: true,
		},
		{
			name: "FastAPI in dependencies",
			dependencies: []Dependency{
				{Name: "fastapi", Version: "0.68.0"},
			},
			expectedName:     "fastapi",
			expectedDetected: true,
		},
		{
			name:         "Django manage.py file",
			dependencies: []Dependency{},
			files: map[string]string{
				"manage.py": "#!/usr/bin/env python",
			},
			expectedName:     "django",
			expectedDetected: true,
		},
		{
			name:         "Flask in app.py",
			dependencies: []Dependency{},
			files: map[string]string{
				"app.py": "from flask import Flask\napp = Flask(__name__)",
			},
			expectedName:     "flask",
			expectedDetected: true,
		},
		{
			name:         "FastAPI in app.py",
			dependencies: []Dependency{},
			files: map[string]string{
				"app.py": "from fastapi import FastAPI\napp = FastAPI()",
			},
			expectedName:     "fastapi",
			expectedDetected: true,
		},
		{
			name:         "No framework detected",
			dependencies: []Dependency{},
			files: map[string]string{
				"main.py": "print('Hello, World!')",
			},
			expectedName:     "",
			expectedDetected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary directory
			tmpDir, err := os.MkdirTemp("", "python-analyzer-test")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(tmpDir)

			// Create test files
			for filename, content := range tt.files {
				filePath := filepath.Join(tmpDir, filename)
				if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
			}

			// Create analyzer
			projectFS := os.DirFS(tmpDir)
			analyzer := NewPythonAnalyzer(projectFS).(*PythonAnalyzer)

			// Test framework detection
			framework, err := analyzer.detectFramework(tt.dependencies)
			if err != nil {
				t.Fatal(err)
			}

			if framework.Name != tt.expectedName {
				t.Errorf("Framework.Name = %v, want %v", framework.Name, tt.expectedName)
			}

			if framework.Detected != tt.expectedDetected {
				t.Errorf("Framework.Detected = %v, want %v", framework.Detected, tt.expectedDetected)
			}
		})
	}
}

func TestPythonAnalyzer_extractServiceRequirements(t *testing.T) {
	tests := []struct {
		name             string
		dependencies     []Dependency
		expectedServices []ServiceRequirement
	}{
		{
			name: "Database and cache services",
			dependencies: []Dependency{
				{Name: "psycopg2-binary"},
				{Name: "redis"},
				{Name: "flask"},
			},
			expectedServices: []ServiceRequirement{
				{Type: "database", Provider: "postgresql"},
				{Type: "cache", Provider: "redis"},
				{Type: "framework", Provider: "flask"},
			},
		},
		{
			name: "Multiple database drivers",
			dependencies: []Dependency{
				{Name: "psycopg2-binary"},
				{Name: "pymysql"},
				{Name: "pymongo"},
			},
			expectedServices: []ServiceRequirement{
				{Type: "database", Provider: "postgresql"},
				{Type: "database", Provider: "mysql"},
				{Type: "database", Provider: "mongodb"},
			},
		},
		{
			name: "No services",
			dependencies: []Dependency{
				{Name: "requests"},
				{Name: "numpy"},
			},
			expectedServices: []ServiceRequirement{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create analyzer
			projectFS := os.DirFS(".")
			analyzer := NewPythonAnalyzer(projectFS).(*PythonAnalyzer)

			// Test service extraction
			services, err := analyzer.extractServiceRequirements(tt.dependencies)
			if err != nil {
				t.Fatal(err)
			}

			// Check service count
			if len(services) != len(tt.expectedServices) {
				t.Errorf("Services count = %v, want %v", len(services), len(tt.expectedServices))
			}

			// Check each expected service
			for _, expectedService := range tt.expectedServices {
				found := false
				for _, actualService := range services {
					if actualService.Type == expectedService.Type && actualService.Provider == expectedService.Provider {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected service %+v not found in %+v", expectedService, services)
				}
			}
		})
	}
}
