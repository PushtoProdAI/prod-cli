package mcpserver

import (
	"context"
	"os"

	"github.com/go-errors/errors"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
	"github.com/pushtoprodai/prod-cli/internal/llm"
)

// --- rollback ---------------------------------------------------------------

type rollbackInput struct {
	Platform string `json:"platform" jsonschema:"the platform to roll back on (required), e.g. 'fly', 'render', 'heroku'"`
	Confirm  bool   `json:"confirm,omitempty" jsonschema:"set true to ACTUALLY roll back (destructive); false or omitted only PREVIEWS and changes nothing"`
	Path     string `json:"path,omitempty" jsonschema:"the project directory (defaults to the current directory)"`
}

type rollbackOutput struct {
	RolledBack bool         `json:"rolledBack"`      // true only if confirm=true and the rollback succeeded
	Status     string       `json:"status"`          // "preview" | "success" | "failed"
	Error      string       `json:"error,omitempty"` // failure reason, if any
	Plan       *planSummary `json:"plan,omitempty"`  // what would be / was rolled back
}

// addRollback registers the rollback tool. An agent that can deploy but not recover
// is unsafe, so rollback is a first-class tool — behind the same human-approval gate
// as deploy.
func addRollback(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "rollback",
		Description: "Roll back the most recent deployment on `platform` to its previous version. " +
			"DESTRUCTIVE. With confirm=false (the default) it only PREVIEWS and changes nothing; " +
			"pass confirm=true to actually roll back, and only after explicit human approval. " +
			"`platform` is REQUIRED (e.g. \"fly\", \"render\", \"heroku\") so it's unambiguous which deployment to revert. " +
			"Note: not every platform supports rollback yet — the preview tells you if the chosen one doesn't.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in rollbackInput) (*mcp.CallToolResult, rollbackOutput, error) {
		if in.Platform == "" {
			return nil, rollbackOutput{}, errors.Errorf("platform is required for rollback (e.g. \"fly\")")
		}
		path := in.Path
		if path == "" {
			path = "."
		}
		// Reuse the exact, tested deploy path: rollback goes through the same
		// plan-approval gate, so confirm=false previews and confirm=true executes.
		res, err := runProd(ctx, "rollback "+in.Platform, in.Confirm, path)
		if err != nil {
			return nil, rollbackOutput{}, err
		}

		out := rollbackOutput{Plan: summarizePlan(res.Plan)}
		if in.Confirm {
			out.Status = res.Status
			out.Error = res.Error
			out.RolledBack = res.Status == "success"
		} else {
			out.Status = "preview"
		}
		return nil, out, nil
	})
}

// --- doctor -----------------------------------------------------------------

type doctorInput struct{}

type llmStatus struct {
	Provider string `json:"provider,omitempty"` // "OpenAI" | "Anthropic" | "Ollama"
	Model    string `json:"model,omitempty"`
	Ready    bool   `json:"ready"`
	Detail   string `json:"detail,omitempty"` // how it's configured, or why it isn't usable
}

type doctorOutput struct {
	LLM             llmStatus `json:"llm"`
	DockerAvailable bool      `json:"dockerAvailable"` // needed for container deploys (App Runner/Cloud Run/Azure)
	Ready           bool      `json:"ready"`           // true when prod can parse intent (an LLM is configured)
}

// addDoctor registers the doctor tool — read-only self-diagnosis so an agent can
// check prod is ready to deploy before trying, and surface setup problems clearly.
func addDoctor(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "doctor",
		Description: "Check whether prod is ready to deploy from this environment: which LLM provider is configured " +
			"(OpenAI / Anthropic / Ollama, from the environment) and whether Docker is available (required for " +
			"container-based deploys like App Runner, Cloud Run, and Azure Container Apps). Read-only — call it before " +
			"deploy to self-diagnose setup problems.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ doctorInput) (*mcp.CallToolResult, doctorOutput, error) {
		p := llm.Detect(os.Getenv)
		out := doctorOutput{
			LLM:             llmStatus{Provider: p.Name, Model: p.Model, Ready: p.Ready, Detail: p.Detail},
			DockerAvailable: deployment.IsDockerAvailable(),
			Ready:           p.Ready, // Docker is platform-specific; a configured LLM is the baseline.
		}
		return nil, out, nil
	})
}
