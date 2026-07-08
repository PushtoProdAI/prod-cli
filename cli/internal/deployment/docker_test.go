package deployment

import (
	"io"
	"os"
	"path/filepath"
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

func TestGenerateDockerfile_Ruby(t *testing.T) {
	// Build context is a fresh empty dir so F4's user-Dockerfile reuse path can't interfere.
	buildCtx := t.TempDir()

	t.Run("Rails app precompiles assets", func(t *testing.T) {
		dg := NewDockerGeneratorWithBackend(io.Discard, []EnvVar{}, nil)
		spec := &DeploymentSpec{
			Name:         "widget",
			Language:     "ruby",
			BuildCommand: "bundle install",
			StartCommand: "bundle exec rails server -b 0.0.0.0",
			Services:     []Service{{Type: "framework", Provider: "rails"}},
			Metadata:     map[string]any{"buildContext": buildCtx},
		}
		artifacts, err := dg.GenerateDockerfile(spec)
		if err != nil {
			t.Fatalf("GenerateDockerfile: %v", err)
		}
		df := artifacts.Dockerfile
		if !strings.Contains(df, "ruby:3.3-slim") {
			t.Errorf("expected the ruby base image; got:\n%s", df)
		}
		if !strings.Contains(df, "RUN bundle install") {
			t.Errorf("expected the bundle install build step; got:\n%s", df)
		}
		if !strings.Contains(df, "assets:precompile") {
			t.Errorf("a Rails app should precompile assets; got:\n%s", df)
		}
		if !strings.Contains(df, "bundle exec rails server") {
			t.Errorf("expected the Rails start command; got:\n%s", df)
		}
	})

	t.Run("Sinatra app skips asset precompile", func(t *testing.T) {
		dg := NewDockerGeneratorWithBackend(io.Discard, []EnvVar{}, nil)
		spec := &DeploymentSpec{
			Name:         "tiny",
			Language:     "ruby",
			BuildCommand: "bundle install",
			StartCommand: "bundle exec ruby app.rb",
			Services:     []Service{{Type: "framework", Provider: "sinatra"}},
			Metadata:     map[string]any{"buildContext": buildCtx},
		}
		artifacts, err := dg.GenerateDockerfile(spec)
		if err != nil {
			t.Fatalf("GenerateDockerfile: %v", err)
		}
		df := artifacts.Dockerfile
		if strings.Contains(df, "assets:precompile") {
			t.Errorf("a non-Rails Ruby app must not precompile assets; got:\n%s", df)
		}
		if !strings.Contains(df, "bundle exec ruby app.rb") {
			t.Errorf("expected the Sinatra start command; got:\n%s", df)
		}
		if artifacts.DockerIgnore == "" {
			t.Error("expected a ruby .dockerignore to be generated")
		}
	})
}

func TestHasUserDockerfile(t *testing.T) {
	dir := t.TempDir()
	if hasUserDockerfile(dir) {
		t.Error("no Dockerfile → false")
	}
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM alpine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !hasUserDockerfile(dir) {
		t.Error("a user-committed Dockerfile → true")
	}
	// A prod-generated one (carries the marker) is NOT the user's.
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(generatedDockerfileMarker+"\nFROM golang\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if hasUserDockerfile(dir) {
		t.Error("a prod-generated Dockerfile must not count as the user's")
	}
}

func TestGenerateDockerfileReusesUserDockerfile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM alpine\nCMD [\"true\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dg := NewDockerGenerator(nil, nil)
	defer dg.Close()
	spec := &DeploymentSpec{Name: "app", Language: "go", Metadata: map[string]any{"buildContext": dir}}

	art, err := dg.GenerateDockerfile(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !art.UseExisting {
		t.Error("a user Dockerfile should be reused (UseExisting)")
	}

	// With a prod-generated Dockerfile present, prod regenerates (and re-marks) rather than reusing.
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(generatedDockerfileMarker+"\nstale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	art2, err := dg.GenerateDockerfile(spec)
	if err != nil {
		t.Fatal(err)
	}
	if art2.UseExisting {
		t.Error("a prod-generated Dockerfile must be regenerated, not reused")
	}
	if !strings.HasPrefix(art2.Dockerfile, generatedDockerfileMarker) {
		t.Error("a generated Dockerfile must carry the marker")
	}
}
