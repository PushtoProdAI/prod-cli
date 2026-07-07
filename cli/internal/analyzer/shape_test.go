package analyzer

import "testing"

func TestDetectAgentShape(t *testing.T) {
	cases := []struct {
		name      string
		deps      []string
		hasWebSrv bool
		want      string
	}{
		{"python mcp sdk", []string{"mcp==1.2.0", "pydantic"}, false, "mcp-server"},
		{"python fastmcp", []string{"fastmcp", "httpx"}, false, "mcp-server"},
		{"node mcp sdk", []string{"@modelcontextprotocol/sdk@^1.0", "zod"}, false, "mcp-server"},
		{"crewai worker (no web)", []string{"crewai", "openai"}, false, "worker"},
		{"langgraph worker", []string{"langgraph", "langchain-core"}, false, "worker"},
		{"langchain WITH fastapi is not a worker", []string{"langchain", "fastapi", "uvicorn"}, false, ""},
		{"langchain with hasWebServer flag is not a worker", []string{"langchain"}, true, ""},
		{"mcp wins even with a web server", []string{"mcp", "fastapi"}, true, "mcp-server"},
		{"plain fastapi web app", []string{"fastapi", "uvicorn"}, true, ""},
		{"nothing conclusive", []string{"requests", "pandas"}, false, ""},
		{"node mastra agent worker", []string{"@mastra/core"}, false, "worker"},
	}
	for _, c := range cases {
		if got := DetectAgentShape(c.deps, c.hasWebSrv); got != c.want {
			t.Errorf("%s: DetectAgentShape(%v, %v) = %q, want %q", c.name, c.deps, c.hasWebSrv, got, c.want)
		}
	}
}

func TestNormalizeDep(t *testing.T) {
	for in, want := range map[string]string{
		"MCP[cli]==1.2":        "mcp",
		"@langchain/core@^0.3": "@langchain/core",
		"uvicorn[standard]":    "uvicorn",
		"  Flask >= 2.0 ":      "flask",
		"crewai":               "crewai",
	} {
		if got := normalizeDep(in); got != want {
			t.Errorf("normalizeDep(%q) = %q, want %q", in, got, want)
		}
	}
}
