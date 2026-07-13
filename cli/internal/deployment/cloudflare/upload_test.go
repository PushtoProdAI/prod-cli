package cloudflare

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// mockClient records the calls UploadDir makes so we can assert the protocol.
type mockClient struct {
	missing       []string // what check-missing reports as needing upload
	uploadedKeys  []string
	upsertedCount int
	manifest      map[string]string
	special       map[string][]byte
	tokenCalls    int
}

func (m *mockClient) ListProjects(context.Context) ([]Project, error) { return nil, nil }
func (m *mockClient) CreateProject(context.Context, string, string) (*Project, error) {
	return &Project{}, nil
}

func (m *mockClient) GetUploadToken(context.Context, string) (string, error) {
	m.tokenCalls++
	return "jwt-token", nil
}

func (m *mockClient) CheckMissing(_ context.Context, _ string, hashes []string) ([]string, error) {
	if m.missing != nil {
		return m.missing, nil
	}
	return hashes, nil // default: everything is missing
}

func (m *mockClient) UploadAssets(_ context.Context, _ string, batch []AssetUpload) error {
	for _, a := range batch {
		if !a.Base64 {
			panic("asset upload must set base64:true")
		}
		m.uploadedKeys = append(m.uploadedKeys, a.Key)
	}
	return nil
}

func (m *mockClient) UpsertHashes(_ context.Context, _ string, hashes []string) error {
	m.upsertedCount = len(hashes)
	return nil
}

func (m *mockClient) CreateDeployment(_ context.Context, _ string, manifest map[string]string, special map[string][]byte) (*Deployment, error) {
	m.manifest = manifest
	m.special = special
	return &Deployment{ID: "dep-1", URL: "https://abc.myproj.pages.dev"}, nil
}

func (m *mockClient) DeleteProject(context.Context, string) error { return nil }

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestUploadDir_ManifestAndAssets(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"index.html":     "<h1>hi</h1>",
		"assets/app.js":  "console.log(1)",
		"_redirects":     "/* /index.html 200", // special: excluded from assets, added to deployment
		"_worker.js":     "ignored",            // ignored entirely (Functions out of scope)
		"node_modules/x": "ignored",            // ignored dir
	})
	m := &mockClient{}
	dep, err := UploadDir(context.Background(), m, dir, "myproj", "main")
	if err != nil {
		t.Fatalf("UploadDir: %v", err)
	}
	if dep.URL == "" {
		t.Error("expected a deployment URL")
	}

	// Manifest keys must carry a leading slash and cover exactly the two real assets.
	got := make([]string, 0, len(m.manifest))
	for k := range m.manifest {
		got = append(got, k)
	}
	sort.Strings(got)
	want := []string{"/assets/app.js", "/index.html"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("manifest keys = %v, want %v (leading slash; no _redirects/_worker.js/node_modules)", got, want)
	}
	// _redirects is a special file → passed to CreateDeployment, not hashed as an asset.
	if _, ok := m.special["_redirects"]; !ok {
		t.Error("_redirects should be passed to the deployment as a special file")
	}
	// Both real assets uploaded (all were missing), and upsert covered both hashes.
	if len(m.uploadedKeys) != 2 {
		t.Errorf("uploaded %d assets, want 2", len(m.uploadedKeys))
	}
	if m.upsertedCount != 2 {
		t.Errorf("upsert-hashes count = %d, want 2 (all hashes)", m.upsertedCount)
	}
}

func TestUploadDir_SkipsAlreadyPresentAssets(t *testing.T) {
	dir := writeTree(t, map[string]string{"index.html": "x", "style.css": "y"})
	// Server already has everything → nothing uploaded, but the manifest + upsert still cover all.
	m := &mockClient{missing: []string{}}
	if _, err := UploadDir(context.Background(), m, dir, "p", "main"); err != nil {
		t.Fatalf("UploadDir: %v", err)
	}
	if len(m.uploadedKeys) != 0 {
		t.Errorf("uploaded %d assets, want 0 (all already present)", len(m.uploadedKeys))
	}
	if len(m.manifest) != 2 {
		t.Errorf("manifest has %d entries, want 2", len(m.manifest))
	}
}
