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

func TestGenerateDockerfile_Rust(t *testing.T) {
	// Build context is a fresh empty dir so the user-Dockerfile reuse path can't interfere.
	buildCtx := t.TempDir()

	dg := NewDockerGeneratorWithBackend(io.Discard, []EnvVar{}, nil)
	spec := &DeploymentSpec{
		Name:         "server",
		Language:     "rust",
		BuildCommand: "cargo build --release",
		StartCommand: "./target/release/server",
		Metadata:     map[string]any{"buildContext": buildCtx},
	}
	artifacts, err := dg.GenerateDockerfile(spec)
	if err != nil {
		t.Fatalf("GenerateDockerfile: %v", err)
	}
	df := artifacts.Dockerfile
	if !strings.Contains(df, "cargo build --release") {
		t.Errorf("expected the cargo release build step; got:\n%s", df)
	}
	if !strings.Contains(df, "gcr.io/distroless/cc-debian12") {
		t.Errorf("expected the distroless runtime image; got:\n%s", df)
	}
	// The binary name (spec.Name) must flow into the COPY-out of target/release.
	if !strings.Contains(df, "target/release/server") {
		t.Errorf("expected the binary name to reach the target/release copy; got:\n%s", df)
	}
	if !strings.Contains(df, `CMD ["/app/server"]`) {
		t.Errorf("expected the runtime CMD to run the copied binary; got:\n%s", df)
	}
	if artifacts.DockerIgnore == "" {
		t.Error("expected a rust .dockerignore to be generated")
	}
}

func TestGenerateDockerfile_Java(t *testing.T) {
	// Build context is a fresh empty dir so the user-Dockerfile reuse path can't interfere.
	buildCtx := t.TempDir()

	dg := NewDockerGeneratorWithBackend(io.Discard, []EnvVar{}, nil)
	spec := &DeploymentSpec{
		Name:         "widget-service",
		Language:     "java",
		BuildCommand: "mvn -q -DskipTests package",
		StartCommand: "java -jar app.jar",
		Metadata:     map[string]any{"buildContext": buildCtx},
	}
	artifacts, err := dg.GenerateDockerfile(spec)
	if err != nil {
		t.Fatalf("GenerateDockerfile: %v", err)
	}
	df := artifacts.Dockerfile
	if !strings.Contains(df, "eclipse-temurin:21-jdk") {
		t.Errorf("expected the JDK builder image; got:\n%s", df)
	}
	if !strings.Contains(df, "eclipse-temurin:21-jre") {
		t.Errorf("expected the JRE runtime image; got:\n%s", df)
	}
	// The detected build command must flow into the builder stage.
	if !strings.Contains(df, "mvn -q -DskipTests package") {
		t.Errorf("expected the Maven build command; got:\n%s", df)
	}
	// The fat jar must be located robustly, excluding Gradle's -plain.jar.
	if !strings.Contains(df, "'*-plain.jar'") {
		t.Errorf("expected the Gradle plain-jar exclusion; got:\n%s", df)
	}
	if !strings.Contains(df, `CMD ["java", "-jar", "/app/app.jar"]`) {
		t.Errorf("expected the runtime CMD to run the copied jar; got:\n%s", df)
	}
	// Spring reads SERVER_PORT; set both PORT and SERVER_PORT to the target port.
	if !strings.Contains(df, "ENV SERVER_PORT=") {
		t.Errorf("expected SERVER_PORT to be set; got:\n%s", df)
	}
	if artifacts.DockerIgnore == "" {
		t.Error("expected a java .dockerignore to be generated")
	}
}

func TestGenerateDockerfile_CSharp(t *testing.T) {
	// Build context is a fresh empty dir so the user-Dockerfile reuse path can't interfere.
	buildCtx := t.TempDir()

	dg := NewDockerGeneratorWithBackend(io.Discard, []EnvVar{}, nil)
	spec := &DeploymentSpec{
		Name:     "WidgetApi",
		Language: "csharp",
		// BuildCommand/StartCommand are intentionally empty for C#: the template drives
		// `dotnet publish` and the ENTRYPOINT.
		Metadata: map[string]any{"buildContext": buildCtx},
	}
	artifacts, err := dg.GenerateDockerfile(spec)
	if err != nil {
		t.Fatalf("GenerateDockerfile: %v", err)
	}
	df := artifacts.Dockerfile
	if !strings.Contains(df, "mcr.microsoft.com/dotnet/sdk:9.0") {
		t.Errorf("expected the .NET SDK builder image; got:\n%s", df)
	}
	if !strings.Contains(df, "dotnet publish -c Release -o /app/publish") {
		t.Errorf("expected the dotnet publish step; got:\n%s", df)
	}
	if !strings.Contains(df, "mcr.microsoft.com/dotnet/aspnet:9.0-noble-chiseled") {
		t.Errorf("expected the chiseled ASP.NET runtime image; got:\n%s", df)
	}
	// Chiseled has no shell, so the ENTRYPOINT must reference the derived assembly name directly.
	if !strings.Contains(df, `ENTRYPOINT ["dotnet", "WidgetApi.dll"]`) {
		t.Errorf("expected the ENTRYPOINT to reference the derived assembly name; got:\n%s", df)
	}
	// .NET 8+ reads ASPNETCORE_HTTP_PORTS for the listen port.
	if !strings.Contains(df, "ENV ASPNETCORE_HTTP_PORTS=") {
		t.Errorf("expected ASPNETCORE_HTTP_PORTS to be set; got:\n%s", df)
	}
	// The chiseled final stage must NOT use RUN (no shell / no package manager).
	if idx := strings.Index(df, "9.0-noble-chiseled"); idx >= 0 {
		if strings.Contains(df[idx:], "RUN ") {
			t.Errorf("chiseled runtime stage must not contain a RUN instruction; got:\n%s", df)
		}
	}
	if artifacts.DockerIgnore == "" {
		t.Error("expected a csharp .dockerignore to be generated")
	}
}

