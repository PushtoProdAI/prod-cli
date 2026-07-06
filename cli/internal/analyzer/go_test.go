package analyzer

import (
	"os"
	"path/filepath"
	"testing"
)

func writeGoFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func goAnalyzer(dir string) *GoAnalyzer {
	return NewGoAnalyzer(projectFS{FS: os.DirFS(dir), rootPath: dir}).(*GoAnalyzer)
}

func TestGoAnalyzer_CanHandle(t *testing.T) {
	t.Run("go.mod present", func(t *testing.T) {
		dir := writeGoFixture(t, map[string]string{"go.mod": "module example.com/api\n\ngo 1.21\n"})
		ok, err := goAnalyzer(dir).CanHandle()
		if err != nil || !ok {
			t.Fatalf("CanHandle = %v, %v; want true", ok, err)
		}
	})
	t.Run("bare .go file without go.mod", func(t *testing.T) {
		dir := writeGoFixture(t, map[string]string{"main.go": "package main\nfunc main(){}\n"})
		if ok, _ := goAnalyzer(dir).CanHandle(); !ok {
			t.Error("a bare .go file should be handled")
		}
	})
	t.Run("no go files", func(t *testing.T) {
		dir := writeGoFixture(t, map[string]string{"README.md": "hi"})
		if ok, _ := goAnalyzer(dir).CanHandle(); ok {
			t.Error("a non-Go project should not be handled")
		}
	})
}

func TestGoAnalyzer_Analyze(t *testing.T) {
	dir := writeGoFixture(t, map[string]string{
		"go.mod": "module github.com/acme/widget-api\n\ngo 1.21\n\nrequire github.com/lib/pq v1.10.9\n",
		"main.go": `package main

import (
	"net/http"
	"os"
)

func main() {
	_ = os.Getenv("DATABASE_URL")
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {})
	http.ListenAndServe(":8080", nil)
}
`,
	})
	spec, err := goAnalyzer(dir).Analyze()
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if spec.Name != "widget-api" {
		t.Errorf("Name = %q, want widget-api (module's last segment)", spec.Name)
	}
	if spec.Language != "go" {
		t.Errorf("Language = %q, want go", spec.Language)
	}

	foundPG := false
	for _, s := range spec.ServiceRequirements {
		if s == ServicePostgres {
			foundPG = true
		}
	}
	if !foundPG {
		t.Errorf("lib/pq should imply a Postgres service; got %+v", spec.ServiceRequirements)
	}

	foundEnv := false
	for _, e := range spec.EnvVars {
		if e.VarName == "DATABASE_URL" {
			foundEnv = true
		}
	}
	if !foundEnv {
		t.Errorf("DATABASE_URL should be detected; got %+v", spec.EnvVars)
	}

	if len(spec.Routes) == 0 {
		t.Errorf("expected the /health route to be detected")
	}
}

// A stdlib-only app (no deps, no go.sum) must still analyze cleanly and imply no
// backing services — the case the Dockerfile's optional go.sum copy also handles.
func TestGoAnalyzer_StdlibOnly(t *testing.T) {
	dir := writeGoFixture(t, map[string]string{
		"go.mod":  "module hello\n\ngo 1.21\n",
		"main.go": "package main\nimport \"net/http\"\nfunc main(){ http.ListenAndServe(\":8080\", nil) }\n",
	})
	spec, err := goAnalyzer(dir).Analyze()
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if spec.Name != "hello" || len(spec.ServiceRequirements) != 0 {
		t.Errorf("stdlib-only: Name=%q services=%+v", spec.Name, spec.ServiceRequirements)
	}
}
