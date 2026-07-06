package pluginhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"

	"github.com/go-errors/errors"
	"github.com/pushtoprodai/prod-cli/pkg/plugin"
)

// ChecksumFile returns the hex-encoded sha256 of a file — recorded at install so the
// binary is verified at every launch.
func ChecksumFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", errors.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", errors.Errorf("hash %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Inspect launches a plugin, reads its metadata, and shuts it down — used by
// `prod plugin install` to learn the plugin's identity (and prove it's a valid,
// protocol-compatible provider) before recording it.
func Inspect(path string, checksum []byte) (plugin.Meta, error) {
	prov, closeFn, err := Launch(path, checksum)
	if err != nil {
		return plugin.Meta{}, err
	}
	defer closeFn()
	return prov.Metadata(context.Background())
}

// Upsert adds a plugin entry to the manifest at path, replacing any existing entry
// with the same name.
func Upsert(path string, e Entry) error {
	entries, err := LoadManifest(path)
	if err != nil {
		return err
	}
	for i := range entries {
		if entries[i].Name == e.Name {
			entries[i] = e
			return SaveManifest(path, entries)
		}
	}
	return SaveManifest(path, append(entries, e))
}

// Remove deletes the plugin entry named name from the manifest at path, reporting
// whether it existed.
func Remove(path, name string) (bool, error) {
	entries, err := LoadManifest(path)
	if err != nil {
		return false, err
	}
	kept := make([]Entry, 0, len(entries))
	found := false
	for _, e := range entries {
		if e.Name == name {
			found = true
			continue
		}
		kept = append(kept, e)
	}
	if !found {
		return false, nil
	}
	return true, SaveManifest(path, kept)
}
