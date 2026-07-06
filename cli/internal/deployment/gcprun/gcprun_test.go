package gcprun

import (
	"testing"

	run "google.golang.org/api/run/v2"
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

func TestServicePath(t *testing.T) {
	d := &Deployer{project: "my-proj", region: "us-central1"}
	want := "projects/my-proj/locations/us-central1/services/app"
	if got := d.ServicePath("app"); got != want {
		t.Errorf("ServicePath = %q, want %q", got, want)
	}
}

func revReady(name, create string) *run.GoogleCloudRunV2Revision {
	return &run.GoogleCloudRunV2Revision{
		Name:       "projects/p/locations/us-central1/services/app/revisions/" + name,
		CreateTime: create,
		Conditions: []*run.GoogleCloudRunV2Condition{{Type: "Ready", State: "CONDITION_SUCCEEDED"}},
	}
}

func revFailed(name, create string) *run.GoogleCloudRunV2Revision {
	r := revReady(name, create)
	r.Conditions[0].State = "CONDITION_FAILED"
	return r
}

func TestServingAndPreviousRevision(t *testing.T) {
	revs := []*run.GoogleCloudRunV2Revision{
		revReady("app-00003", "2026-07-03T10:00:00Z"),
		revReady("app-00002", "2026-07-02T10:00:00Z"),
		revReady("app-00001", "2026-07-01T10:00:00Z"),
	}

	// Default (no explicit pin): serving = newest ready; previous = the one before.
	serving := servingRevision(nil, revs)
	if serving != "app-00003" {
		t.Errorf("servingRevision(default) = %q, want app-00003", serving)
	}
	if got := previousReadyRevision(revs, serving); got != "app-00002" {
		t.Errorf("previousReadyRevision = %q, want app-00002", got)
	}

	// After a rollback the service pins a revision — serving is that pin, and a further
	// rollback walks back again (the naive second-newest could never do this).
	pin := []*run.GoogleCloudRunV2TrafficTarget{{Revision: "app-00002", Percent: 100, Type: trafficToRevision}}
	if s := servingRevision(pin, revs); s != "app-00002" {
		t.Errorf("servingRevision(pinned) = %q, want app-00002", s)
	}
	if got := previousReadyRevision(revs, "app-00002"); got != "app-00001" {
		t.Errorf("walk-back previousReadyRevision = %q, want app-00001", got)
	}

	// A failed latest deploy is skipped: serving = newest READY, not the broken one.
	withFailed := append([]*run.GoogleCloudRunV2Revision{revFailed("app-00004", "2026-07-04T10:00:00Z")}, revs...)
	if s := servingRevision(nil, withFailed); s != "app-00003" {
		t.Errorf("servingRevision(failed latest) = %q, want app-00003", s)
	}

	// Nothing older to roll back to.
	if got := previousReadyRevision(revs[2:], "app-00001"); got != "" {
		t.Errorf("previousReadyRevision(only one) = %q, want empty", got)
	}
}
