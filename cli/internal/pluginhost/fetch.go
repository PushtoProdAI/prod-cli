package pluginhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-errors/errors"
)

// maxPluginSize bounds a downloaded plugin binary — defense against a hostile release
// serving an enormous asset.
const maxPluginSize = 256 << 20 // 256 MB

// DownloadVerified downloads url to a temp file, verifies its sha256 against wantSHA (hex),
// and ONLY on a match moves it to dest (0700). On any mismatch, size overflow, or error the
// temp file is removed and dest is never created — so an unverified binary is never placed
// anywhere it could be executed. This is the security gate for installing a plugin from the
// network (ACE.4): the checksum is checked before the binary is ever runnable.
func DownloadVerified(ctx context.Context, url, wantSHA, dest string) error {
	wantSHA = strings.ToLower(strings.TrimSpace(wantSHA))
	if len(wantSHA) != 64 || !isHex(wantSHA) {
		return errors.Errorf("a 64-character hex sha256 is required to install from the network (got %q)", wantSHA)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 120 * time.Second}).Do(req)
	if err != nil {
		return errors.Errorf("failed to download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.Errorf("download %s returned HTTP %d", url, resp.StatusCode)
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".prod-plugin-dl-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once we rename it away; cleans up on every failure path

	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, maxPluginSize+1))
	closeErr := tmp.Close()
	if err != nil {
		return errors.Errorf("failed while downloading %s: %w", url, err)
	}
	if closeErr != nil {
		return closeErr
	}
	if n > maxPluginSize {
		return errors.Errorf("plugin download exceeds the %d-byte limit — refusing", maxPluginSize)
	}

	got := hex.EncodeToString(h.Sum(nil))
	if got != wantSHA {
		// Abort BEFORE the binary is placed where it could run.
		return errors.Errorf("checksum mismatch — refusing to install: downloaded sha256 %s, expected %s", got, wantSHA)
	}
	if err := os.Chmod(tmpPath, 0o700); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		return errors.Errorf("failed to place the verified plugin at %s: %w", dest, err)
	}
	return nil
}

func isHex(s string) bool {
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}
