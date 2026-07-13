package pluginhost

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestManifestRoundTrip(t *testing.T) {
	path := DefaultManifestPath(t.TempDir())

	// A missing manifest is not an error — no plugins installed.
	if entries, err := LoadManifest(path); err != nil || entries != nil {
		t.Fatalf("missing manifest: entries=%v err=%v", entries, err)
	}

	want := []Entry{{
		Name: "Acme", Aliases: []string{"acme"}, DomainSuffix: ".acme.app",
		SupportsRollback: true, Shapes: []string{"worker", "mcp-server"},
		Path: "/usr/local/bin/prod-provider-acme", Checksum: "abc123",
	}}
	if err := SaveManifest(path, want); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	got, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", got, want)
	}
}

// TestManifestShapesBackCompat proves an existing manifest with no `shapes` field decodes
// to a nil Shapes (⇒ the host treats the plugin as web-only) — so no migration is needed.
func TestManifestShapesBackCompat(t *testing.T) {
	path := DefaultManifestPath(t.TempDir())
	legacy := `[{"name":"Old","aliases":["old"],"path":"/x/prod-provider-old","checksum":"abc"}]`
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if len(got) != 1 || got[0].Shapes != nil {
		t.Errorf("legacy manifest Shapes = %v, want nil", got[0].Shapes)
	}
}
