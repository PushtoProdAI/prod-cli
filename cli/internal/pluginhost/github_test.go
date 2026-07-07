package pluginhost

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
)

func TestParseGitHubRef(t *testing.T) {
	ok := map[string]GitHubRef{
		"github.com/acme/prod-provider-acme":  {Owner: "acme", Repo: "prod-provider-acme"},
		"https://github.com/acme/repo@v1.2.0": {Owner: "acme", Repo: "repo", Version: "v1.2.0"},
	}
	for in, want := range ok {
		got, err := ParseGitHubRef(in)
		if err != nil || got != want {
			t.Errorf("ParseGitHubRef(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"gitlab.com/x/y", "github.com/onlyowner", "", "github.com/"} {
		if _, err := ParseGitHubRef(bad); err == nil {
			t.Errorf("ParseGitHubRef(%q) should fail", bad)
		}
	}
}

func TestResolveGitHubAsset(t *testing.T) {
	suffix := fmt.Sprintf("_%s_%s", runtime.GOOS, runtime.GOARCH)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"tag_name":"v1.0.0","assets":[
			{"name":"prod-provider-acme_someother_plat","browser_download_url":"https://example/wrong"},
			{"name":"prod-provider-acme%s","browser_download_url":"https://example/right"}
		]}`, suffix)
	}))
	defer srv.Close()
	old := gitHubAPIBase
	gitHubAPIBase = srv.URL
	defer func() { gitHubAPIBase = old }()

	url, tag, err := ResolveGitHubAsset(context.Background(), GitHubRef{Owner: "acme", Repo: "prod-provider-acme"})
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://example/right" || tag != "v1.0.0" {
		t.Errorf("resolved to %q (tag %q), want the platform-matching asset", url, tag)
	}
}
