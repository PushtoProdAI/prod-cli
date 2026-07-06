// Package modal deploys apps to Modal (modal.com) — serverless, Python-native,
// GPU-capable — by shelling out to the user's `modal` CLI. Unlike the managed-container
// clouds, Modal has no container-registry step; it deploys the Python app directly.
//
// EXPERIMENTAL / UNVALIDATED: this adapter has not been run against a live Modal
// account. The command shape and URL parsing follow Modal's documented CLI, but treat
// it as best-effort until validated end-to-end.
package modal

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-errors/errors"
	"github.com/pushtoprodai/prod-cli/internal/deployment"
)

// modalURLRe matches a deployed web-endpoint URL in `modal deploy` output.
var modalURLRe = regexp.MustCompile(`https://[a-zA-Z0-9][a-zA-Z0-9._-]*\.modal\.run[^\s"']*`)

// Deployment deploys a project to Modal via the `modal` CLI.
type Deployment struct {
	spec   *deployment.DeploymentSpec
	writer io.Writer
}

var _ deployment.Deployable = (*Deployment)(nil)

// NewModalDeployment builds a Modal deployable for a project spec.
func NewModalDeployment(spec *deployment.DeploymentSpec, writer io.Writer) *Deployment {
	return &Deployment{spec: spec, writer: writer}
}

// Deploy runs `modal deploy <entrypoint>` from the project directory and returns the
// deployed app (with its web-endpoint URL if it exposes one).
func (d *Deployment) Deploy(ctx context.Context) ([]deployment.CreatedResource, error) {
	if _, err := exec.LookPath("modal"); err != nil {
		return nil, errors.Errorf("the `modal` CLI isn't installed — install it with `pip install modal`, then run `modal token new`")
	}
	entry, err := d.entrypoint()
	if err != nil {
		return nil, err
	}

	fmt.Fprintf(d.writer, "Deploying %s to Modal — `modal deploy %s`\n", d.spec.Name, entry)
	cmd := exec.CommandContext(ctx, "modal", "deploy", entry)
	cmd.Dir = d.source()
	cmd.Env = os.Environ() // MODAL_TOKEN_ID / MODAL_TOKEN_SECRET pass through
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, errors.Errorf("modal deploy failed: %w\n%s", err, strings.TrimSpace(string(out)))
	}

	res := deployment.CreatedResource{
		Type:     "modal_app",
		Name:     d.spec.Name,
		Primary:  true,
		Metadata: map[string]any{},
	}
	if url := modalURLRe.FindString(string(out)); url != "" {
		res.Metadata["url"] = url
	}
	return []deployment.CreatedResource{res}, nil
}

// GetPreviousDeployment is not implemented for Modal.
func (d *Deployment) GetPreviousDeployment(context.Context) (*deployment.DeploymentInfo, error) {
	return nil, nil
}

// Rollback is not supported for Modal yet.
func (d *Deployment) Rollback(context.Context, string) error {
	return errors.Errorf("Modal rollback isn't supported yet — redeploy the previous version of your app")
}

// source is the project directory the workflow recorded on the spec.
func (d *Deployment) source() string {
	if d.spec.Metadata != nil {
		if s, ok := d.spec.Metadata["buildContext"].(string); ok && s != "" {
			return s
		}
	}
	return "."
}

// entrypoint finds the Python file that defines the Modal app. MODAL_ENTRYPOINT wins;
// otherwise the first root-level .py that references `modal.App(`/`modal.Stub(`.
func (d *Deployment) entrypoint() (string, error) {
	if e := os.Getenv("MODAL_ENTRYPOINT"); e != "" {
		return e, nil
	}
	dir := d.source()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", errors.Errorf("cannot read the project directory %q: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".py") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		if IsModalApp(string(data)) {
			return e.Name(), nil
		}
	}
	return "", errors.Errorf("no Modal app found — expected a .py file defining `modal.App(...)` (or set MODAL_ENTRYPOINT to your entrypoint)")
}

// IsModalApp reports whether Python source defines a Modal app.
func IsModalApp(content string) bool {
	if !strings.Contains(content, "modal") {
		return false
	}
	return strings.Contains(content, "modal.App(") ||
		strings.Contains(content, "modal.Stub(") ||
		strings.Contains(content, "= App(") // `from modal import App`
}
