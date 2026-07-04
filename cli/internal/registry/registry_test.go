package registry

import (
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestFromEnvSelectsAndConfigures(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr bool
		wantRef string // Ref("my-app", "t")
	}{
		{
			name:    "dockerhub defaults namespace to username",
			env:     map[string]string{"PROD_REGISTRY_USERNAME": "alice", "PROD_REGISTRY_TOKEN": "tok"},
			wantRef: "docker.io/alice/my-app:t",
		},
		{
			name:    "dockerhub explicit namespace, lowercased",
			env:     map[string]string{"PROD_REGISTRY": "dockerhub", "PROD_REGISTRY_NAMESPACE": "Acme", "PROD_REGISTRY_USERNAME": "alice", "PROD_REGISTRY_TOKEN": "tok"},
			wantRef: "docker.io/acme/my-app:t",
		},
		{
			name:    "ghcr",
			env:     map[string]string{"PROD_REGISTRY": "ghcr", "PROD_REGISTRY_NAMESPACE": "acme", "PROD_REGISTRY_USERNAME": "alice", "PROD_REGISTRY_TOKEN": "tok"},
			wantRef: "ghcr.io/acme/my-app:t",
		},
		{
			name:    "generic strips scheme and path",
			env:     map[string]string{"PROD_REGISTRY": "generic", "PROD_REGISTRY_HOST": "https://registry.gitlab.com/ignored", "PROD_REGISTRY_NAMESPACE": "grp", "PROD_REGISTRY_USERNAME": "alice", "PROD_REGISTRY_TOKEN": "tok"},
			wantRef: "registry.gitlab.com/grp/my-app:t",
		},
		{name: "missing creds errors", env: map[string]string{"PROD_REGISTRY": "dockerhub"}, wantErr: true},
		{name: "ghcr without namespace errors", env: map[string]string{"PROD_REGISTRY": "ghcr", "PROD_REGISTRY_USERNAME": "alice", "PROD_REGISTRY_TOKEN": "tok"}, wantErr: true},
		{name: "generic without host errors", env: map[string]string{"PROD_REGISTRY": "generic", "PROD_REGISTRY_NAMESPACE": "grp", "PROD_REGISTRY_USERNAME": "alice", "PROD_REGISTRY_TOKEN": "tok"}, wantErr: true},
		{name: "unknown kind errors", env: map[string]string{"PROD_REGISTRY": "nope", "PROD_REGISTRY_USERNAME": "alice", "PROD_REGISTRY_TOKEN": "tok"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := FromEnv(env(tt.env))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got registry %v", r)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			ref, err := r.Ref("my-app", "t")
			if err != nil {
				t.Fatalf("Ref error: %v", err)
			}
			if ref != tt.wantRef {
				t.Errorf("Ref = %q, want %q", ref, tt.wantRef)
			}
		})
	}
}

// Invalid project names must be rejected, not turned into malformed refs.
func TestProjectNameValidation(t *testing.T) {
	r := mustDockerhub(t)

	// normalized: uppercase -> lowercase, still valid
	if ref, err := r.Ref("MyApp", "t"); err != nil || ref != "docker.io/alice/myapp:t" {
		t.Errorf("MyApp: ref=%q err=%v, want docker.io/alice/myapp:t", ref, err)
	}

	for _, bad := range []string{"", "app:latest", "foo/bar", "my app", "app@sha256:x", "-lead", "trail-"} {
		if _, err := r.Credentials(bad); err == nil {
			t.Errorf("Credentials(%q) should be rejected", bad)
		}
		if _, err := r.Ref(bad, "t"); err == nil {
			t.Errorf("Ref(%q) should be rejected", bad)
		}
	}
}

func TestInvalidTagRejected(t *testing.T) {
	r := mustDockerhub(t)
	for _, bad := range []string{"", "bad tag", "no:colon", strings.Repeat("x", 200)} {
		if _, err := r.Ref("my-app", bad); err == nil {
			t.Errorf("Ref with tag %q should be rejected", bad)
		}
	}
}

func TestCredentials(t *testing.T) {
	cases := []struct {
		kind, ns, wantURL, wantAuth, wantRepo string
	}{
		{"dockerhub", "", "docker.io", "https://index.docker.io/v1/", "alice/my-app"},
		{"ghcr", "acme", "ghcr.io", "ghcr.io", "acme/my-app"},
	}
	for _, c := range cases {
		t.Run(c.kind, func(t *testing.T) {
			r, err := FromEnv(env(map[string]string{
				"PROD_REGISTRY": c.kind, "PROD_REGISTRY_NAMESPACE": c.ns,
				"PROD_REGISTRY_USERNAME": "alice", "PROD_REGISTRY_TOKEN": "secret",
			}))
			if err != nil {
				t.Fatal(err)
			}
			cr, err := r.Credentials("my-app")
			if err != nil {
				t.Fatal(err)
			}
			if cr.URL != c.wantURL || cr.AuthServer != c.wantAuth || cr.Repository != c.wantRepo || cr.Token != "secret" {
				t.Errorf("unexpected credentials for %s", c.kind) // deliberately not printing cr (would risk leaking token)
			}
		})
	}
}

// The token must not be trimmed (a valid secret could carry edge whitespace) and
// must not leak through String()/%v.
func TestTokenHandling(t *testing.T) {
	r, err := FromEnv(env(map[string]string{
		"PROD_REGISTRY_USERNAME": "alice", "PROD_REGISTRY_TOKEN": " secret-with-space ",
	}))
	if err != nil {
		t.Fatal(err)
	}
	cr, _ := r.Credentials("my-app")
	if cr.Token != " secret-with-space " {
		t.Errorf("token was altered: %q", cr.Token)
	}
	if s := cr.String(); strings.Contains(s, "secret") || !strings.Contains(s, "<redacted>") {
		t.Errorf("String() leaked the token: %q", s)
	}
}

func mustDockerhub(t *testing.T) Registry {
	t.Helper()
	r, err := FromEnv(env(map[string]string{
		"PROD_REGISTRY_USERNAME": "alice", "PROD_REGISTRY_TOKEN": "tok",
	}))
	if err != nil {
		t.Fatal(err)
	}
	return r
}
