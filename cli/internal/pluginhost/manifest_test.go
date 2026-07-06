package pluginhost

import (
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
		SupportsRollback: true, Path: "/usr/local/bin/prod-provider-acme", Checksum: "abc123",
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
