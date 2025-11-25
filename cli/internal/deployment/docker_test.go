package deployment

import (
	"io"
	"strings"
	"testing"
)

func TestGenerateDockerfile_Python(t *testing.T) {
	tests := []struct {
		name          string
		buildCommand  string
		expectedCopy  string
		expectedBuild string
	}{
		{
			name:          "Poetry project",
			buildCommand:  "poetry install --only=main",
			expectedCopy:  "COPY pyproject.toml poetry.lock* ./",
			expectedBuild: "RUN poetry install --only=main",
		},
		{
			name:          "Pipenv project",
			buildCommand:  "pipenv install --deploy",
			expectedCopy:  "COPY Pipfile Pipfile.lock* ./",
			expectedBuild: "RUN pip install --no-cache-dir pipenv && pipenv install --deploy",
		},
		{
			name:          "Requirements.txt project",
			buildCommand:  "pip install -r requirements.txt",
			expectedCopy:  "COPY requirements.txt .",
			expectedBuild: "RUN pip install -r requirements.txt",
		},
		{
			name:          "Setup.py project",
			buildCommand:  "pip install .",
			expectedCopy:  "COPY pyproject.toml setup.py* setup.cfg* ./",
			expectedBuild: "RUN pip install .",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Pass nil backend client to skip network calls
			dg := NewDockerGeneratorWithBackend(io.Discard, []EnvVar{}, nil)
			spec := &DeploymentSpec{
				Name:         "test-app",
				Language:     "python",
				BuildCommand: tt.buildCommand,
				StartCommand: "python app.py",
			}

			artifacts, err := dg.GenerateDockerfile(spec)
			if err != nil {
				t.Fatalf("Failed to generate Dockerfile: %v", err)
			}

			dockerfile := artifacts.Dockerfile

			// Check that the expected copy command is present
			if !strings.Contains(dockerfile, tt.expectedCopy) {
				t.Errorf("Expected copy command %q not found in Dockerfile:\n%s", tt.expectedCopy, dockerfile)
			}

			// Check that the expected build command is present
			if !strings.Contains(dockerfile, tt.expectedBuild) {
				t.Errorf("Expected build command %q not found in Dockerfile:\n%s", tt.expectedBuild, dockerfile)
			}
		})
	}
}

func TestGenerateDockerfile_PythonFallback(t *testing.T) {
	// Pass nil backend client to skip network calls
	dg := NewDockerGeneratorWithBackend(io.Discard, []EnvVar{}, nil)
	spec := &DeploymentSpec{
		Name:         "test-app",
		Language:     "python",
		BuildCommand: "", // Empty build command should fall back to requirements.txt
		StartCommand: "python app.py",
	}

	artifacts, err := dg.GenerateDockerfile(spec)
	if err != nil {
		t.Fatalf("Failed to generate Dockerfile: %v", err)
	}

	dockerfile := artifacts.Dockerfile

	// Should fall back to requirements.txt
	expectedCopy := "COPY requirements.txt ."
	expectedBuild := "RUN pip install --no-cache-dir -r requirements.txt"

	if !strings.Contains(dockerfile, expectedCopy) {
		t.Errorf("Expected fallback copy command %q not found in Dockerfile:\n%s", expectedCopy, dockerfile)
	}

	if !strings.Contains(dockerfile, expectedBuild) {
		t.Errorf("Expected fallback build command %q not found in Dockerfile:\n%s", expectedBuild, dockerfile)
	}
}

func TestGenerateDockerfile_DjangoCollectStatic(t *testing.T) {
	tests := []struct {
		name                    string
		services                []Service
		shouldHaveCollectStatic bool
	}{
		{
			name: "Django project should have collectstatic",
			services: []Service{
				{Type: "framework", Provider: "django"},
			},
			shouldHaveCollectStatic: true,
		},
		{
			name: "Flask project should not have collectstatic",
			services: []Service{
				{Type: "framework", Provider: "flask"},
			},
			shouldHaveCollectStatic: false,
		},
		{
			name: "FastAPI project should not have collectstatic",
			services: []Service{
				{Type: "framework", Provider: "fastapi"},
			},
			shouldHaveCollectStatic: false,
		},
		{
			name:                    "Non-framework Python project should not have collectstatic",
			services:                []Service{},
			shouldHaveCollectStatic: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dg := NewDockerGeneratorWithBackend(io.Discard, []EnvVar{}, nil)
			spec := &DeploymentSpec{
				Name:         "test-app",
				Language:     "python",
				BuildCommand: "pip install -r requirements.txt",
				StartCommand: "gunicorn app:application",
				Services:     tt.services,
			}

			artifacts, err := dg.GenerateDockerfile(spec)
			if err != nil {
				t.Fatalf("Failed to generate Dockerfile: %v", err)
			}

			dockerfile := artifacts.Dockerfile
			hasCollectStatic := strings.Contains(dockerfile, "python manage.py collectstatic")

			if tt.shouldHaveCollectStatic && !hasCollectStatic {
				t.Errorf("Expected collectstatic command in Dockerfile for %s, but it was not found", tt.name)
				// Print relevant lines for debugging
				lines := strings.Split(dockerfile, "\n")
				for i, line := range lines {
					if strings.Contains(line, "Collect static") {
						t.Logf("Line %d: %s", i, line)
						if i+1 < len(lines) {
							t.Logf("Line %d: %s", i+1, lines[i+1])
						}
					}
				}
			}

			if !tt.shouldHaveCollectStatic && hasCollectStatic {
				t.Errorf("Did not expect collectstatic command in Dockerfile for %s, but it was found:\n%s", tt.name, dockerfile)
			}
		})
	}
}
