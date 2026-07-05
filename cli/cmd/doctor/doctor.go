// Package doctorcmd provides `prod doctor`, a quick check that prod's
// prerequisites — an LLM provider and Docker — are ready.
package doctorcmd

import (
	"context"
	"fmt"
	"os"

	"github.com/conduitio/ecdysis"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
	"github.com/pushtoprodai/prod-cli/internal/llm"
)

var (
	_ ecdysis.CommandWithExecute = (*DoctorCommand)(nil)
	_ ecdysis.CommandWithDocs    = (*DoctorCommand)(nil)
)

// DoctorCommand runs environment checks and prints a checklist.
type DoctorCommand struct{}

func (c *DoctorCommand) Execute(_ context.Context) error {
	fmt.Fprint(os.Stdout, "prod doctor — checking your environment\n\n")

	p := llm.Detect(os.Getenv)
	if p.Ready {
		fmt.Fprintf(os.Stdout, "  ✓ LLM      %s (%s) — %s\n", p.Name, p.Model, p.Detail)
	} else {
		fmt.Fprintf(os.Stdout, "  ✗ LLM      %s\n", p.Detail)
		fmt.Fprintln(os.Stdout, "             Fix: set OPENAI_API_KEY or ANTHROPIC_API_KEY, or run a local Ollama —")
		fmt.Fprintln(os.Stdout, "                  https://ollama.com, then `ollama pull llama3.1 && ollama serve`")
	}

	if deployment.IsDockerAvailable() {
		fmt.Fprintln(os.Stdout, "  ✓ Docker   available (needed for Render and AWS container builds)")
	} else {
		fmt.Fprintln(os.Stdout, "  ✗ Docker   not running — needed for Render and AWS deploys")
		fmt.Fprintln(os.Stdout, "             Fix: https://docs.docker.com/get-docker/")
	}

	fmt.Fprintln(os.Stdout, "\nPlatform credentials are checked when you deploy — prod uses your own")
	fmt.Fprintln(os.Stdout, "(a Fly token, ~/.aws, a Render login, ...). Nothing to configure here.")

	if !p.Ready {
		// Exit non-zero so `prod doctor && prod "deploy ..."` short-circuits. Use
		// os.Exit rather than returning an error so the clean checklist above isn't
		// followed by a timestamped log.Fatal re-print of the message.
		os.Exit(1)
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
