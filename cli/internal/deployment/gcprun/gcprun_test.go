package gcprun

import (
	"testing"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
)

func TestBuildService(t *testing.T) {
	svc := buildService(ServiceConfig{
		Name:     "app",
		ImageRef: "us-central1-docker.pkg.dev/p/prod/app:1720000000",
		Port:     8080,
		CPU:      "1000m",
		Memory:   "512Mi",
		EnvVars:  map[string]string{"FOO": "bar"},
	})

	if svc.Template == nil || len(svc.Template.Containers) != 1 {
		t.Fatalf("expected one container, got %+v", svc.Template)
	}
	c := svc.Template.Containers[0]
	if c.Image != "us-central1-docker.pkg.dev/p/prod/app:1720000000" {
		t.Errorf("image = %q", c.Image)
	}
	if len(c.Ports) != 1 || c.Ports[0].ContainerPort != 8080 {
		t.Errorf("ports = %+v", c.Ports)
	}
	if c.Resources.Limits["cpu"] != "1000m" || c.Resources.Limits["memory"] != "512Mi" {
		t.Errorf("resources = %+v", c.Resources.Limits)
	}
	found := false
	for _, e := range c.Env {
		if e.Name == "FOO" && e.Value == "bar" {
			found = true
		}
	}
	if !found {
		t.Errorf("env missing FOO=bar: %+v", c.Env)
	}
}

func TestEnvMapForcesPort(t *testing.T) {
	m := envMap([]deployment.EnvVar{
		{Name: "FOO", Value: "bar"},
		{Name: "SECRET", Value: "s", Sensitive: true},
	})
	if m["FOO"] != "bar" {
		t.Errorf("FOO = %q", m["FOO"])
	}
	if m["PORT"] != "8080" {
		t.Errorf("PORT should be forced to the container port, got %q", m["PORT"])
	}
	if m["SECRET"] != "s" {
		t.Errorf("SECRET should be present (plain env for v1), got %q", m["SECRET"])
	}
}

func TestServicePath(t *testing.T) {
	d := &Deployer{project: "my-proj", region: "us-central1"}
	want := "projects/my-proj/locations/us-central1/services/app"
	if got := d.ServicePath("app"); got != want {
		t.Errorf("ServicePath = %q, want %q", got, want)
	}
}
