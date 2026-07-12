package analyzer

import "strings"

// Dependency signatures that reveal a project's deploy shape from code, so neither the user
// nor the LLM has to name it. An MCP SDK ⇒ an MCP server; an agent framework with no web
// server ⇒ a background worker.
var (
	mcpDeps = []string{
		"mcp", "fastmcp", "mcp-server", "modelcontextprotocol",
		"@modelcontextprotocol/sdk", "@modelcontextprotocol/server",
	}
	agentFrameworkDeps = []string{
		"langchain", "langgraph", "langchain-core", "llama-index", "llama_index", "llamaindex",
		"crewai", "autogen", "pyautogen", "autogen-agentchat", "agno", "smolagents",
		"@langchain/core", "@langchain/langgraph", "@mastra/core", "@openai/agents", "openai-agents",
	}
	webServerDeps = []string{
		"fastapi", "flask", "django", "starlette", "uvicorn", "gunicorn", "aiohttp", "sanic",
		"tornado", "quart", "express", "fastify", "koa", "next", "@nestjs/core", "hono", "hapi",
		"rails", "puma", "sinatra", "rack",
		"axum", "actix-web", "actix", "rocket", "poem",
		"spring-boot", "quarkus", "micronaut", "tomcat", "netty", "spring-webmvc", "spring-webflux",
	}
)

// DetectAgentShape infers a deploy shape from dependencies when the code makes it
// unambiguous:
//   - an MCP SDK                                   → "mcp-server"
//   - an agent framework (LangChain/CrewAI/…) with NO web server → "worker"
//   - anything else                                → "" (defer to the LLM's shape)
//
// hasWebServer (from the caller's framework detection) OR a web-server dependency keeps an
// agent that also serves HTTP from being mislabeled a worker. Returns a plain string — the
// analyzer package can't import deployment (that would be an import cycle), so the caller
// ParseShape()s the result. This is a strong prior the caller lets win over the LLM.
func DetectAgentShape(deps []string, hasWebServer bool) string {
	present := make(map[string]bool, len(deps))
	for _, d := range deps {
		if n := normalizeDep(d); n != "" {
			present[n] = true
		}
	}
	anyOf := func(names []string) bool {
		for _, n := range names {
			if present[n] {
				return true
			}
		}
		return false
	}
	webServer := hasWebServer || anyOf(webServerDeps)
	switch {
	case anyOf(mcpDeps):
		return "mcp-server"
	case !webServer && anyOf(agentFrameworkDeps):
		return "worker"
	default:
		return ""
	}
}

// normalizeDep reduces a requirement/spec string to its base package name, lowercased:
// "MCP[cli]==1.2" → "mcp"; "@langchain/core@^0.3" → "@langchain/core"; "uvicorn[standard]" →
// "uvicorn".
func normalizeDep(d string) string {
	d = strings.ToLower(strings.TrimSpace(d))
	for _, sep := range []string{"[", " ", ";", "==", ">=", "<=", "~=", "!=", ">", "<", "="} {
		if i := strings.Index(d, sep); i >= 0 {
			d = d[:i]
		}
	}
	// npm "@scope/pkg@version": drop a trailing @version but keep the leading @scope.
	if i := strings.LastIndex(d, "@"); i > 0 {
		d = d[:i]
	}
	return strings.TrimSpace(d)
}
