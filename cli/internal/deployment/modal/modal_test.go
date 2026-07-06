package modal

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
)

func TestIsModalApp(t *testing.T) {
	for _, c := range []string{
		"import modal\napp = modal.App(\"x\")",
		"import modal\nstub = modal.Stub()",
		"from modal import App\napp = App()",
	} {
		if !IsModalApp(c) {
			t.Errorf("should detect a Modal app: %q", c)
		}
	}
	for _, c := range []string{"import flask\napp = Flask(__name__)", "print('hi')"} {
		if IsModalApp(c) {
			t.Errorf("should NOT detect a Modal app: %q", c)
		}
	}
}

func TestParseModalURL(t *testing.T) {
	out := "Building...\n✓ Created web endpoint => https://myws--my-app-fastapi.modal.run\nDone.\n"
	if got := modalURLRe.FindString(out); got != "https://myws--my-app-fastapi.modal.run" {
		t.Errorf("parsed URL = %q", got)
	}
	if modalURLRe.FindString("no url in this output") != "" {
		t.Error("should find no URL when none is present")
	}
}

func TestEntrypoint(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "helper.py"), []byte("def f(): pass"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.py"), []byte("import modal\napp = modal.App('x')"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := NewModalDeployment(&deployment.DeploymentSpec{Name: "x", Metadata: map[string]any{"buildContext": dir}}, io.Discard)
	entry, err := d.entrypoint()
	if err != nil || entry != "app.py" {
		t.Fatalf("entrypoint = %q, %v; want app.py", entry, err)
	}

	empty := t.TempDir()
	if err := os.WriteFile(filepath.Join(empty, "main.py"), []byte("print(1)"), 0o644); err != nil {
		t.Fatal(err)
	}
	d2 := NewModalDeployment(&deployment.DeploymentSpec{Metadata: map[string]any{"buildContext": empty}}, io.Discard)
	if _, err := d2.entrypoint(); err == nil {
		t.Error("expected an error when no Modal app is present")
	}
}

func TestEntrypointEnvOverride(t *testing.T) {
	t.Setenv("MODAL_ENTRYPOINT", "custom_entry.py")
	d := NewModalDeployment(&deployment.DeploymentSpec{Metadata: map[string]any{"buildContext": t.TempDir()}}, io.Discard)
	if entry, err := d.entrypoint(); err != nil || entry != "custom_entry.py" {
		t.Errorf("MODAL_ENTRYPOINT should win: got %q, %v", entry, err)
	}
}
