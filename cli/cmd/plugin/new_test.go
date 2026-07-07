package plugincmd

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// The generated main.go must be valid Go (the ACE.3 "buildable module" guarantee, checked
// hermetically without fetching the SDK).
func TestScaffoldMainGoParses(t *testing.T) {
	for _, name := range []string{"acme", "acme-cloud", "a1"} {
		if _, err := parser.ParseFile(token.NewFileSet(), "main.go", scaffoldMainGo(name), 0); err != nil {
			t.Fatalf("scaffold for %q doesn't parse: %v", name, err)
		}
	}
	src := scaffoldMainGo("acme-cloud")
	if !strings.Contains(src, "acmecloudProvider") || !strings.Contains(src, "plugin.Serve(") {
		t.Errorf("scaffold missing provider type or Serve call")
	}
	// All six Provider methods must be stubbed.
	for _, m := range []string{"Metadata", "RegistryInfo", "CheckAuth", "Deploy", "PreviousDeployment", "Rollback"} {
		if !strings.Contains(src, ") "+m+"(") {
			t.Errorf("scaffold missing method %s", m)
		}
	}
}

func TestPluginNameValidation(t *testing.T) {
	for _, bad := range []string{"", "Acme", "1acme", "acme_cloud", "acme cloud", "-acme"} {
		if pluginNameRE.MatchString(bad) {
			t.Errorf("%q should be rejected", bad)
		}
	}
	for _, good := range []string{"acme", "acme-cloud", "a1", "gcp2"} {
		if !pluginNameRE.MatchString(good) {
			t.Errorf("%q should be accepted", good)
		}
	}
}

func TestScaffoldGoMod(t *testing.T) {
	if m := scaffoldGoMod("acme"); !strings.Contains(m, "module prod-provider-acme") || !strings.Contains(m, "go 1.25") {
		t.Errorf("bad go.mod: %q", m)
	}
}
