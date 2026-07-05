package mcpserver

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestServerToolsOverInMemoryTransport connects a real MCP client to the server
// over the SDK's in-memory transport and exercises tool discovery + a call.
func TestServerToolsOverInMemoryTransport(t *testing.T) {
	// Isolate history to a temp HOME so list_deploys doesn't touch the real ~/.prod.
	t.Setenv("HOME", t.TempDir())

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	clientT, serverT := mcp.NewInMemoryTransports()

	serverErr := make(chan error, 1)
	go func() { serverErr <- New("test").Run(ctx, serverT) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()

	// Tool discovery: both tools present.
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	got := map[string]bool{}
	for _, tool := range tools.Tools {
		got[tool.Name] = true
	}
	for _, want := range []string{"list_deploys", "analyze_project", "deploy"} {
		if !got[want] {
			t.Errorf("tool %q not advertised; got %v", want, got)
		}
	}

	// list_deploys returns a valid (empty) result in a clean HOME.
	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "list_deploys",
		Arguments: map[string]any{"limit": 5},
	})
	if err != nil {
		t.Fatalf("CallTool(list_deploys): %v", err)
	}
	if res.IsError {
		t.Fatalf("list_deploys reported an error: %+v", res.Content)
	}
	out, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("expected structured content map, got %T", res.StructuredContent)
	}
	if out["mode"] != "local" {
		t.Errorf("mode = %v, want local", out["mode"])
	}
}
