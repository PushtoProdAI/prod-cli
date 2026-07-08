package newcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScaffoldAgentWorker(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	if err := scaffold("agent-worker", "my-agent"); err != nil {
		t.Fatalf("scaffold: %v", err)
	}

	// Every expected file lands, including the dotfile (verifies the `all:` embed prefix).
	for _, f := range []string{"main.py", "requirements.txt", ".env.example", "prod.template.yaml", "README.md"} {
		if _, err := os.Stat(filepath.Join(dir, "my-agent", f)); err != nil {
			t.Errorf("missing scaffolded file %s: %v", f, err)
		}
	}

	// {{.Name}} is expanded in file contents.
	readme, err := os.ReadFile(filepath.Join(dir, "my-agent", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), "# my-agent") {
		t.Errorf("README.md did not get the project name substituted:\n%s", readme)
	}
	if strings.Contains(string(readme), "{{") {
		t.Errorf("README.md still has an unexpanded template directive")
	}

	// The worker template must carry the agent-framework signal so prod classifies it as a
	// worker from code alone (langgraph is in the analyzer's agentFrameworkDeps).
	reqs, _ := os.ReadFile(filepath.Join(dir, "my-agent", "requirements.txt"))
	if !strings.Contains(string(reqs), "langgraph") {
		t.Errorf("agent-worker requirements.txt should depend on langgraph so it detects as a worker")
	}
}

func TestScaffoldMCPServer(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	if err := scaffold("mcp-server", "my-mcp"); err != nil {
		t.Fatalf("scaffold: %v", err)
	}

	// Nested dirs (src/) and {{.Name}} in JSON must both work.
	for _, f := range []string{"package.json", "tsconfig.json", "src/index.ts", "prod.template.yaml", "README.md"} {
		if _, err := os.Stat(filepath.Join(dir, "my-mcp", f)); err != nil {
			t.Errorf("missing scaffolded file %s: %v", f, err)
		}
	}

	pkg, err := os.ReadFile(filepath.Join(dir, "my-mcp", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pkg), `"name": "my-mcp"`) {
		t.Errorf("package.json name not substituted:\n%s", pkg)
	}
	// The MCP SDK dependency is what makes prod classify this as an mcp-server (mcpDeps).
	if !strings.Contains(string(pkg), "@modelcontextprotocol/sdk") {
		t.Errorf("mcp-server package.json should depend on @modelcontextprotocol/sdk")
	}
	if strings.Contains(string(pkg), "{{") {
		t.Errorf("package.json still has an unexpanded template directive")
	}
}

// Every registered template must scaffold cleanly: no leftover {{ directives and no stray
// .tmpl files in the output (the suffix is stripped on write).
func TestAllTemplatesScaffoldClean(t *testing.T) {
	for _, tpl := range templates {
		t.Run(tpl.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			if err := scaffold(tpl.name, "proj"); err != nil {
				t.Fatalf("scaffold %s: %v", tpl.name, err)
			}
			_ = filepath.Walk(filepath.Join(dir, "proj"), func(p string, info os.FileInfo, err error) error {
				if err != nil || info.IsDir() {
					return err
				}
				if strings.HasSuffix(p, ".tmpl") {
					t.Errorf("%s: scaffolded file still has a .tmpl suffix: %s", tpl.name, p)
				}
				b, _ := os.ReadFile(p)
				if strings.Contains(string(b), "{{") {
					t.Errorf("%s: %s has an unexpanded template directive", tpl.name, filepath.Base(p))
				}
				return nil
			})
		})
	}
}

// The go-api template's Go files are stored .tmpl-suffixed (so prod's own toolchain ignores
// them); scaffolding must produce real go.mod + main.go with the module name substituted.
func TestScaffoldGoAPIRenamesTmpl(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := scaffold("go-api", "my-api"); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"go.mod", "main.go"} {
		if _, err := os.Stat(filepath.Join(dir, "my-api", f)); err != nil {
			t.Errorf("expected scaffolded %s: %v", f, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "my-api", "go.mod.tmpl")); err == nil {
		t.Error("go.mod.tmpl should have been renamed to go.mod, not written verbatim")
	}
	gomod, _ := os.ReadFile(filepath.Join(dir, "my-api", "go.mod"))
	if !strings.Contains(string(gomod), "module my-api") {
		t.Errorf("go.mod module name not substituted:\n%s", gomod)
	}
}

func TestUnknownTemplateListsAvailable(t *testing.T) {
	msg := availableTemplates("Unknown template \"nope\".")
	if !strings.Contains(msg, "agent-worker") {
		t.Errorf("unknown-template message should list agent-worker, got:\n%s", msg)
	}
}

func TestLookupTemplate(t *testing.T) {
	if _, ok := lookupTemplate("agent-worker"); !ok {
		t.Error("agent-worker should be a known template")
	}
	if _, ok := lookupTemplate("does-not-exist"); ok {
		t.Error("unknown template should not resolve")
	}
}
