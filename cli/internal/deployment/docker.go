package deployment

import (
	"archive/tar"
	"bytes"
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/jsonmessage"
	"github.com/meroxa/prod/cli/internal/backend"
	"github.com/meroxa/prod/cli/internal/output"
)

type DockerArtifacts struct {
	Dockerfile      string
	DockerIgnore    string
	DockerCompose   string
	AdditionalFiles map[string]string
	ImageName       string
	Services        []DockerService
}

type DockerBuildResult struct {
	ImageName   string
	ImageID     string
	BuildOutput string
	Artifacts   *DockerArtifacts
}

type DockerPushResult struct {
	PushedImageURL string
	PushOutput     string
}

type DockerService struct {
	Name        string
	Image       string
	Environment map[string]string
	Ports       []string
	Volumes     []string
	DependsOn   []string
}

//go:embed templates/*.dockerfile templates/*.dockerignore
var templateFS embed.FS

type DockerGenerator struct {
	dockerignoreTemplates map[string]string
	templates             map[string]*template.Template
	baseImages            map[string]string
	client                *client.Client
	beClient              *backend.Client
	writer                io.Writer
	envVars               []EnvVar // Environment variables to pass as build args
}

func NewDockerGenerator(writer io.Writer, envVars []EnvVar) *DockerGenerator {
	if writer == nil {
		writer = output.NewNoOpWriter()
	}
	dg := &DockerGenerator{
		templates:             make(map[string]*template.Template),
		dockerignoreTemplates: make(map[string]string),
		beClient:              backend.NewClient(),
		writer:                writer,
		envVars:               envVars,
	}
	dg.initTemplates()

	// Initialize Docker client if available
	if IsDockerAvailable() {
		cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
		if err == nil {
			dg.client = cli
		}
	}

	return dg
}

func (dg *DockerGenerator) initTemplates() {
	// Load templates from embedded files
	nodeTemplate, err := templateFS.ReadFile("templates/node.dockerfile")
	if err != nil {
		panic(fmt.Sprintf("failed to read node template: %v", err))
	}

	pythonTemplate, err := templateFS.ReadFile("templates/python.dockerfile")
	if err != nil {
		panic(fmt.Sprintf("failed to read python template: %v", err))
	}

	goTemplate, err := templateFS.ReadFile("templates/go.dockerfile")
	if err != nil {
		panic(fmt.Sprintf("failed to read go template: %v", err))
	}

	// get the base images we will use in the templates
	images, err := backend.NewClient().GetBaseDockerImages(context.Background())
	if err != nil {
		slog.Info("could not fetch base images from backend, using defaults", "error", err)
		images = make(map[string]string)
	}
	dg.baseImages = images
	// Parse and store templates
	dg.templates["node"] = template.Must(template.New("node").Parse(string(nodeTemplate)))
	dg.templates["nodejs"] = dg.templates["node"]
	dg.templates["javascript"] = dg.templates["node"]
	funcMap := template.FuncMap{
		"contains": strings.Contains,
		"and":      func(a, b bool) bool { return a && b },
	}
	dg.templates["python"] = template.Must(template.New("python").Funcs(funcMap).Parse(string(pythonTemplate)))
	dg.templates["go"] = template.Must(template.New("go").Parse(string(goTemplate)))
	dg.templates["golang"] = dg.templates["go"]

	nodeDockerignore, err := templateFS.ReadFile("templates/node.dockerignore")
	if err != nil {
		panic(fmt.Sprintf("failed to read node dockerignore: %v", err))
	}
	dg.dockerignoreTemplates["node"] = string(nodeDockerignore)
	dg.dockerignoreTemplates["nodejs"] = string(nodeDockerignore)
	dg.dockerignoreTemplates["javascript"] = string(nodeDockerignore)

	pythonDockerignore, err := templateFS.ReadFile("templates/python.dockerignore")
	if err != nil {
		panic(fmt.Sprintf("failed to read python dockerignore: %v", err))
	}
	dg.dockerignoreTemplates["python"] = string(pythonDockerignore)

	goDockerignore, err := templateFS.ReadFile("templates/go.dockerignore")
	if err != nil {
		panic(fmt.Sprintf("failed to read go dockerignore: %v", err))
	}
	dg.dockerignoreTemplates["go"] = string(goDockerignore)
	dg.dockerignoreTemplates["golang"] = string(goDockerignore)
}

