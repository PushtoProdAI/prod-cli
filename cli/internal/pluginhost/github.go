package pluginhost

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/go-errors/errors"
)

// GitHubRef is a parsed github.com/owner/repo[@version] plugin reference.
type GitHubRef struct {
	Owner, Repo, Version string // Version "" ⇒ the latest release
}

// IsGitHubRef reports whether s looks like a github.com plugin reference.
func IsGitHubRef(s string) bool {
	s = strings.TrimPrefix(strings.TrimSpace(s), "https://")
	return strings.HasPrefix(s, "github.com/")
}

// ParseGitHubRef parses "github.com/owner/repo[@version]" (with or without an https:// prefix).
func ParseGitHubRef(s string) (GitHubRef, error) {
	rest := strings.TrimPrefix(strings.TrimSpace(s), "https://")
	if !strings.HasPrefix(rest, "github.com/") {
		return GitHubRef{}, errors.Errorf("not a github.com reference: %q", s)
	}
	rest = strings.TrimPrefix(rest, "github.com/")
	version := ""
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		version = rest[at+1:]
		rest = rest[:at]
	}
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return GitHubRef{}, errors.Errorf("expected github.com/owner/repo[@version], got %q", s)
	}
	return GitHubRef{Owner: parts[0], Repo: parts[1], Version: version}, nil
}

type ghRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// gitHubAPIBase is overridable in tests.
var gitHubAPIBase = "https://api.github.com"

// ResolveGitHubAsset queries the GitHub releases API for the latest release (or the given
// version) and returns the download URL of the asset matching this host's OS/arch. The
// asset name is expected to contain "_<os>_<arch>" (the release-artifact convention).
func ResolveGitHubAsset(ctx context.Context, ref GitHubRef) (assetURL, tag string, err error) {
	api := fmt.Sprintf("%s/repos/%s/%s/releases/latest", gitHubAPIBase, ref.Owner, ref.Repo)
	if ref.Version != "" {
		api = fmt.Sprintf("%s/repos/%s/%s/releases/tags/%s", gitHubAPIBase, ref.Owner, ref.Repo, ref.Version)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", "", errors.Errorf("failed to query GitHub releases: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", errors.Errorf("GitHub releases API for %s/%s returned HTTP %d", ref.Owner, ref.Repo, resp.StatusCode)
	}
	var rel ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 5<<20)).Decode(&rel); err != nil {
		return "", "", errors.Errorf("failed to parse GitHub release: %w", err)
	}
	suffix := fmt.Sprintf("_%s_%s", runtime.GOOS, runtime.GOARCH)
	for _, a := range rel.Assets {
		if strings.Contains(a.Name, suffix) {
			return a.URL, rel.TagName, nil
		}
	}
	return "", "", errors.Errorf("no release asset for %s/%s matches this platform (%s_%s)", ref.Owner, ref.Repo, runtime.GOOS, runtime.GOARCH)
}
