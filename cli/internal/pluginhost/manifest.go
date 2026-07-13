package pluginhost

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/go-errors/errors"
)

// Entry is one installed plugin in the manifest: the plugin's metadata (recorded at
// install time so startup needn't launch it) plus the binary path and sha256 checksum.
type Entry struct {
	Name             string   `json:"name"`
	Aliases          []string `json:"aliases,omitempty"`
	DomainSuffix     string   `json:"domainSuffix,omitempty"`
	SupportsRollback bool     `json:"supportsRollback,omitempty"`
	// Shapes are the plugin's declared deploy shapes (web/mcp-server/worker/cron),
	// recorded at install so the host knows a plugin may return a URL-less worker/agent
	// without launching it. Absent ⇒ web-only (an existing manifest needs no migration).
	Shapes   []string `json:"shapes,omitempty"`
	Path     string   `json:"path"`
	Checksum string   `json:"checksum"` // hex-encoded sha256 of the binary
}

// DefaultManifestPath is ~/.prod/plugins.json.
func DefaultManifestPath(homeDir string) string {
	return filepath.Join(homeDir, ".prod", "plugins.json")
}

// LoadManifest reads the plugin manifest. A missing file means no plugins (not an
// error), so startup is a no-op when nothing is installed.
func LoadManifest(path string) ([]Entry, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.Errorf("read plugin manifest: %w", err)
	}
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, errors.Errorf("parse plugin manifest %s: %w", path, err)
	}
	return entries, nil
}

// SaveManifest writes the plugin manifest at 0600 (dir 0700).
func SaveManifest(path string, entries []Entry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return errors.Errorf("create plugin directory: %w", err)
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return errors.Errorf("write plugin manifest: %w", err)
	}
	return nil
}
