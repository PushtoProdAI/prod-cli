package registry

import (
	"context"
	"testing"

	"golang.org/x/oauth2"
)

type mockEnsurer struct{ err error }

func (m mockEnsurer) ensureDockerRepo(_ context.Context, _, _, _ string) error { return m.err }

type staticToken string

func (s staticToken) Token() (*oauth2.Token, error) {
	return &oauth2.Token{AccessToken: string(s)}, nil
}

type errToken struct{}

func (errToken) Token() (*oauth2.Token, error) { return nil, context.DeadlineExceeded }

func garFixture() *garRegistry {
	return &garRegistry{
		ensurer: mockEnsurer{},
		ts:      staticToken("tok123"),
		project: "my-proj",
		region:  "us-central1",
		repo:    "prod",
	}
}

func TestGARRef(t *testing.T) {
	r := garFixture()
	got, err := r.Ref("My-App", "1720000000")
	if err != nil {
		t.Fatalf("Ref: %v", err)
	}
	want := "us-central1-docker.pkg.dev/my-proj/prod/my-app:1720000000"
	if got != want {
		t.Errorf("Ref = %q, want %q", got, want)
	}

	if _, err := r.Ref("bad name!", "1"); err == nil {
		t.Error("expected an error for an invalid project name")
	}
	if _, err := r.Ref("app", "bad tag!"); err == nil {
		t.Error("expected an error for an invalid tag")
	}
}

func TestGARCredentials(t *testing.T) {
	r := garFixture()
	c, err := r.Credentials(context.Background(), "My-App")
	if err != nil {
		t.Fatalf("Credentials: %v", err)
	}
	if c.URL != "us-central1-docker.pkg.dev" || c.AuthServer != c.URL {
		t.Errorf("host = %q/%q", c.URL, c.AuthServer)
	}
	if c.Repository != "my-proj/prod/my-app" {
		t.Errorf("Repository = %q, want my-proj/prod/my-app", c.Repository)
	}
	if c.Username != "oauth2accesstoken" {
		t.Errorf("Username = %q, want oauth2accesstoken", c.Username)
	}
	if c.Token != "tok123" {
		t.Errorf("Token = %q, want tok123", c.Token)
	}
	// token never leaks via String()
	if s := c.String(); contains(s, "tok123") {
		t.Errorf("String() leaked the token: %s", s)
	}
}

func TestGARCredentialsErrors(t *testing.T) {
	// ensure-repo failure surfaces
	r := garFixture()
	r.ensurer = mockEnsurer{err: context.DeadlineExceeded}
	if _, err := r.Credentials(context.Background(), "app"); err == nil {
		t.Error("expected ensure-repo error to surface")
	}
	// token failure surfaces
	r = garFixture()
	r.ts = errToken{}
	if _, err := r.Credentials(context.Background(), "app"); err == nil {
		t.Error("expected token error to surface")
	}
	// invalid project name
	r = garFixture()
	if _, err := r.Credentials(context.Background(), "bad name!"); err == nil {
		t.Error("expected invalid-project error")
	}
}

func TestGARName(t *testing.T) {
	if garFixture().Name() != "gar" {
		t.Error("Name should be gar")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
