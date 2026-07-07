package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
	"github.com/pushtoprodai/prod-cli/internal/output"
)

// mcpInitializeOK is the parse gate: a real initialize result (serverInfo / capabilities /
// protocolVersion, raw or SSE-framed) is live; a plain page, an error, or an empty result
// is not.
func TestMCPInitializeOK(t *testing.T) {
	live := []string{
		`{"jsonrpc":"2.0","id":1,"result":{"serverInfo":{"name":"x","version":"1"}}}`,
		`{"result":{"capabilities":{"tools":{}}}}`,
		`{"result":{"protocolVersion":"2025-06-18"}}`,
		"event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"serverInfo\":{\"name\":\"x\"}}}\n\n",
	}
	for _, s := range live {
		if !mcpInitializeOK([]byte(s)) {
			t.Errorf("should be a live MCP handshake: %s", s)
		}
	}
	dead := []string{`<html>ok</html>`, `{"result":{}}`, `{"error":{"code":-32601}}`, ``, `not json`}
	for _, s := range dead {
		if mcpInitializeOK([]byte(s)) {
			t.Errorf("should NOT be a live MCP handshake: %q", s)
		}
	}
}

// ACD.4: mcp-server liveness requires the JSON-RPC handshake — a plain 200 web app is not
// a live MCP server.
func TestMCPServerLiveness(t *testing.T) {
	a := &Activities{uiWriter: output.NewNoOpWriter()}
	ctx := context.Background()

	mcpOK := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","serverInfo":{"name":"x","version":"1"},"capabilities":{}}}`))
	}))
	defer mcpOK.Close()
	if err := a.verifyLiveness(ctx, deployment.ShapeMCPServer, mcpOK.URL); err != nil {
		t.Errorf("a real MCP server should be live: %v", err)
	}

	plainWeb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>hello</html>"))
	}))
	defer plainWeb.Close()
	if err := a.verifyLiveness(ctx, deployment.ShapeMCPServer, plainWeb.URL); err == nil {
		t.Error("a plain 200 with no handshake must not count as a live MCP server")
	}

	// A 5xx is not-live regardless of shape.
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(502) }))
	defer broken.Close()
	if err := a.verifyLiveness(ctx, deployment.ShapeMCPServer, broken.URL); err == nil {
		t.Error("a 502 MCP endpoint must be not-live")
	}

	// An auth-walled MCP server (401/403) is reachable → live. We must NOT fail (and, for
	// an update, auto-roll-back) a healthy OAuth-protected MCP server just because we can't
	// complete the handshake unauthenticated.
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound} {
		walled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }))
		if err := a.verifyLiveness(ctx, deployment.ShapeMCPServer, walled.URL); err != nil {
			t.Errorf("MCP server returning %d is reachable and should be live, got %v", code, err)
		}
		walled.Close()
	}
}
