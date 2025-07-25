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
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/jsonmessage"
)

type DockerArtifacts struct {
	Dockerfile    string
	DockerCompose string
	BuildContext  map[string]string
	ImageName     string
	Services      []DockerService
}

type DockerBuildResult struct {
	ImageName   string
	ImageID     string
	BuildOutput string
	Artifacts   *DockerArtifacts
}

type RegistryCredentials struct {
	Username   string `json:"dockerAuthUsername"`
	Token      string `json:"dockerAuthToken"`
	URL        string `json:"proxyEndpoint"`
	Repository string `json:"dockerRepo"`
	ExpiresAt  string `json:"expiresAt"`
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

//go:embed templates/*.dockerfile
var templateFS embed.FS

type DockerGenerator struct {
	templates map[string]*template.Template
	client    *client.Client
}

func NewDockerGenerator() *DockerGenerator {
	dg := &DockerGenerator{
		templates: make(map[string]*template.Template),
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

	// Parse and store templates
	dg.templates["node"] = template.Must(template.New("node").Parse(string(nodeTemplate)))
	dg.templates["nodejs"] = dg.templates["node"]
	dg.templates["javascript"] = dg.templates["node"]
	dg.templates["python"] = template.Must(template.New("python").Parse(string(pythonTemplate)))
	dg.templates["go"] = template.Must(template.New("go").Parse(string(goTemplate)))
	dg.templates["golang"] = dg.templates["go"]
}

func (dg *DockerGenerator) GenerateDockerfile(spec *DeploymentSpec) (*DockerArtifacts, error) {
	// Get the appropriate template for the language
	tmpl, exists := dg.templates[strings.ToLower(spec.Language)]
	if !exists {
		return nil, fmt.Errorf("unsupported language: %s", spec.Language)
	}

	// Prepare template data
	templateData := struct {
		Name         string
		Language     string
		BuildCommand string
		StartCommand string
		Port         int
	}{
		Name:         spec.Name,
		Language:     spec.Language,
		BuildCommand: spec.BuildCommand,
		StartCommand: spec.StartCommand,
		Port:         8080, // Default port, could be configurable
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

	return &DockerArtifacts{
		Dockerfile:    dockerfileBuf.String(),
		DockerCompose: dockerCompose,
		ImageName:     fmt.Sprintf("app-%s", spec.Name),
		Services:      services,
		BuildContext:  map[string]string{".": "."}, // Default build context
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
		PullParent:     false,
		NoCache:        true,
		SuppressOutput: false,
		Platform:       "linux/amd64", // Ensure we build for the correct platform
		BuildArgs: map[string]*string{
			"TARGETPLATFORM": stringPtr("linux/amd64"),
		},
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
	output := io.MultiWriter(os.Stdout, buildOutput)

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

	fmt.Printf("✓ Successfully built image: %s\n", artifacts.ImageName+":latest")

	return &DockerBuildResult{
		ImageName:   artifacts.ImageName + ":latest",
		ImageID:     imageID,
		BuildOutput: buildOutput.String(),
		Artifacts:   artifacts,
	}, nil
}

// GenerateAndBuild generates Dockerfile and builds the image in one step
func (dg *DockerGenerator) GenerateAndBuild(ctx context.Context, spec *DeploymentSpec, buildContext string) (*DockerBuildResult, error) {
	fmt.Printf("Generating Dockerfile for %s (%s)...\n", spec.Name, spec.Language)

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
		fmt.Printf("Building Docker image...\n")
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

// createTarFromDir creates a tar archive from a directory
func createTarFromDir(dir string) (io.ReadCloser, error) {
	buf := new(bytes.Buffer)
	tw := tar.NewWriter(buf)
	defer tw.Close()

	fileCount := 0
	err := filepath.Walk(dir, func(file string, fi os.FileInfo, err error) error {
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
func (dg *DockerGenerator) GetPushCredentials(ctx context.Context, tenantId string) (*RegistryCredentials, error) {
	// Prepare request payload
	payload := map[string]string{
		"tenantId": tenantId,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request payload: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", "http://127.0.0.1:54321/functions/v1/push-token", bytes.NewBuffer(payloadBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	// Make HTTP request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request to push-token endpoint: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("push-token endpoint returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Parse response
	var creds RegistryCredentials
	if err := json.NewDecoder(resp.Body).Decode(&creds); err != nil {
		return nil, fmt.Errorf("failed to decode push-token response: %w", err)
	}

	// Validate required fields
	if creds.Username == "" || creds.Token == "" || creds.URL == "" || creds.Repository == "" {
		return nil, fmt.Errorf("incomplete credentials received: username=%s, token present=%t, url=%s, repository=%s",
			creds.Username, creds.Token != "", creds.URL, creds.Repository)
	}

	// Strip https:// prefix as Docker doesn't accept it in image references
	creds.URL = strings.TrimPrefix(creds.URL, "https://")
	creds.URL = strings.TrimPrefix(creds.URL, "http://")

	return &creds, nil
}

// PushToRegistry tags and pushes a Docker image to a private registry
func (dg *DockerGenerator) PushToRegistry(ctx context.Context, buildResult *DockerBuildResult, creds *RegistryCredentials) (*DockerPushResult, error) {
	if dg.client == nil {
		return nil, fmt.Errorf("Docker client not available. Please ensure Docker is installed and running")
	}

	// Build the registry image tag in ECR format: registry/repository:tag
	registryImageTag := fmt.Sprintf("%s/%s:latest", strings.TrimSuffix(creds.URL, "/"), creds.Repository)

	// Ensure we have the correct source image name with tag
	sourceImage := buildResult.ImageName
	if !strings.Contains(sourceImage, ":") {
		sourceImage = sourceImage + ":latest"
	}

	fmt.Printf("Tagging image for registry...\n")

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

	fmt.Printf("Pushing image to registry...\n")

	// Push the image
	pushResponse, err := dg.client.ImagePush(ctx, registryImageTag, pushOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to push image to registry: %w", err)
	}
	defer pushResponse.Close()

	// Read push output and parse JSON messages
	pushOutput := &strings.Builder{}
	output := io.MultiWriter(os.Stdout, pushOutput)

	// Use the Docker SDK's jsonmessage package for proper parsing
	err = jsonmessage.DisplayJSONMessagesStream(pushResponse, output, 0, false, nil)
	if err != nil {
		return nil, fmt.Errorf("docker push failed: %w", err)
	}

	fmt.Printf("✓ Successfully pushed image: %s\n", registryImageTag)

	return &DockerPushResult{
		PushedImageURL: registryImageTag,
		PushOutput:     pushOutput.String(),
	}, nil
}

// GetPullCredentials fetches registry credentials from the pull-token endpoint
func (dg *DockerGenerator) GetPullCredentials(ctx context.Context, tenantId string) (*RegistryCredentials, error) {
	// Prepare request payload
	payload := map[string]string{
		"tenantId": tenantId,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request payload: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", "http://127.0.0.1:54321/functions/v1/pull-token", bytes.NewBuffer(payloadBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	// Make HTTP request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request to pull-token endpoint: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("pull-token endpoint returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Parse response
	var creds RegistryCredentials
	if err := json.NewDecoder(resp.Body).Decode(&creds); err != nil {
		return nil, fmt.Errorf("failed to decode pull-token response: %w", err)
	}

	// Validate required fields
	if creds.Username == "" || creds.Token == "" || creds.URL == "" || creds.Repository == "" {
		return nil, fmt.Errorf("incomplete credentials received: username=%s, token present=%t, url=%s, repository=%s",
			creds.Username, creds.Token != "", creds.URL, creds.Repository)
	}

	// Strip https:// prefix as Docker doesn't accept it in image references
	creds.URL = strings.TrimPrefix(creds.URL, "https://")
	creds.URL = strings.TrimPrefix(creds.URL, "http://")

	return &creds, nil
}

// BuildAndPush is a convenience method that generates, builds, and pushes a Docker image in one step
func (dg *DockerGenerator) BuildAndPush(ctx context.Context, spec *DeploymentSpec, buildContext string, tenantId string) (*DockerBuildResult, *DockerPushResult, error) {
	fmt.Printf("Starting Docker build and push for %s...\n", spec.Name)

	// Build the image first
	buildResult, err := dg.GenerateAndBuild(ctx, spec, buildContext)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build image: %w", err)
	}

	// If Docker is not available, we can't push
	if dg.client == nil {
		return buildResult, nil, fmt.Errorf("Docker client not available - cannot push to registry")
	}

	// Get push credentials
	creds, err := dg.GetPushCredentials(ctx, tenantId)
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

// stringPtr returns a pointer to a string
func stringPtr(s string) *string {
	return &s
}
