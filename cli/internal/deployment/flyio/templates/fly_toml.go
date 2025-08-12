package templates

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/meroxa/prod/cli/internal/deployment"
)

// FlyioTemplateGenerator generates fly.toml configuration files
type FlyioTemplateGenerator struct{}

// FlyioTemplateData represents the data used in fly.toml templates
type FlyioTemplateData struct {
	AppName       string
	PrimaryRegion string
	Dockerfile    string
	Builder       string
	BuildCmd      string
	StartCmd      string
	InternalPort  int
	EnvVars       map[string]string
	Services      []ServiceTemplateData
	Volumes       []VolumeTemplateData
}

// ServiceTemplateData represents service configuration in fly.toml
type ServiceTemplateData struct {
	Protocol     string
	InternalPort int
	Ports        []PortTemplateData
}

// PortTemplateData represents port configuration in fly.toml
type PortTemplateData struct {
	Port     int
	Handlers []string
}

// VolumeTemplateData represents volume configuration in fly.toml
type VolumeTemplateData struct {
	Name   string
	Size   int
	Region string
}

// GenerateFlyToml generates a fly.toml configuration file
func (ftg *FlyioTemplateGenerator) GenerateFlyToml(spec *deployment.DeploymentSpec) (string, error) {
	data := ftg.buildTemplateData(spec)

	tmpl, err := template.New("fly.toml").Parse(flyTomlTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	var result strings.Builder
	err = tmpl.Execute(&result, data)
	if err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return result.String(), nil
}

// buildTemplateData builds the template data from deployment spec
func (ftg *FlyioTemplateGenerator) buildTemplateData(spec *deployment.DeploymentSpec) *FlyioTemplateData {
	data := &FlyioTemplateData{
		AppName:       spec.Name,
		PrimaryRegion: "iad", // Default to IAD
		InternalPort:  ftg.getInternalPortForLanguage(spec.Language),
		EnvVars:       make(map[string]string),
		Services:      []ServiceTemplateData{},
		Volumes:       []VolumeTemplateData{},
	}

	// Set build configuration
	if spec.BuildCommand != "" {
		data.BuildCmd = spec.BuildCommand
	}
	if spec.StartCommand != "" {
		data.StartCmd = spec.StartCommand
	}

	// Set builder based on language
	data.Builder = ftg.getBuilderForLanguage(spec.Language)

	// Add service configuration
	data.Services = append(data.Services, ServiceTemplateData{
		Protocol:     "tcp",
		InternalPort: data.InternalPort,
		Ports: []PortTemplateData{
			{Port: 80, Handlers: []string{"http"}},
			{Port: 443, Handlers: []string{"tls", "http"}},
		},
	})

	// Add environment variables for backing services
	for _, service := range spec.Services {
		switch service.Provider {
		case "postgresql":
			data.EnvVars["DATABASE_URL"] = fmt.Sprintf("postgres://postgres:password@%s-postgres.internal:5432/%s", spec.Name, spec.Name)
		case "redis":
			data.EnvVars["REDIS_URL"] = fmt.Sprintf("redis://%s-redis.internal:6379", spec.Name)
		case "volume":
			data.Volumes = append(data.Volumes, VolumeTemplateData{
				Name:   fmt.Sprintf("%s-volume", spec.Name),
				Size:   10, // Default 10GB
				Region: data.PrimaryRegion,
			})
		}
	}

	return data
}

// getBuilderForLanguage returns the appropriate builder for the given language
func (ftg *FlyioTemplateGenerator) getBuilderForLanguage(language string) string {
	switch language {
	case "python":
		return "python"
	case "node", "nodejs", "javascript":
		return "node"
	case "go", "golang":
		return "go"
	case "ruby":
		return "ruby"
	case "php":
		return "php"
	case "rust":
		return "rust"
	default:
		return "docker"
	}
}

// getInternalPortForLanguage returns the default internal port for the given language
func (ftg *FlyioTemplateGenerator) getInternalPortForLanguage(language string) int {
	switch language {
	case "python":
		return 8000
	case "node", "nodejs", "javascript":
		return 3000
	case "go", "golang":
		return 8080
	case "ruby":
		return 3000
	case "php":
		return 8000
	case "rust":
		return 8080
	default:
		return 8080
	}
}

// flyTomlTemplate is the template for fly.toml configuration
const flyTomlTemplate = `# fly.toml
app = "{{.AppName}}"
primary_region = "{{.PrimaryRegion}}"

{{if .Builder}}
[build]
  {{if eq .Builder "docker"}}
  dockerfile = "Dockerfile"
  {{else}}
  builder = "{{.Builder}}"
  {{end}}
  {{if .BuildCmd}}
  build_cmd = "{{.BuildCmd}}"
  {{end}}
  {{if .StartCmd}}
  start_cmd = "{{.StartCmd}}"
  {{end}}
{{end}}

{{if .EnvVars}}
[env]
{{range $key, $value := .EnvVars}}
  {{$key}} = "{{$value}}"
{{end}}
{{end}}

{{range .Services}}
[[services]]
  protocol = "{{.Protocol}}"
  internal_port = {{.InternalPort}}
  
  {{range .Ports}}
  [[services.ports]]
    port = {{.Port}}
    handlers = [{{range $i, $handler := .Handlers}}{{if $i}}, {{end}}"{{$handler}}"{{end}}]
  {{end}}
{{end}}

{{range .Volumes}}
[[mounts]]
  source = "{{.Name}}"
  destination = "/data"
{{end}}
`
