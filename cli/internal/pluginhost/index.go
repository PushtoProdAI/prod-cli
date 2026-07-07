package pluginhost

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-errors/errors"
)

// DefaultIndexURL is the curated, git-native plugin index: a plain JSON file in the repo,
// read over HTTPS. No backend and no database — listing a plugin is a pull request against
// that file. (A hosted, signed registry is a commercial-tier decision, deliberately not
// this.)
const DefaultIndexURL = "https://raw.githubusercontent.com/PushtoProdAI/prod-cli/main/plugins.json"

// IndexEntry is one curated plugin in the index.
type IndexEntry struct {
	Name        string `json:"name"`
	Repo        string `json:"repo"`       // e.g. github.com/org/prod-provider-acme
	Maintainer  string `json:"maintainer"` // who publishes it
	Description string `json:"description"`
	Install     string `json:"install"` // the exact command to install it
}

// Index is the parsed plugins.json.
type Index struct {
	Plugins []IndexEntry `json:"plugins"`
}

// FetchIndex downloads and parses the plugin index over HTTPS. The read is size-bounded and
// time-bounded so a hostile or broken index can't hang or exhaust memory.
func FetchIndex(ctx context.Context, url string) (Index, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Index{}, err
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return Index{}, errors.Errorf("failed to fetch plugin index: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Index{}, errors.Errorf("plugin index %s returned HTTP %d", url, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20)) // 5 MB cap
	if err != nil {
		return Index{}, errors.Errorf("failed to read plugin index: %w", err)
	}
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return Index{}, errors.Errorf("failed to parse plugin index (not valid JSON): %w", err)
	}
	return idx, nil
}

// Filter returns the entries matching term (case-insensitive substring of name, repo, or
// description). An empty term returns everything.
func (idx Index) Filter(term string) []IndexEntry {
	term = strings.ToLower(strings.TrimSpace(term))
	if term == "" {
		return idx.Plugins
	}
	var out []IndexEntry
	for _, e := range idx.Plugins {
		if strings.Contains(strings.ToLower(e.Name), term) ||
			strings.Contains(strings.ToLower(e.Repo), term) ||
			strings.Contains(strings.ToLower(e.Description), term) {
			out = append(out, e)
		}
	}
	return out
}
