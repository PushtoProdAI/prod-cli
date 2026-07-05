// Package doctorcmd provides `prod doctor`, a quick check that prod's
// prerequisites — an LLM provider and Docker — are ready.
package doctorcmd

import (
	"context"
	"fmt"
	"os"

	"github.com/conduitio/ecdysis"
	"github.com/go-errors/errors"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
	"github.com/pushtoprodai/prod-cli/internal/llm"
	"github.com/pushtoprodai/prod-cli/internal/output"
)

var (
	_ ecdysis.CommandWithExecute = (*DoctorCommand)(nil)
	_ ecdysis.CommandWithDocs    = (*DoctorCommand)(nil)
)

// DoctorCommand runs environment checks and emits them through a StatusWriter so
// the same checklist renders in console mode and as structured events for
// JSON/MCP/agent consumers.
type DoctorCommand struct {
	StatusWriter output.StatusWriter
}

func (c *DoctorCommand) Execute(_ context.Context) error {
	// Defensive: if the writer wasn't injected, fall back to plain console output
	// so `prod doctor` still works rather than nil-panicking.
	w := c.StatusWriter
	if w == nil {
		w = output.NewConsoleWriter()
	}

	fmt.Fprint(w, "prod doctor — checking your environment\n\n")

	p := llm.Detect(os.Getenv)
	if p.Ready {
		w.SendDoctorResult("LLM", "ok", fmt.Sprintf("%s (%s) — %s", p.Name, p.Model, p.Detail), "")
	} else {
		w.SendDoctorResult("LLM", "fail", p.Detail,
			"Fix: set OPENAI_API_KEY or ANTHROPIC_API_KEY, or run a local Ollama —\n"+
				"     https://ollama.com, then `ollama pull llama3.1 && ollama serve`")
	}

	if deployment.IsDockerAvailable() {
		w.SendDoctorResult("Docker", "ok", "available (needed for Render and AWS container builds)", "")
	} else {
		w.SendDoctorResult("Docker", "fail", "not running — needed for Render and AWS deploys",
			"Fix: https://docs.docker.com/get-docker/")
	}

	fmt.Fprint(w, "\nPlatform credentials are checked when you deploy — prod uses your own\n"+
		"(a Fly token, ~/.aws, a Render login, ...). Nothing to configure here.\n")

	if !p.Ready {
		// Return an error so the process exits non-zero (`prod doctor && prod
		// "deploy ..."` short-circuits) and JSON/MCP consumers see the failure via
		// the emitted doctor_result events. The clean checklist above already went
		// to stdout; this message goes to stderr.
		return errors.New("no usable LLM provider — set OPENAI_API_KEY/ANTHROPIC_API_KEY or run Ollama")
	}
	return nil
}

func (c *DoctorCommand) Usage() string { return "doctor" }

func (c *DoctorCommand) Docs() ecdysis.Docs {
	return ecdysis.Docs{
		Short: "Check that prod's prerequisites are ready (LLM, Docker)",
		Long: `Diagnose prod's environment before you deploy:

  - which LLM provider is configured (OpenAI > Anthropic > a local Ollama), and
    whether it's actually reachable, and
  - whether Docker is available for container-based deploys (Render, AWS).

Exits non-zero if no usable LLM is found, so it composes in scripts.`,
	}
}
