package cloudflare

import (
	"context"
	"testing"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
)

// destroySpy records whether DeleteProject was called and with what name.
type destroySpy struct {
	mockClient
	deleted string
}

func (s *destroySpy) DeleteProject(_ context.Context, name string) error {
	s.deleted = name
	return nil
}

func TestDestroyDeletesProject(t *testing.T) {
	spy := &destroySpy{}
	spec := &deployment.DeploymentSpec{Name: "My App"}
	d := NewCloudflareQueuedDeployment(spy, spec, nil)

	if err := d.Destroy(context.Background()); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if spy.deleted != "my-app" {
		t.Errorf("deleted project = %q, want %q (sanitized)", spy.deleted, "my-app")
	}
}

func TestDeployRefusesNonStatic(t *testing.T) {
	spec := &deployment.DeploymentSpec{Name: "app", IsStatic: false}
	d := NewCloudflareQueuedDeployment(&mockClient{}, spec, nil)
	if _, err := d.Deploy(context.Background()); err == nil {
		t.Error("Deploy should refuse a non-static project on Cloudflare Pages")
	}
}

func TestSatisfiesDestroyer(t *testing.T) {
	var _ deployment.Destroyer = (*CloudflareQueuedDeployment)(nil)
}

func TestSanitizeProjectName(t *testing.T) {
	cases := map[string]string{
		"My App":            "my-app",
		"app_pr-7":          "app-pr-7",
		"  Weird!!Name  ":   "weird-name",
		"--leading-trail--": "leading-trail",
		"":                  "prod-app",
	}
	for in, want := range cases {
		if got := SanitizeProjectName(in); got != want {
			t.Errorf("SanitizeProjectName(%q) = %q, want %q", in, got, want)
		}
	}
}
