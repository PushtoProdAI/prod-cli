package pluginhost

import (
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestInstallInspectManifestRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a plugin binary")
	}
	bin := filepath.Join(t.TempDir(), "prod-provider-example")
	build := exec.Command("go", "build", "-o", bin, "github.com/pushtoprodai/prod-cli/examples/prod-provider-example")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Skipf("cannot build the sample plugin: %v", err)
	}

	// Checksum + inspect (what `prod plugin install` does): launch the plugin and
	// learn its identity.
	sum, err := ChecksumFile(bin)
	if err != nil || len(sum) != 64 {
		t.Fatalf("ChecksumFile = %q, %v", sum, err)
	}
	cb, _ := hex.DecodeString(sum)
	meta, err := Inspect(bin, cb)
	if err != nil || meta.Name != "Example" {
		t.Fatalf("Inspect = %+v, %v", meta, err)
	}

	// Manifest upsert → load → remove.
	mp := DefaultManifestPath(t.TempDir())
	entry := Entry{Name: meta.Name, Aliases: meta.Aliases, DomainSuffix: meta.DomainSuffix, Path: bin, Checksum: sum}
	if err := Upsert(mp, entry); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	// Upsert is idempotent by name.
	if err := Upsert(mp, entry); err != nil {
		t.Fatalf("Upsert (again): %v", err)
	}
	entries, err := LoadManifest(mp)
	if err != nil || len(entries) != 1 || entries[0].Name != "Example" {
		t.Fatalf("LoadManifest = %+v, %v", entries, err)
	}

	removed, err := Remove(mp, "Example")
	if err != nil || !removed {
		t.Fatalf("Remove = %v, %v", removed, err)
	}
	if entries, _ := LoadManifest(mp); len(entries) != 0 {
		t.Errorf("manifest should be empty after remove, got %+v", entries)
	}
	if removed, _ := Remove(mp, "Example"); removed {
		t.Error("removing an absent plugin should report not-removed")
	}
}
