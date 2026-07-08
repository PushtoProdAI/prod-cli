package analyzer

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// Regression: an ignore token must match a directory NAME, not a substring of the path.
// Previously ignoreDirs was checked with strings.Contains(path, ignore), so a project living
// under a path that merely contained "tmp"/"log" (e.g. Ubuntu CI's /tmp/... temp dirs) had its
// entire tree skipped — env/route scanning returned nothing.
func TestWalkersMatchIgnoreDirByName(t *testing.T) {
	root := t.TempDir()
	// A source file under a dir whose name CONTAINS "tmp" but isn't exactly "tmp": must be scanned.
	kept := filepath.Join(root, "tmphelpers")
	if err := os.MkdirAll(kept, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kept, "a.rb"), []byte(`x = ENV["KEEP_ME"]`), 0o644); err != nil {
		t.Fatal(err)
	}
	// A file under a dir named EXACTLY "tmp": must be skipped.
	skipped := filepath.Join(root, "tmp")
	if err := os.MkdirAll(skipped, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skipped, "b.rb"), []byte(`y = ENV["SKIP_ME"]`), 0o644); err != nil {
		t.Fatal(err)
	}

	fsys := projectFS{rootPath: root}
	cands, err := walkProjectForCandidates(fsys, []string{".rb"}, []string{"tmp"}, regexp.MustCompile(`ENV\["([A-Z_][A-Z0-9_]*)"\]`), 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, c := range cands {
		names[c.VarName] = true
	}
	if !names["KEEP_ME"] {
		t.Error("a file under tmphelpers/ (name contains but != an ignore token) must be scanned")
	}
	if names["SKIP_ME"] {
		t.Error("a file under a dir named exactly tmp/ must be skipped")
	}
}