func TestGenerateDockerfile_ElixirPhoenix(t *testing.T) {
	// Build context is a fresh empty dir so the user-Dockerfile reuse path can't interfere.
	buildCtx := t.TempDir()

	dg := NewDockerGeneratorWithBackend(io.Discard, []EnvVar{}, nil)
	spec := &DeploymentSpec{
		Name:         "widget_app",
		Language:     "elixir",
		BuildCommand: "mix release",
		StartCommand: "bin/server",
		Services:     []Service{{Type: "framework", Provider: "phoenix"}},
		Metadata:     map[string]any{"buildContext": buildCtx},
	}
	artifacts, err := dg.GenerateDockerfile(spec)
	if err != nil {
		t.Fatalf("GenerateDockerfile: %v", err)
	}
	df := artifacts.Dockerfile
	if !strings.Contains(df, "hexpm/elixir") {
		t.Errorf("expected the hexpm/elixir builder image; got:\n%s", df)
	}
	if !strings.Contains(df, "debian:bookworm-slim") {
		t.Errorf("expected the debian-slim runtime image; got:\n%s", df)
	}
	if !strings.Contains(df, "mix release") {
		t.Errorf("expected the `mix release` build step; got:\n%s", df)
	}
	// The app name (spec.Name) must flow into the release-copy path, since the release dir is
	// named after the OTP app.
	if !strings.Contains(df, "_build/prod/rel/widget_app") {
		t.Errorf("expected the app name to reach the release copy path; got:\n%s", df)
	}
	// Phoenix only starts its endpoint when PHX_SERVER is set.
	if !strings.Contains(df, "ENV PHX_SERVER=true") {
		t.Errorf("expected PHX_SERVER to be set for Phoenix; got:\n%s", df)
	}
	if !strings.Contains(df, "ENV PORT=") {
		t.Errorf("expected PORT to be set; got:\n%s", df)
	}
	// Phoenix asset pipeline should run for a Phoenix app.
	if !strings.Contains(df, "mix assets.deploy") {
		t.Errorf("expected mix assets.deploy for Phoenix; got:\n%s", df)
	}
	if !strings.Contains(df, `CMD ["/app/bin/server"]`) {
		t.Errorf("expected the Phoenix release start CMD; got:\n%s", df)
	}
	if artifacts.DockerIgnore == "" {
		t.Error("expected an elixir .dockerignore to be generated")
	}
}

func TestGenerateDockerfile_ElixirPlug(t *testing.T) {
	// A non-Phoenix Elixir app must skip the asset pipeline / PHX_SERVER and start by release name.
	buildCtx := t.TempDir()

	dg := NewDockerGeneratorWithBackend(io.Discard, []EnvVar{}, nil)
	spec := &DeploymentSpec{
		Name:         "thin",
		Language:     "elixir",
		BuildCommand: "mix release",
		Services:     []Service{{Type: "framework", Provider: "plug"}},
		Metadata:     map[string]any{"buildContext": buildCtx},
	}
	artifacts, err := dg.GenerateDockerfile(spec)
	if err != nil {
		t.Fatalf("GenerateDockerfile: %v", err)
	}
	df := artifacts.Dockerfile
	if strings.Contains(df, "mix assets.deploy") {
		t.Errorf("a non-Phoenix app should skip mix assets.deploy; got:\n%s", df)
	}
	if strings.Contains(df, "PHX_SERVER") {
		t.Errorf("a non-Phoenix app should not set PHX_SERVER; got:\n%s", df)
	}
	if !strings.Contains(df, `CMD ["/app/bin/thin", "start"]`) {
		t.Errorf("expected a plain-release start CMD by app name; got:\n%s", df)
	}
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
