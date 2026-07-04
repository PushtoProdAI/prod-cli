// Package mcpserver exposes prod as a Model Context Protocol (MCP) server so AI
// agents (Claude, Cursor, Cline, ...) can call prod as a tool. It runs in the
// same single binary via `prod mcp` and speaks MCP over stdio.
//
// Tools today are the safe, read-only ones that need no interactive deploy
// state: list_deploys (local history) and analyze_project (stack detection).
// The mutating deploy/plan/rollback tools are the next iteration — they require
// driving the interactive deploy state machine with an explicit human-approval
// (confirm) gate; see ROADMAP.md "The MCP server".
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
	return s
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
