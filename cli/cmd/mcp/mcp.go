// Package mcpcmd provides the `prod mcp` command, which starts prod's Model
// Context Protocol server over stdio.
package mcpcmd

import (
	"context"

	"github.com/conduitio/ecdysis"

	"github.com/pushtoprodai/prod-cli/internal/config"
	"github.com/pushtoprodai/prod-cli/internal/mcpserver"
)

var (
	_ ecdysis.CommandWithExecute = (*MCPCommand)(nil)
	_ ecdysis.CommandWithDocs    = (*MCPCommand)(nil)
)

// MCPCommand starts the prod MCP server (stdio).
type MCPCommand struct{}

func (c *MCPCommand) Execute(ctx context.Context) error {
	return mcpserver.Serve(ctx, config.Version)
}

func (c *MCPCommand) Usage() string { return "mcp" }

func (c *MCPCommand) Docs() ecdysis.Docs {
	return ecdysis.Docs{
		Short: "Start the prod MCP server (stdio)",
		Long: `Expose prod as Model Context Protocol tools over stdio so AI agents
(Claude Code, Cursor, Cline, ...) can call it. Tools: list_deploys, analyze_project.

Add to an MCP client config:

    { "mcpServers": { "prod": { "command": "prod", "args": ["mcp"] } } }`,
	}
}
