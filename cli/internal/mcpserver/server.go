// Package mcpserver exposes prod as a Model Context Protocol (MCP) server so AI
// agents (Claude, Cursor, Cline, ...) can call prod as a tool. It runs in the
// same single binary via `prod mcp` and speaks MCP over stdio.
//
// Tools: list_deploys (local history), analyze_project (stack detection), doctor
// (read-only readiness self-check — LLM provider + Docker), deploy, and rollback.
// deploy and rollback are natural-language actions behind a required human-approval
// gate (confirm=false previews the plan + cost and changes nothing; confirm=true
// executes). They drive prod's own `prod run` over the JSON event substrate, so they
// reuse the exact, tested path and enforce the confirm gate by replying to the
// plan-approval event.
package mcpserver

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pushtoprodai/prod-cli/internal/analyzer"
	"github.com/pushtoprodai/prod-cli/internal/config"
	"github.com/pushtoprodai/prod-cli/internal/history"
)

// New builds the prod MCP server with all tools registered.
func New(version string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{
		Name:    "prod",
		Title:   "prod — natural-language deploys",
		Version: version,
	}, nil)

	addListDeploys(s)
	addAnalyzeProject(s)
	addDeploy(s)
	addRollback(s)
	addDoctor(s)
	return s
}

// --- deploy ------------------------------------------------------------------

type deployInput struct {
	Prompt  string `json:"prompt" jsonschema:"the natural-language deploy request, e.g. 'deploy this to fly with a postgres'"`
	Confirm bool   `json:"confirm,omitempty" jsonschema:"set true to ACTUALLY deploy (destructive, provisions cloud resources, costs money); false or omitted only PREVIEWS the plan and estimated cost and deploys nothing"`
	Path    string `json:"path,omitempty" jsonschema:"the project directory to deploy (defaults to the current directory)"`
}

type deployOutput struct {
	Deployed bool         `json:"deployed"`        // true only if confirm=true and the deploy succeeded
	Status   string       `json:"status"`          // "preview" | "success" | "failed"
	URL      string       `json:"url,omitempty"`   // the live URL, on a successful deploy
	Error    string       `json:"error,omitempty"` // failure reason, if any
	Plan     *planSummary `json:"plan,omitempty"`  // what would be / was deployed
}

func addDeploy(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "deploy",
		Description: "Deploy the project in `path` to a cloud platform from a natural-language request " +
			"(e.g. \"deploy this to fly with a postgres\"). DESTRUCTIVE and COSTS MONEY. " +
			"With confirm=false (the default) it only PREVIEWS — it returns the plan and estimated monthly cost and deploys NOTHING. " +
			"You MUST pass confirm=true to actually deploy. Always preview first, show the human the plan + cost, and only deploy after explicit human approval. " +
			"Platform credentials must already be in the environment (a Fly token, ~/.aws, a Render login, ...) — prod uses the user's own, like the CLI.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in deployInput) (*mcp.CallToolResult, deployOutput, error) {
		path := in.Path
		if path == "" {
			path = "."
		}
		res, err := runProd(ctx, in.Prompt, in.Confirm, path)
		if err != nil {
			return nil, deployOutput{}, err
		}

		out := deployOutput{Plan: summarizePlan(res.Plan)}
		if in.Confirm {
			out.Status = res.Status
			out.URL = res.URL
			out.Error = res.Error
			out.Deployed = res.Status == "success"
		} else {
			out.Status = "preview"
		}
		return nil, out, nil
	})
}

// Serve runs the MCP server over stdio until ctx is done.
func Serve(ctx context.Context, version string) error {
	return New(version).Run(ctx, &mcp.StdioTransport{})
}

// --- list_deploys -----------------------------------------------------------

type listDeploysInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"maximum number of deployments to return, most recent first (0 uses the default of 20)"`
}

type deployRecord struct {
	ID            string `json:"id"`
	OperationType string `json:"operationType"`
	ResourceName  string `json:"resourceName"`
	Platform      string `json:"platform"`
	Language      string `json:"language"`
	Status        string `json:"status"`
	StartedAt     string `json:"startedAt"`
	CompletedAt   string `json:"completedAt,omitempty"`
}

type listDeploysOutput struct {
	Mode        string         `json:"mode"` // "local" or "managed"
	Deployments []deployRecord `json:"deployments"`
}

func addListDeploys(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_deploys",
		Description: "List recent deployments prod has performed, most recent first, from local history (~/.prod/history.json). Use this to recall what has been shipped, to which platform, and whether it succeeded.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in listDeploysInput) (*mcp.CallToolResult, listDeploysOutput, error) {
		limit := in.Limit
		if limit <= 0 {
			limit = 20
		}
		store, err := history.NewStore()
		if err != nil {
			return nil, listDeploysOutput{}, err
		}
		records, err := store.List(limit)
		if err != nil {
			return nil, listDeploysOutput{}, err
		}

		out := listDeploysOutput{Mode: config.Mode(), Deployments: make([]deployRecord, 0, len(records))}
		for _, r := range records {
			rec := deployRecord{
				ID:            r.ID,
				OperationType: r.OperationType,
				ResourceName:  r.ResourceName,
				Platform:      r.Platform,
				Language:      r.Language,
				Status:        r.Status,
				StartedAt:     r.StartedAt.Format(time.RFC3339),
			}
			if r.CompletedAt != nil {
				rec.CompletedAt = r.CompletedAt.Format(time.RFC3339)
			}
			out.Deployments = append(out.Deployments, rec)
		}
		return nil, out, nil
	})
}

// --- analyze_project --------------------------------------------------------

type analyzeInput struct {
	Path string `json:"path,omitempty" jsonschema:"path to the project directory to analyze (defaults to the current directory)"`
}

type serviceRequirement struct {
	Type     string `json:"type"`
	Provider string `json:"provider"`
}

type analyzeOutput struct {
	Name         string               `json:"name"`
	Language     string               `json:"language"`
	BuildCommand string               `json:"buildCommand,omitempty"`
	StartCommand string               `json:"startCommand,omitempty"`
	Services     []serviceRequirement `json:"services"`
}

func addAnalyzeProject(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "analyze_project",
		Description: "Detect a project's language, framework, build/start commands, and required backing services (Postgres, Redis, etc.) from its files. Node and Python projects are supported today. Use this before deciding what to deploy.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in analyzeInput) (*mcp.CallToolResult, analyzeOutput, error) {
		path := in.Path
		if path == "" {
			path = "."
		}
		a, err := analyzer.GetAnalyzer(path)
		if err != nil {
			return nil, analyzeOutput{}, err
		}
		spec, err := a.Analyze()
		if err != nil {
			return nil, analyzeOutput{}, err
		}

		out := analyzeOutput{
			Name:         spec.Name,
			Language:     spec.Language,
			BuildCommand: spec.BuildCommand,
			StartCommand: spec.StartCommand,
			Services:     make([]serviceRequirement, 0, len(spec.ServiceRequirements)),
		}
		for _, r := range spec.ServiceRequirements {
			out.Services = append(out.Services, serviceRequirement{Type: r.Type, Provider: r.Provider})
		}
		return nil, out, nil
	})
}
