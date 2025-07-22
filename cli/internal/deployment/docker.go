package deployment

import (
	"bytes"
	"embed"
	"fmt"
	"strings"
	"text/template"
)

type DockerArtifacts struct {
	Dockerfile    string
	DockerCompose string
	BuildContext  map[string]string
	ImageName     string
	Services      []DockerService
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
}

func NewDockerGenerator() *DockerGenerator {
	dg := &DockerGenerator{
		templates: make(map[string]*template.Template),
	}
	dg.initTemplates()
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
