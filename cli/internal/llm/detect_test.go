package llm

import (
	"net"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDetect(t *testing.T) {
	t.Run("openai key wins", func(t *testing.T) {
		p := Detect(env(map[string]string{"OPENAI_API_KEY": "sk-x", "ANTHROPIC_API_KEY": "sk-ant"}))
		if !p.Ready || p.Name != "OpenAI" || p.Model != "gpt-4o" {
			t.Errorf("got %+v", p)
		}
	})

	t.Run("anthropic when no openai", func(t *testing.T) {
		p := Detect(env(map[string]string{"ANTHROPIC_API_KEY": "sk-ant"}))
		if !p.Ready || p.Name != "Anthropic" {
			t.Errorf("got %+v", p)
		}
	})

	t.Run("model override", func(t *testing.T) {
		p := Detect(env(map[string]string{"OPENAI_API_KEY": "sk-x", "PROD_LLM_MODEL": "gpt-4o-mini"}))
		if p.Model != "gpt-4o-mini" {
			t.Errorf("model = %q, want gpt-4o-mini", p.Model)
		}
	})

	t.Run("no keys + reachable Ollama = ready", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()

		p := Detect(env(map[string]string{"OLLAMA_BASE_URL": "http://" + ln.Addr().String() + "/v1"}))
		if !p.Ready || p.Name != "Ollama" {
			t.Errorf("expected ready Ollama, got %+v", p)
		}
	})

	t.Run("no keys + unreachable Ollama = not ready", func(t *testing.T) {
		// A port we open then immediately close is deterministically unreachable.
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		dead := ln.Addr().String()
		_ = ln.Close()

		p := Detect(env(map[string]string{"OLLAMA_BASE_URL": "http://" + dead + "/v1"}))
		if p.Ready || p.Name != "Ollama" {
			t.Errorf("expected not-ready Ollama, got %+v", p)
		}
	})
}

func TestReachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	live := ln.Addr().String()
	defer ln.Close()

	if !reachable("http://" + live) {
		t.Error("a live listener should be reachable")
	}

	ln2, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := ln2.Addr().String()
	_ = ln2.Close()
	if reachable("http://" + dead) {
		t.Error("a closed port should be unreachable")
	}

	if reachable("::not a url") {
		t.Error("a malformed URL should be unreachable")
	}
}