func (dg *DockerGenerator) GenerateDockerfile(spec *DeploymentSpec) (*DockerArtifacts, error) {
	// Get the appropriate template for the language
	tmpl, exists := dg.templates[strings.ToLower(spec.Language)]
	if !exists {
		return nil, fmt.Errorf("unsupported language: %s", spec.Language)
	}
	baseImage, hasBaseImage := dg.baseImages[strings.ToLower(spec.Language)]
	if !hasBaseImage {
		baseImage = ""
	}

	slog.Info("Using base image", "language", spec.Language, "baseImage", baseImage)

	// Prepare template data
	templateData := struct {
		Name             string
		Language         string
		BuildCommand     string
		StartCommand     string
		Port             int
		BaseImage        string
		HasStartupScript bool
		OutputDir        string
		IsStatic         bool
		EnvVars          []EnvVar
	}{
		Name:             spec.Name,
		Language:         spec.Language,
		BuildCommand:     spec.BuildCommand,
		StartCommand:     spec.StartCommand,
		Port:             8080, // Default port, could be configurable
		BaseImage:        baseImage,
		HasStartupScript: strings.ToLower(spec.Language) == "python",
		OutputDir:        spec.OutputDir,
		IsStatic:         spec.IsStatic,
		EnvVars:          dg.envVars,
	}
	// Execute template
	var dockerfileBuf bytes.Buffer
	if err := tmpl.Execute(&dockerfileBuf, templateData); err != nil {
		return nil, fmt.Errorf("failed to execute Dockerfile template: %w", err)
	}

	// Generate Docker services for the deployment
	services := dg.generateServices(spec)

	// Generate Docker Compose if we have services
	var dockerCompose string
	if len(services) > 0 {
		var err error
		dockerCompose, err = dg.generateDockerCompose(spec, services)
		if err != nil {
			return nil, fmt.Errorf("failed to generate Docker Compose: %w", err)
		}
	}

	// Generate .dockerignore for the language
	dockerIgnore := dg.generateDockerIgnore(spec.Language)

	// Initialize additional files for build context
	additionalFiles := make(map[string]string)

	// Add startup script for Python
	if strings.ToLower(spec.Language) == "python" {
		additionalFiles["start.sh"] = dg.generatePythonStartupScript()
	}

	return &DockerArtifacts{
		Dockerfile:      dockerfileBuf.String(),
		DockerIgnore:    dockerIgnore,
		DockerCompose:   dockerCompose,
		ImageName:       fmt.Sprintf("app-%s", spec.Name),
		Services:        services,
		AdditionalFiles: additionalFiles,
	}, nil
}

func (dg *DockerGenerator) generateServices(spec *DeploymentSpec) []DockerService {
	var services []DockerService

	// Add the main application service
	appService := DockerService{
		Name:        spec.Name,
		Image:       fmt.Sprintf("app-%s", spec.Name),
		Environment: make(map[string]string),
		Ports:       []string{"8080:8080"},
		Volumes:     []string{},
		DependsOn:   []string{},
	}

	// Add backing services based on the deployment spec
	var backingServices []DockerService
	for _, service := range spec.Services {
		dockerService := dg.createDockerService(service)
		if dockerService != nil {
			backingServices = append(backingServices, *dockerService)

			// Add dependency to main app service
			appService.DependsOn = append(appService.DependsOn, dockerService.Name)

			// Add connection string environment variable
			envVar := dg.getConnectionEnvVar(service.Provider)
			if envVar != "" {
				appService.Environment[envVar] = dg.getConnectionString(service.Provider, dockerService.Name)
			}
		}
	}

	// Add app service first, then backing services
	services = append(services, appService)
	services = append(services, backingServices...)

	return services
}

