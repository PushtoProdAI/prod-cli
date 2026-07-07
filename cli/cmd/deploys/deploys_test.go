package deployscmd

import (
	"strings"
	"testing"
	"time"
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
