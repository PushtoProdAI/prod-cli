package run

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildEnvOverrides(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, ".env.ci")
	if err := os.WriteFile(f, []byte("# comment\nFOO=from-file\nBAR=\"quoted\"\n\nBAZ=base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := buildEnvOverrides(f, []string{"FOO=from-flag", "NEW=x"})
	if err != nil {
		t.Fatal(err)
	}
	if m["FOO"] != "from-flag" {
		t.Errorf("--env must win over --env-file, got %q", m["FOO"])
	}
	if m["BAR"] != "quoted" {
		t.Errorf("quotes should be stripped, got %q", m["BAR"])
	}
	if m["BAZ"] != "base" || m["NEW"] != "x" {
		t.Errorf("missing keys: %v", m)
	}
	if _, err := buildEnvOverrides("", []string{"NOEQUALS"}); err == nil {
		t.Error("--env without = should error")
	}
	if _, err := buildEnvOverrides("/does/not/exist", nil); err == nil {
		t.Error("missing --env-file should error")
	}
}
