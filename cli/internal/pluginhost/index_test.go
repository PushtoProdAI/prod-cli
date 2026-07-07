package pluginhost

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

const sampleIndex = `{"plugins":[
  {"name":"acme","repo":"github.com/acme/prod-provider-acme","maintainer":"Acme Inc","description":"Deploy to Acme Cloud","install":"prod plugin install github.com/acme/prod-provider-acme"},
  {"name":"widgetco","repo":"github.com/widgetco/prod-provider-widget","maintainer":"WidgetCo","description":"Serverless widgets","install":"prod plugin install github.com/widgetco/prod-provider-widget"}
]}`

func TestFetchIndexAndFilter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(sampleIndex))
	}))
	defer srv.Close()

	idx, err := FetchIndex(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("FetchIndex: %v", err)
	}
	if len(idx.Plugins) != 2 {
		t.Fatalf("want 2 plugins, got %d", len(idx.Plugins))
	}
	if all := idx.Filter(""); len(all) != 2 {
		t.Errorf("empty term should return all, got %d", len(all))
	}
	// match by name
	if m := idx.Filter("acme"); len(m) != 1 || m[0].Name != "acme" {
		t.Errorf("term 'acme' should match 1 (acme), got %+v", m)
	}
	// match by description (case-insensitive)
	if m := idx.Filter("SERVERLESS"); len(m) != 1 || m[0].Name != "widgetco" {
		t.Errorf("term 'SERVERLESS' should match widgetco, got %+v", m)
	}
	if m := idx.Filter("nothing-here"); len(m) != 0 {
		t.Errorf("no match should return empty, got %+v", m)
	}
}

func TestFetchIndexErrors(t *testing.T) {
	// non-200
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(404) }))
	defer bad.Close()
	if _, err := FetchIndex(context.Background(), bad.URL); err == nil {
		t.Error("a 404 index should error")
	}
	// invalid JSON
	junk := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("not json")) }))
	defer junk.Close()
	if _, err := FetchIndex(context.Background(), junk.URL); err == nil {
		t.Error("invalid JSON should error")
	}
}
