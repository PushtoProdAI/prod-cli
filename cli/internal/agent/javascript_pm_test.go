package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectNodePackageManager(t *testing.T) {
	cases := []struct{ lockfile, wantMgr string }{
		{"pnpm-lock.yaml", "pnpm"},
		{"yarn.lock", "yarn"},
		{"bun.lockb", "bun"},
		{"bun.lock", "bun"},
		{"package-lock.json", "npm"},
	}
	for _, c := range cases {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, c.lockfile), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if mgr, _ := detectNodePackageManager(dir); mgr != c.wantMgr {
			t.Errorf("%s → %q, want %q", c.lockfile, mgr, c.wantMgr)
		}
	}
	// No lockfile → npm (the default).
	if mgr, _ := detectNodePackageManager(t.TempDir()); mgr != "npm" {
		t.Errorf("no lockfile → %q, want npm", mgr)
	}
	// pnpm wins over a stray package-lock.json (workspaces often have both).
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "pnpm-lock.yaml"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "package-lock.json"), []byte("x"), 0o644)
	if mgr, _ := detectNodePackageManager(dir); mgr != "pnpm" {
		t.Errorf("pnpm-lock + package-lock → %q, want pnpm", mgr)
	}
}

func TestFirstLines(t *testing.T) {
	out := []byte("\n\n  npm error Cannot read properties of null (reading 'matches')\nnpm error A complete log...\n")
	if got := firstLines(out, 1); got != "npm error Cannot read properties of null (reading 'matches')" {
		t.Errorf("firstLines = %q", got)
	}
	if got := firstLines([]byte("a\nb\nc\nd"), 2); got != "a | b" {
		t.Errorf("firstLines(2) = %q", got)
	}
}