func (dg *DockerGenerator) createDockerService(service Service) *DockerService {
	switch service.Provider {
	case "postgresql":
		return &DockerService{
			Name:  fmt.Sprintf("%s-postgres", service.Name),
			Image: "postgres:15-alpine",
			Environment: map[string]string{
				"POSTGRES_DB":       service.Name,
				"POSTGRES_USER":     "postgres",
				"POSTGRES_PASSWORD": "postgres",
			},
			Ports:   []string{"5432:5432"},
			Volumes: []string{fmt.Sprintf("%s-postgres-data:/var/lib/postgresql/data", service.Name)},
		}
	case "redis":
		return &DockerService{
			Name:    fmt.Sprintf("%s-redis", service.Name),
			Image:   "redis:7-alpine",
			Ports:   []string{"6379:6379"},
			Volumes: []string{fmt.Sprintf("%s-redis-data:/data", service.Name)},
		}
	default:
		return nil
	}
}

func (dg *DockerGenerator) getConnectionEnvVar(provider string) string {
	switch provider {
	case "postgresql":
		return "DATABASE_URL"
	case "redis":
		return "REDIS_URL"
	default:
		return ""
	}
}

func (dg *DockerGenerator) getConnectionString(provider, serviceName string) string {
	switch provider {
	case "postgresql":
		return fmt.Sprintf("postgresql://postgres:postgres@%s:5432/postgres", serviceName)
	case "redis":
		return fmt.Sprintf("redis://%s:6379", serviceName)
	default:
		return ""
	}
}

func (dg *DockerGenerator) generateDockerCompose(spec *DeploymentSpec, services []DockerService) (string, error) {
	composeTemplate := `version: '3.8'

services:
{{- range .Services }}
  {{ .Name }}:
    {{- if eq .Name $.AppName }}
    build: .
    {{- else }}
    image: {{ .Image }}
    {{- end }}
    {{- if .Environment }}
    environment:
      {{- range $key, $value := .Environment }}
      {{ $key }}: {{ $value }}
      {{- end }}
    {{- end }}
    {{- if .Ports }}
    ports:
      {{- range .Ports }}
      - "{{ . }}"
      {{- end }}
    {{- end }}
    {{- if .Volumes }}
    volumes:
      {{- range .Volumes }}
      - {{ . }}
      {{- end }}
    {{- end }}
    {{- if .DependsOn }}
    depends_on:
      {{- range .DependsOn }}
      - {{ . }}
      {{- end }}
    {{- end }}
{{- end }}

{{- if .Volumes }}
volumes:
  {{- range .Volumes }}
  {{ . }}:
  {{- end }}
{{- end }}`

	// Parse template
	tmpl, err := template.New("docker-compose").Parse(composeTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse docker-compose template: %w", err)
	}

	// Extract volume names from all services
	volumeSet := make(map[string]bool)
	for _, service := range services {
		for _, volume := range service.Volumes {
			// Extract volume name (before the colon)
			parts := strings.Split(volume, ":")
			if len(parts) > 1 {
				volumeSet[parts[0]] = true
			}
		}
	}

	// Convert volume set to slice
	var volumes []string
	for volume := range volumeSet {
		volumes = append(volumes, volume)
	}

	// Prepare template data
	templateData := struct {
		Services []DockerService
		AppName  string
		Volumes  []string
	}{
		Services: services,
		AppName:  spec.Name,
		Volumes:  volumes,
	}

	// Execute template
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, templateData); err != nil {
		return "", fmt.Errorf("failed to execute docker-compose template: %w", err)
	}

	return buf.String(), nil
}

// IsDockerAvailable checks if Docker is available on the system
func IsDockerAvailable() bool {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return false
	}
	defer cli.Close()

	_, err = cli.Ping(context.Background())
	return err == nil
}

