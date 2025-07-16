package deployment

import (
	"fmt"
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

type DockerGenerator struct{}

func NewDockerGenerator() *DockerGenerator {
	return &DockerGenerator{}
}

func (dg *DockerGenerator) GenerateDockerfile(spec *DeploymentSpec) (*DockerArtifacts, error) {
	// TODO: implement using go templates
	return &DockerArtifacts{
		ImageName: fmt.Sprintf("app-%s", spec.ProjectID),
		Services:  []DockerService{},
	}, nil
}