package deployscmd

import (
	"strings"
	"testing"
	"time"

	"github.com/pushtoprodai/prod-cli/internal/deploytarget"
)

func TestRelAge(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		d    time.Duration
		want string
	}{
		{10 * time.Second, "just now"},
		{5 * time.Minute, "5m"},
		{3 * time.Hour, "3h"},
		{50 * time.Hour, "2d"},
	} {
		if got := relAge(now.Add(-c.d)); got != c.want {
			t.Errorf("relAge(-%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestTruncAndGlyph(t *testing.T) {
	if trunc("short", 20) != "short" {
		t.Error("short names untouched")
	}
	if got := trunc("a-very-long-application-name", 10); got != "a-very-lo…" || !strings.HasSuffix(got, "…") {
		t.Errorf("trunc = %q, want a-very-lo…", got)
	}
	if statusGlyph("success") == "success" || statusGlyph("weird") != "weird" {
		t.Error("glyph mapping wrong")
	}
}

// A no-URL record must render a clear marker in the URL column, never a blank/broken cell.
func TestURLCell(t *testing.T) {
	for _, c := range []struct{ url, shape, want string }{
		{"https://x.fly.dev", "web", "https://x.fly.dev"},
		{"", "worker", "worker"},
		{"", "cron", "cron"},
		{"", "", "—"},                        // legacy shapeless no-URL record
		{"https://x", "worker", "https://x"}, // a real URL always wins
	} {
		if got := urlCell(c.url, c.shape); got != c.want {
			t.Errorf("urlCell(%q,%q) = %q, want %q", c.url, c.shape, got, c.want)
		}
	}
}

// `prod open` on a URL-less worker must not error: it opens the console and explains why. With
// no console link it prints a helpful pointer instead of failing. Web deploys open their URL.
func TestOpenPlan(t *testing.T) {
	web := deploytarget.Target{Name: "web", Shape: "web", LiveURL: "https://web.fly.dev", ConsoleURL: "https://fly.io/apps/web"}
	act, err := openPlan(web, false)
	if err != nil || act.open != "https://web.fly.dev" || act.message != "" {
		t.Errorf("web open = %+v err=%v, want open the live URL silently", act, err)
	}

	worker := deploytarget.Target{Name: "cronjob", Shape: "worker", ConsoleURL: "https://fly.io/apps/cronjob"}
	act, err = openPlan(worker, false)
	if err != nil {
		t.Fatalf("worker open should not error: %v", err)
	}
	if act.open != "https://fly.io/apps/cronjob" {
		t.Errorf("worker open should fall back to the console, got %q", act.open)
	}
	if !strings.Contains(act.message, "no public URL (it's a worker)") || !strings.Contains(act.message, "Opening the console") {
		t.Errorf("worker message unclear: %q", act.message)
	}

	// A worker with no console link at all: helpful message, no error, nothing to open.
	bare := deploytarget.Target{Name: "cronjob", Shape: "worker"}
	act, err = openPlan(bare, false)
	if err != nil {
		t.Fatalf("bare worker open should not error: %v", err)
	}
	if act.open != "" {
		t.Errorf("nothing to open, got %q", act.open)
	}
	if !strings.Contains(act.message, "prod logs cronjob") {
		t.Errorf("bare worker should point at logs: %q", act.message)
	}

	// --console explicitly asked, but none recorded: this may error (the user asked for a
	// specific thing that doesn't exist).
	if _, err := openPlan(deploytarget.Target{Name: "x"}, true); err == nil {
		t.Error("open --console with no console URL should error")
	}
}
