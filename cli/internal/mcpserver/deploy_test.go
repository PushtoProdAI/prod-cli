package mcpserver

import (
	"io"
	"strings"
	"testing"
	"time"
)

// blockAfterEOF yields its data, then blocks forever instead of returning EOF —
// simulating the real child, which stays alive at its input loop and does NOT
// close stdout after emitting a terminal event.
type blockAfterEOF struct{ data *strings.Reader }

func (b *blockAfterEOF) Read(p []byte) (int, error) {
	n, err := b.data.Read(p)
	if err == io.EOF {
		select {} // block; a caller that keeps reading past the terminal event hangs
	}
	return n, err
}

// The load-bearing safety guarantee: a preview (confirm=false) must ALWAYS reply
// "rejected" to the approval gate, so it can never deploy — it only captures the
// plan.
func TestProcessEventsPreviewRejects(t *testing.T) {
	events := `{"type":"plan_approval_request","action":"deploy","platform":"fly.io","summary":"deploy to fly","pricing":{"total":7}}` + "\n"
	var stdin strings.Builder

	res := processEvents(strings.NewReader(events), &stdin, false)

	if got := strings.TrimSpace(stdin.String()); got != "rejected" {
		t.Errorf("preview must REJECT the plan (never deploy), sent %q", got)
	}
	if res.Plan == nil {
		t.Fatal("plan should still be captured for the preview")
	}
	if res.Status != "" {
		t.Errorf("preview should carry no deploy status, got %q", res.Status)
	}
}

// confirm=true approves and captures the deployment result.
func TestProcessEventsConfirmApprovesAndCaptures(t *testing.T) {
	events := strings.Join([]string{
		`{"type":"plan_approval_request","action":"deploy","platform":"fly.io"}`,
		`{"type":"log","message":"deploying..."}`, // a plain log line must be tolerated
		`{"type":"deployment_complete","platform":"fly.io","status":"success","url":"https://x.fly.dev"}`,
	}, "\n") + "\n"
	var stdin strings.Builder

	res := processEvents(strings.NewReader(events), &stdin, true)

	if got := strings.TrimSpace(stdin.String()); got != "approved" {
		t.Errorf("confirm=true must APPROVE, sent %q", got)
	}
	if res.Status != "success" || res.URL != "https://x.fly.dev" {
		t.Errorf("deploy result not captured: %+v", res)
	}
}

func TestProcessEventsFailedDeploy(t *testing.T) {
	events := strings.Join([]string{
		`{"type":"plan_approval_request","platform":"aws"}`,
		`{"type":"deployment_complete","platform":"aws","status":"failed","error":"boom"}`,
	}, "\n") + "\n"
	var stdin strings.Builder

	res := processEvents(strings.NewReader(events), &stdin, true)
	if res.Status != "failed" || res.Error != "boom" {
		t.Errorf("failure not captured: %+v", res)
	}
}

func TestProcessEventsSkipsNonJSON(t *testing.T) {
	events := "a plain log line\n" +
		`{"type":"plan_approval_request","platform":"render"}` + "\n" +
		"another log line\n"
	var stdin strings.Builder

	res := processEvents(strings.NewReader(events), &stdin, false)
	if res.Plan == nil {
		t.Error("plan should be captured despite surrounding non-JSON lines")
	}
}

// The deadlock regression test: processEvents must RETURN at the terminal event
// without reading to EOF, because the child stays alive (blocked on stdin) and
// never closes stdout. If it keeps reading, blockAfterEOF hangs it.
func TestProcessEventsReturnsAtTerminalEventWithoutEOF(t *testing.T) {
	events := strings.Join([]string{
		`{"type":"plan_approval_request","platform":"fly.io"}`,
		`{"type":"deployment_complete","status":"success","url":"https://x.fly.dev"}`,
	}, "\n") + "\n"
	r := &blockAfterEOF{data: strings.NewReader(events)}
	var stdin strings.Builder

	done := make(chan *deployResult, 1)
	go func() { done <- processEvents(r, &stdin, true) }()

	select {
	case res := <-done:
		if res.Status != "success" {
			t.Errorf("status = %q, want success", res.Status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("processEvents kept reading past deployment_complete — it would deadlock a live deploy")
	}
}

// A preview must also return without reading to EOF (after replying "rejected").
func TestProcessEventsPreviewReturnsWithoutEOF(t *testing.T) {
	events := `{"type":"plan_approval_request","platform":"render"}` + "\n"
	r := &blockAfterEOF{data: strings.NewReader(events)}
	var stdin strings.Builder

	done := make(chan *deployResult, 1)
	go func() { done <- processEvents(r, &stdin, false) }()

	select {
	case <-done:
		if got := strings.TrimSpace(stdin.String()); got != "rejected" {
			t.Errorf("preview sent %q, want rejected", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("preview kept reading past the approval event")
	}
}

// A mid-deploy env_var_prompt can't be answered headlessly — flag it, don't hang.
func TestProcessEventsEnvVarPromptFailsFast(t *testing.T) {
	events := strings.Join([]string{
		`{"type":"plan_approval_request","platform":"fly.io"}`,
		`{"type":"env_var_prompt","name":"DATABASE_URL"}`,
	}, "\n") + "\n"
	r := &blockAfterEOF{data: strings.NewReader(events)}
	var stdin strings.Builder

	done := make(chan *deployResult, 1)
	go func() { done <- processEvents(r, &stdin, true) }()

	select {
	case res := <-done:
		if !res.NeedsInteractive {
			t.Error("env_var_prompt should set NeedsInteractive")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("processEvents hung on env_var_prompt instead of failing fast")
	}
}

func TestSummarizePlan(t *testing.T) {
	ev := map[string]any{
		"action":   "deploy",
		"platform": "fly.io",
		"shape":    "mcp-server",
		"summary":  "deploy to fly with a postgres",
		"pricing":  map[string]any{"total": 12.5},
	}
	ps := summarizePlan(ev)
	if ps == nil || ps.Action != "deploy" || ps.Platform != "fly.io" || ps.EstimatedMonthlyCostUSD != 12.5 {
		t.Errorf("summarizePlan = %+v", ps)
	}
	if ps.Shape != "mcp-server" {
		t.Errorf("shape should surface in the preview, got %q", ps.Shape)
	}
	if summarizePlan(nil) != nil {
		t.Error("a nil plan should summarize to nil")
	}
}
