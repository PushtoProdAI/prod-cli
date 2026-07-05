package deployment

import "strings"

// DeployShape is the kind of thing being deployed. It selects the liveness
// strategy (and, later, artifact generation): a worker or cron job has no URL to
// probe, so it must not be checked — or auto-rolled-back — like a web service.
type DeployShape string

const (
	ShapeWeb       DeployShape = "web"        // serves HTTP; liveness is a URL probe
	ShapeMCPServer DeployShape = "mcp-server" // an MCP server; HTTP + a protocol handshake
	ShapeWorker    DeployShape = "worker"     // a continuous non-HTTP process
	ShapeCron      DeployShape = "cron"       // a scheduled/periodic job
)

// ParseShape normalizes a shape string (from the LLM Intent or an analyzer hint),
// defaulting to ShapeWeb for empty/unknown values so existing behavior is unchanged.
func ParseShape(s string) DeployShape {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "mcp-server", "mcp", "mcpserver":
		return ShapeMCPServer
	case "worker":
		return ShapeWorker
	case "cron":
		return ShapeCron
	default:
		return ShapeWeb
	}
}

// HTTPShaped reports whether a shape's liveness is a URL probe. Web and MCP
// servers answer HTTP; workers and cron jobs do not.
func (s DeployShape) HTTPShaped() bool {
	return s == ShapeWeb || s == ShapeMCPServer
}

func (s DeployShape) String() string { return string(s) }
