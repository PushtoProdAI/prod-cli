package llm

import "testing"

func TestSelectDirectClient(t *testing.T) {
	tests := []struct {
		name         string
		env          map[string]string
		wantName     string
		wantProvider string
		wantModel    string
	}{
		{
			name:         "openai when key present",
			env:          map[string]string{"OPENAI_API_KEY": "sk-openai"},
			wantName:     "prod-direct-openai",
			wantProvider: "openai",
			wantModel:    "gpt-4o",
		},
		{
			name:         "anthropic when only anthropic key present",
			env:          map[string]string{"ANTHROPIC_API_KEY": "sk-anthropic"},
			wantName:     "prod-direct-anthropic",
			wantProvider: "anthropic",
			wantModel:    "claude-3-5-sonnet-20241022",
		},
		{
			name:         "ollama fallback when no cloud keys",
			env:          map[string]string{},
			wantName:     "prod-direct-ollama",
			wantProvider: "openai-generic",
			wantModel:    "llama3.1",
		},
		{
			name:         "openai takes precedence over anthropic",
			env:          map[string]string{"OPENAI_API_KEY": "sk-openai", "ANTHROPIC_API_KEY": "sk-anthropic"},
			wantName:     "prod-direct-openai",
			wantProvider: "openai",
			wantModel:    "gpt-4o",
		},
		{
			name:         "model override applies",
			env:          map[string]string{"OPENAI_API_KEY": "sk-openai", "PROD_LLM_MODEL": "gpt-4o-mini"},
			wantName:     "prod-direct-openai",
			wantProvider: "openai",
			wantModel:    "gpt-4o-mini",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(k string) string { return tt.env[k] }
			dc := selectDirectClient(getenv)

			if dc.name != tt.wantName {
				t.Errorf("name = %q, want %q", dc.name, tt.wantName)
			}
			if dc.provider != tt.wantProvider {
				t.Errorf("provider = %q, want %q", dc.provider, tt.wantProvider)
			}
			if got := dc.options["model"]; got != tt.wantModel {
				t.Errorf("model = %v, want %q", got, tt.wantModel)
			}
		})
	}
}

func TestSelectDirectClientOllamaBaseURLOverride(t *testing.T) {
	env := map[string]string{"OLLAMA_BASE_URL": "http://ollama.internal:11434/v1"}
	dc := selectDirectClient(func(k string) string { return env[k] })

	if dc.provider != "openai-generic" {
		t.Fatalf("provider = %q, want openai-generic", dc.provider)
	}
	if got := dc.options["base_url"]; got != "http://ollama.internal:11434/v1" {
		t.Errorf("base_url = %v, want overridden value", got)
	}
}
