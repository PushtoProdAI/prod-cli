package pluginhost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ACE.4: a github download is refused unless the checksum matches, and a mismatch must
// abort BEFORE the binary is placed anywhere it could run.
func TestDownloadVerified(t *testing.T) {
	payload := []byte("fake plugin binary contents")
	sum := sha256.Sum256(payload)
	hexSum := hex.EncodeToString(sum[:])
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(payload) }))
	defer srv.Close()
	dir := t.TempDir()

	// correct checksum → installed + content matches
	dest := filepath.Join(dir, "prod-provider-ok")
	if err := DownloadVerified(context.Background(), srv.URL, hexSum, dest); err != nil {
		t.Fatalf("verified download failed: %v", err)
	}
	if got, _ := os.ReadFile(dest); !bytes.Equal(got, payload) {
		t.Error("installed content does not match")
	}

	// wrong checksum → error AND the destination must not exist
	badDest := filepath.Join(dir, "prod-provider-bad")
	if err := DownloadVerified(context.Background(), srv.URL, strings.Repeat("a", 64), badDest); err == nil {
		t.Error("a checksum mismatch must error")
	}
	if _, err := os.Stat(badDest); !os.IsNotExist(err) {
		t.Error("a checksum mismatch must NOT create the destination binary")
	}

	// malformed checksum → rejected outright
	if err := DownloadVerified(context.Background(), srv.URL, "not-a-real-sha", filepath.Join(dir, "x")); err == nil {
		t.Error("a malformed checksum must be rejected")
	}
	// no leftover temp files in the dir (only the one good binary)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".prod-plugin-dl-") {
			t.Errorf("leftover temp download file: %s", e.Name())
		}
	}
}