// BuildImage builds a Docker image from pre-generated artifacts
func (dg *DockerGenerator) BuildImage(ctx context.Context, artifacts *DockerArtifacts, buildContext string) (*DockerBuildResult, error) {
	if dg.client == nil {
		return nil, fmt.Errorf("docker client not available. Please ensure Docker is installed and running")
	}

	// Validate build context exists
	if _, err := os.Stat(buildContext); os.IsNotExist(err) {
		return nil, fmt.Errorf("build context directory does not exist: %s", buildContext)
	}

	// Write Dockerfile to build context
	dockerfilePath := filepath.Join(buildContext, "Dockerfile")
	if err := os.WriteFile(dockerfilePath, []byte(artifacts.Dockerfile), 0644); err != nil {
		return nil, fmt.Errorf("failed to write Dockerfile: %w", err)
	}

	// Write .dockerignore if provided
	if artifacts.DockerIgnore != "" {
		dockerignorePath := filepath.Join(buildContext, ".dockerignore")
		if err := os.WriteFile(dockerignorePath, []byte(artifacts.DockerIgnore), 0644); err != nil {
			return nil, fmt.Errorf("failed to write .dockerignore: %w", err)
		}
	}

	// Write additional files to build context
	for filename, content := range artifacts.AdditionalFiles {
		filePath := filepath.Join(buildContext, filename)
		if err := os.WriteFile(filePath, []byte(content), 0755); err != nil { // 0755 for executable files like start.sh
			return nil, fmt.Errorf("failed to write additional file %s: %w", filename, err)
		}
	}

	// Create tar archive of build context
	buildContextTar, err := createTarFromDir(buildContext)
	if err != nil {
		return nil, fmt.Errorf("failed to create build context tar: %w", err)
	}
	defer buildContextTar.Close()

	// Build options
	buildOptions := build.ImageBuildOptions{
		Tags:           []string{artifacts.ImageName + ":latest"},
		Dockerfile:     "Dockerfile",
		Remove:         true,
		ForceRemove:    true,
		PullParent:     true,
		NoCache:        false,
		SuppressOutput: false,
		Platform:       "linux/amd64", // Target platform for deployment
		BuildArgs:      dg.prepareBuildArgs(),
	}

	// Build the image
	buildResponse, err := dg.client.ImageBuild(ctx, buildContextTar, buildOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to build Docker image: %w", err)
	}
	defer buildResponse.Body.Close()

	// Read build output and extract image ID
	buildOutput := &strings.Builder{}
	var imageID string

	// Create a custom writer to capture output
	output := io.MultiWriter(dg.writer, buildOutput)

	// Create auxCallback to capture the built image ID
	auxCallback := func(msg jsonmessage.JSONMessage) {
		if msg.Aux != nil {
			var result struct {
				ID string `json:"ID"`
			}
			if err := json.Unmarshal(*msg.Aux, &result); err == nil && result.ID != "" {
				imageID = result.ID
			}
		}
	}

	// Use the Docker SDK's jsonmessage package for proper parsing
	err = jsonmessage.DisplayJSONMessagesStream(buildResponse.Body, output, 0, false, auxCallback)
	if err != nil {
		return nil, fmt.Errorf("docker build failed: %w", err)
	}

	// If we didn't get an image ID from aux messages, try to inspect the image
	if imageID == "" {
		imageInfo, err := dg.client.ImageInspect(ctx, artifacts.ImageName+":latest")
		if err != nil {
			// If we can't inspect, just use the tag as fallback
			imageID = artifacts.ImageName + ":latest"
		} else {
			imageID = imageInfo.ID
		}
	}

	fmt.Fprintf(dg.writer, "✓ Successfully built image: %s\n", artifacts.ImageName+":latest")

	return &DockerBuildResult{
		ImageName:   artifacts.ImageName + ":latest",
		ImageID:     imageID,
		BuildOutput: buildOutput.String(),
		Artifacts:   artifacts,
	}, nil
}

