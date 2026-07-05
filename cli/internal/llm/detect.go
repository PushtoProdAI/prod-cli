package llm

import (
	"net"
	"net/url"
	"time"
)

// Provider describes the LLM backend prod will use and whether it's usable now.
type Provider struct {
	Name   string // "OpenAI" | "Anthropic" | "Ollama"
	Model  string
	Ready  bool
	Detail string // how it's configured, or why it isn't usable
}

// Detect reports which LLM provider prod will use (OpenAI > Anthropic > a local
// Ollama fallback) and whether it's usable right now. Cloud providers are Ready
// when their key is set; the Ollama fallback is probed for reachability, so a
// keyless user without Ollama gets a clear signal up front instead of a raw
// connection error in the middle of a deploy. getenv is injected for testing.
func Detect(getenv func(string) string) Provider {
	dc := selectDirectClient(getenv)
	model, _ := dc.options["model"].(string)

	switch dc.provider {
	case "openai":
		return Provider{Name: "OpenAI", Model: model, Ready: true, Detail: "using OPENAI_API_KEY"}
	case "anthropic":
		return Provider{Name: "Anthropic", Model: model, Ready: true, Detail: "using ANTHROPIC_API_KEY"}
	default: // Ollama (openai-generic)
		base, _ := dc.options["base_url"].(string)
		if reachable(base) {
			return Provider{Name: "Ollama", Model: model, Ready: true, Detail: base}
		}
		return Provider{
			Name:   "Ollama",
			Model:  model,
			Ready:  false,
			Detail: "no OPENAI_API_KEY/ANTHROPIC_API_KEY set, and Ollama isn't reachable at " + base,
		}
	}
}

// reachable does a fast TCP dial to a base URL's host:port.
func reachable(baseURL string) bool {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return false
	}
	conn, err := net.DialTimeout("tcp", u.Host, 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
