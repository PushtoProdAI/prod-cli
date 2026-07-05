package tui

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestClassifyLog(t *testing.T) {
	cases := []struct {
		in   string
		want logKind
	}{
		// real failures still color red
		{"❌ deploy failed", logError},
		{"failed to create postgres", logError},
		{"Error: connection refused", logError},
		// negated phrasings must NOT be red
		{"0 errors", logDefault},
		{"Build finished with no errors", logDefault},
		{"completed without errors", logDefault},
		// negated "error" but a real "failed" on the same line → still an error
		{"Tests: 0 errors, 3 failed", logError},
		// success + warning
		{"✅ Deployed — https://x.fly.dev", logSuccess},
		{"deployment successful", logSuccess},
		{"⚠️ heads up", logWarning},
		// plain
		{"Detecting deployment platforms...", logDefault},
	}
	for _, c := range cases {
		if got := classifyLog(c.in); got != c.want {
			t.Errorf("classifyLog(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestDefaultHistoryFileIsPrivate(t *testing.T) {
	p := defaultHistoryFile()
	if p == "" {
		return // no home dir resolvable in this env; degrades to no history
	}
	if strings.HasPrefix(p, "/tmp") || strings.Contains(p, "prodcli_app_history") {
		t.Errorf("history must not live in a shared /tmp path, got %q", p)
	}
	if filepath.Base(p) != "history" || filepath.Base(filepath.Dir(p)) != ".prod" {
		t.Errorf("history should be ~/.prod/history, got %q", p)
	}
}