// GenerateAndBuild generates Dockerfile and builds the image in one step
func (dg *DockerGenerator) GenerateAndBuild(ctx context.Context, spec *DeploymentSpec, buildContext string) (*DockerBuildResult, error) {
	fmt.Fprintf(dg.writer, "Generating Dockerfile for %s (%s)...\n", spec.Name, spec.Language)

	// Generate artifacts first
	artifacts, err := dg.GenerateDockerfile(spec)
	if err != nil {
		return nil, fmt.Errorf("failed to generate Dockerfile: %w", err)
	}

	// Write docker-compose.yml if services are present (DISABLED)
	// if len(artifacts.Services) > 1 { // More than just the app service
	// 	composeFile := filepath.Join(buildContext, "docker-compose.yml")
	// 	if err := os.WriteFile(composeFile, []byte(artifacts.DockerCompose), 0644); err != nil {
	// 		return nil, fmt.Errorf("failed to write docker-compose.yml: %w", err)
	// 	}
	// 	fmt.Printf("Generated docker-compose.yml with %d services\n", len(artifacts.Services))
	// }

	// If Docker is available, build the image
	if dg.client != nil {
		fmt.Fprintf(dg.writer, "Building Docker image...\n")
		return dg.BuildImage(ctx, artifacts, buildContext)
	}

	// If Docker is not available, just return the artifacts
	return &DockerBuildResult{
		ImageName:   artifacts.ImageName,
		Artifacts:   artifacts,
		BuildOutput: "Docker not available - Dockerfile generated successfully",
	}, nil
}

// Close closes the Docker client connection
func (dg *DockerGenerator) Close() error {
	if dg.client != nil {
		return dg.client.Close()
	}
	return nil
}

// parseDockerIgnore parses a .dockerignore file and returns the patterns
func parseDockerIgnore(dockerignorePath string) ([]string, error) {
	var patterns []string

	// Check if .dockerignore exists
	if _, err := os.Stat(dockerignorePath); os.IsNotExist(err) {
		return patterns, nil // No .dockerignore file, return empty patterns
	}

	content, err := os.ReadFile(dockerignorePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read .dockerignore: %w", err)
	}

	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}

	return patterns, nil
}

// matchDockerIgnorePattern checks if a path matches a .dockerignore pattern
func matchDockerIgnorePattern(path string, pattern string) bool {
	// Handle negation patterns (starting with !)
	if strings.HasPrefix(pattern, "!") {
		return false // We'll handle negation in the caller
	}

	// Convert pattern to filepath pattern
	pattern = strings.ReplaceAll(pattern, "**", "*")

	// If pattern ends with /, it only matches directories
	if strings.HasSuffix(pattern, "/") {
		pattern = strings.TrimSuffix(pattern, "/")
		// For directory patterns, check if path starts with pattern
		return strings.HasPrefix(path, pattern+"/") || path == pattern
	}

	// Try exact match first
	if matched, _ := filepath.Match(pattern, path); matched {
		return true
	}

	// Try matching any part of the path
	pathParts := strings.Split(path, "/")
	for i := range pathParts {
		subPath := strings.Join(pathParts[i:], "/")
		if matched, _ := filepath.Match(pattern, subPath); matched {
			return true
		}
		// Also try matching just the filename
		if matched, _ := filepath.Match(pattern, pathParts[i]); matched {
			return true
		}
	}

	return false
}

// shouldIgnoreFile determines if a file should be ignored based on .dockerignore patterns
func shouldIgnoreFile(relPath string, patterns []string) bool {
	ignored := false

	for _, pattern := range patterns {
		if strings.HasPrefix(pattern, "!") {
			// Negation pattern - if it matches, don't ignore
			negPattern := strings.TrimPrefix(pattern, "!")
			if matchDockerIgnorePattern(relPath, negPattern) {
				ignored = false
			}
		} else {
			// Normal pattern - if it matches, ignore
			if matchDockerIgnorePattern(relPath, pattern) {
				ignored = true
			}
		}
	}

	return ignored
}

