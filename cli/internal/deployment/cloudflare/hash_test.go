package cloudflare

import (
	"encoding/base64"
	"encoding/hex"
	"testing"

	"github.com/zeebo/blake3"
)

// The blake3 library must be genuine blake3 — the official empty-input known-answer vector.
func TestBlake3KnownAnswer(t *testing.T) {
	sum := blake3.Sum256(nil)
	const want = "af1349b9f5f9a1a6a0404dea36dcc9499bcb25c9adc112b7cc9a93cae41f3262"
	if got := hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("blake3(\"\") = %s, want %s (wrong hash library!)", got, want)
	}
}

// HashFile must compose exactly base64(TEXT) + ext(no dot) → blake3 → first 32 hex. Rebuild the
// expected value from the raw primitives so a regression in ANY step (hashing raw bytes instead
// of base64, keeping the dot, wrong truncation) is caught.
func TestHashFileRecipe(t *testing.T) {
	contents := []byte("hello world")
	b64 := base64.StdEncoding.EncodeToString(contents)
	sum := blake3.Sum256([]byte(b64 + "html")) // ext "html", no dot
	want := hex.EncodeToString(sum[:16])

	if got := HashFile(contents, "index.html"); got != want {
		t.Errorf("HashFile = %s, want %s", got, want)
	}
	if got := HashFile(contents, "index.html"); len(got) != 32 {
		t.Errorf("hash length = %d, want 32 hex chars", len(got))
	}
	// Hashing the raw bytes (not base64) would give a different value — guard against it.
	rawSum := blake3.Sum256(append(contents, []byte("html")...))
	if HashFile(contents, "index.html") == hex.EncodeToString(rawSum[:16]) {
		t.Error("HashFile hashed raw bytes; it must hash the base64 text")
	}
}

func TestNodeExtWithoutDot(t *testing.T) {
	cases := map[string]string{
		"index.html":        "html",
		"assets/app.js":     "js",
		"a.b.c":             "c",
		".config.json":      "json", // dot after the first counts
		".gitignore":        "",     // leading-dot dotfile: NO extension (Node semantics)
		"noext":             "",
		"dir/.env":          "",
		"path/to/style.css": "css",
	}
	for path, want := range cases {
		if got := nodeExtWithoutDot(path); got != want {
			t.Errorf("nodeExtWithoutDot(%q) = %q, want %q", path, got, want)
		}
	}
}
