package deployment

import (
	"regexp"
	"strings"
)

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

// cronFieldRE matches a single cron field's allowed characters (digits and * , - /).
var cronFieldRE = regexp.MustCompile(`^[0-9*,\-/]+$`)

// IsValidCron reports whether s looks like a standard 5-field cron expression
// (minute hour day-of-month month day-of-week). It's a shape check, not a full semantic
// validator — enough to reject an LLM hallucination or a natural-language string that slipped
// through, so a cron deploy can fail safe (fall back to a worker) rather than sending garbage
// to the platform.
func IsValidCron(s string) bool {
	fields := strings.Fields(strings.TrimSpace(s))
	if len(fields) != 5 {
		return false
	}
	for _, f := range fields {
		if !cronFieldRE.MatchString(f) {
			return false
		}
	}
	return true
}