// createTarFromDir creates a tar archive from a directory
func createTarFromDir(dir string) (io.ReadCloser, error) {
	buf := new(bytes.Buffer)
	tw := tar.NewWriter(buf)
	defer tw.Close()

	// Parse .dockerignore file if it exists
	dockerignorePath := filepath.Join(dir, ".dockerignore")
	ignorePatterns, err := parseDockerIgnore(dockerignorePath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse .dockerignore: %w", err)
	}

	fileCount := 0
	err = filepath.Walk(dir, func(file string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if fi.IsDir() {
			return nil
		}

		// Create tar header
		relPath, err := filepath.Rel(dir, file)
		if err != nil {
			return err
		}

		// Check if file should be ignored based on .dockerignore patterns
		if shouldIgnoreFile(relPath, ignorePatterns) {
			return nil // Skip this file
		}

		fileCount++

		header, err := tar.FileInfoHeader(fi, "")
		if err != nil {
			return err
		}
		header.Name = relPath

		// Write header
		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		// Write file content
		f, err := os.Open(file)
		if err != nil {
			return err
		}

		_, copyErr := io.Copy(tw, f)
		f.Close() // Close immediately, don't defer in loop

		if copyErr != nil {
			return copyErr
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return io.NopCloser(bytes.NewReader(buf.Bytes())), nil
}

// GetPushCredentials fetches registry credentials from the push-token endpoint
func (dg *DockerGenerator) GetPushCredentials(ctx context.Context, authToken, projectName string) (*backend.RegistryCredentials, error) {
	return dg.beClient.GetPushRegistryCredentials(ctx, authToken, projectName)
}

// PushToRegistry tags and pushes a Docker image to a private registry
func (dg *DockerGenerator) PushToRegistry(ctx context.Context, buildResult *DockerBuildResult, creds *backend.RegistryCredentials) (*DockerPushResult, error) {
	if dg.client == nil {
		return nil, fmt.Errorf("docker client not available. Please ensure Docker is installed and running")
	}

	// Build the registry image tag in ECR format: registry/repository:tag
	registryImageTag := fmt.Sprintf("%s/%s:latest", strings.TrimSuffix(creds.URL, "/"), creds.Repository)

	// Ensure we have the correct source image name with tag
	sourceImage := buildResult.ImageName
	if !strings.Contains(sourceImage, ":") {
		sourceImage = sourceImage + ":latest"
	}

	fmt.Fprintf(dg.writer, "Tagging image for registry...\n")

	// Tag the image for the registry
	err := dg.client.ImageTag(ctx, sourceImage, registryImageTag)
	if err != nil {
		return nil, fmt.Errorf("failed to tag image for registry: %w", err)
	}

	// Create authentication config
	authConfig := registry.AuthConfig{
		Username:      creds.Username,
		Password:      creds.Token,
		ServerAddress: creds.URL,
	}

	// Encode authentication
	authConfigBytes, err := json.Marshal(authConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal auth config: %w", err)
	}
	authStr := base64.URLEncoding.EncodeToString(authConfigBytes)

	// Push options
	pushOptions := image.PushOptions{
		RegistryAuth: authStr,
	}

	fmt.Fprintf(dg.writer, "Pushing image to registry...\n")

	// Push the image
	pushResponse, err := dg.client.ImagePush(ctx, registryImageTag, pushOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to push image to registry: %w", err)
	}
	defer pushResponse.Close()

	// Read push output and parse JSON messages
	pushOutput := &strings.Builder{}
	output := io.MultiWriter(dg.writer, pushOutput)

	// Use the Docker SDK's jsonmessage package for proper parsing
	err = jsonmessage.DisplayJSONMessagesStream(pushResponse, output, 0, false, nil)
	if err != nil {
		return nil, fmt.Errorf("docker push failed: %w", err)
	}

	fmt.Fprintf(dg.writer, "✓ Successfully pushed image: %s\n", registryImageTag)

	return &DockerPushResult{
		PushedImageURL: registryImageTag,
		PushOutput:     pushOutput.String(),
	}, nil
}

// GetPullCredentials fetches registry credentials from the pull-token endpoint
func (dg *DockerGenerator) GetPullCredentials(ctx context.Context, authToken string, projectName string) (*backend.RegistryCredentials, error) {
	return dg.beClient.GetPullRegistryCredentials(ctx, authToken, projectName)
}

// BuildAndPush is a convenience method that generates, builds, and pushes a Docker image in one step
func (dg *DockerGenerator) BuildAndPush(ctx context.Context, spec *DeploymentSpec, buildContext string, authToken string) (*DockerBuildResult, *DockerPushResult, error) {
	fmt.Fprintf(dg.writer, "Starting Docker build and push for %s...\n", spec.Name)

	// Build the image first
	buildResult, err := dg.GenerateAndBuild(ctx, spec, buildContext)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build image: %w", err)
	}

	// If Docker is not available, we can't push
	if dg.client == nil {
		return buildResult, nil, fmt.Errorf("docker client not available - cannot push to registry")
	}

	// Get push credentials
	creds, err := dg.GetPushCredentials(ctx, authToken, spec.Name)
	if err != nil {
		return buildResult, nil, fmt.Errorf("failed to get push credentials: %w", err)
	}

	// Push to registry
	pushResult, err := dg.PushToRegistry(ctx, buildResult, creds)
	if err != nil {
		return buildResult, nil, fmt.Errorf("failed to push to registry: %w", err)
	}

	return buildResult, pushResult, nil
}

// generatePythonStartupScript creates the startup script for Python applications
func (dg *DockerGenerator) generatePythonStartupScript() string {
	return `#!/bin/bash

# Get PORT from environment, default to 8000
export PORT=${PORT:-8000}
export HOST=${HOST:-0.0.0.0}

# Original command passed as arguments
ORIGINAL_CMD="$*"

# Detect and modify common Python server commands
if [[ "$ORIGINAL_CMD" =~ uvicorn ]]; then
    # Handle uvicorn (FastAPI)
    # Remove any existing --host or --port arguments and add our own
    MODIFIED_CMD=$(echo "$ORIGINAL_CMD" | sed -E 's/--host[= ][^ ]*//' | sed -E 's/--port[= ][^ ]*//')
    exec $MODIFIED_CMD --host $HOST --port $PORT
    
elif [[ "$ORIGINAL_CMD" =~ gunicorn ]]; then
    # Handle gunicorn
    # Remove any existing --bind arguments and add our own  
    MODIFIED_CMD=$(echo "$ORIGINAL_CMD" | sed -E 's/--bind[= ][^ ]*//' | sed -E 's/-b[= ][^ ]*//')
    exec $MODIFIED_CMD --bind $HOST:$PORT
    
elif [[ "$ORIGINAL_CMD" =~ "manage.py runserver" ]]; then
    # Handle Django runserver
    # Remove any existing host:port argument and add our own
    MODIFIED_CMD=$(echo "$ORIGINAL_CMD" | sed -E 's/(runserver) [0-9.]+:[0-9]+/\1/' | sed -E 's/(runserver) [0-9]+/\1/')
    exec $MODIFIED_CMD $HOST:$PORT
    
else
    # For other cases (like Flask app.run()), set environment variables and run as-is
    export FLASK_RUN_HOST=$HOST
    export FLASK_RUN_PORT=$PORT
    exec $ORIGINAL_CMD
fi`
}

// prepareBuildArgs converts environment variables to Docker build arguments
func (dg *DockerGenerator) prepareBuildArgs() map[string]*string {
	buildArgs := map[string]*string{
		"TARGETPLATFORM": stringPtr("linux/amd64"),
	}

	// Add environment variables as build args (for import.meta.env support)
	for _, envVar := range dg.envVars {
		if envVar.Value != "" {
			buildArgs[envVar.Name] = stringPtr(envVar.Value)
		}
	}

	return buildArgs
}

// stringPtr returns a pointer to a string
func stringPtr(s string) *string {
	return &s
}

// generateDockerIgnore returns the appropriate dockerignore content for the language
func (dg *DockerGenerator) generateDockerIgnore(language string) string {
	// Try to get language-specific dockerignore
	if dockerignore, exists := dg.dockerignoreTemplates[strings.ToLower(language)]; exists {
		return dockerignore
	}

	// Return a default dockerignore if language not found
	return `# Version control
.git
.gitignore

# Documentation
README.md
*.md

# IDEs
.idea
.vscode
*.swp
*.swo
*~

# OS
.DS_Store
Thumbs.db

# CI/CD
.github
.gitlab-ci.yml
.travis.yml
.circleci
`
}
