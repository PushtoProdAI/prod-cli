package output

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

// exerciseAllEvents drives every StatusWriter event with representative args.
// Adding a method to StatusWriter forces every writer to implement it at compile
// time; this guards that none of them *panics* at runtime on a real event (the
// class of bug that once crashed console mode via an unchecked type assertion).
func exerciseAllEvents(w StatusWriter) {
	_, _ = w.Write([]byte("a raw log line\n"))
	w.SendStatus("building", "Building image")
	w.SendStatusComplete("building", "Built")
	w.SendDeploymentStart("aws", "/path/to/project")
	w.SendDeploymentComplete("aws", "success", "https://x.us-east-1.awsapprunner.com", "", "op-123", "myapp", 1234)
	w.SendDeploymentComplete("aws", "failed", "", "something broke", "op-124", "myapp", 100)
	w.SendPlanApprovalRequest(map[string]interface{}{
		"action": "deploy", "platform": "aws", "summary": "deploy to aws",
		"shape":   "mcp-server",
		"pricing": map[string]interface{}{"total": 12.5},
	})
	w.SendEnvVarPrompt("DATABASE_URL", "", "Enter your database URL")
	w.SendDoctorResult("LLM", "ok", "OpenAI (gpt-4o) — using OPENAI_API_KEY", "")
	w.SendDoctorResult("Docker", "fail", "not running", "Fix: https://docs.docker.com/get-docker/")
	if ib, ok := w.(InfoBoxWriter); ok {
		ib.SendInfoBox("Title", "content", "info")
	}
}

// captureStdout redirects os.Stdout for the duration of fn and returns what was
// written. fn must construct any writer whose output should be captured (the JSON
// writer binds its encoder to os.Stdout at construction).
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = orig }() // restore even if fn panics
	done := make(chan string, 1)
	go func() {
		var sb strings.Builder
		_, _ = io.Copy(&sb, r)
		done <- sb.String()
	}()

	fn()

	_ = w.Close()
	return <-done
}

// Every writer must handle the full event surface without panicking. This is the
// cross-writer anti-drift guarantee: a new event or a re-introduced unchecked
// assertion in any writer fails here.
func TestWriterParityNoPanic(t *testing.T) {
	cases := []struct {
		name string
		make func() StatusWriter
	}{
		{"console", func() StatusWriter { return NewConsoleWriter() }},
		{"json", func() StatusWriter { return NewJSONWriter() }},
		{"noop", func() StatusWriter { return NewNoOpWriter() }},
		{"proxy", func() StatusWriter { return NewProxyWriter(NewConsoleWriter()) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// captureStdout keeps the suite quiet; a panic in exerciseAllEvents is
			// the only way this fails.
			_ = captureStdout(t, func() { exerciseAllEvents(tc.make()) })
		})
	}
}

// The JSON writer is the MCP substrate — it must emit well-formed JSON for every
// event and never silently drop an event type.
func TestJSONWriterEmitsWellFormedEvents(t *testing.T) {
	out := captureStdout(t, func() { exerciseAllEvents(NewJSONWriter()) })

	seen := map[string]bool{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("JSON writer emitted a non-JSON line: %q (%v)", line, err)
		}
		if typ, ok := ev["type"].(string); ok {
			seen[typ] = true
		}
	}

	for _, want := range []string{
		"log", "status", "status_complete", "deployment_start",
		"deployment_complete", "plan_approval_request", "env_var_prompt",
		"doctor_result",
	} {
		if !seen[want] {
			t.Errorf("JSON writer never emitted event type %q; saw %s", want, fmt.Sprint(seen))
		}
	}
}

// The plan event must carry shape + estimated cost through the writers, so a
// spend/undo decision isn't made blind (the point of the Phase 1 enrichment).
func TestPlanEventCarriesShapeAndCost(t *testing.T) {
	// JSON: the plan_approval_request event includes shape + pricing.total.
	out := captureStdout(t, func() { exerciseAllEvents(NewJSONWriter()) })
	var planEv map[string]any
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		var ev map[string]any
		if json.Unmarshal([]byte(strings.TrimSpace(sc.Text())), &ev) == nil && ev["type"] == "plan_approval_request" {
			planEv = ev
		}
	}
	if planEv == nil {
		t.Fatal("no plan_approval_request event emitted")
	}
	if planEv["shape"] != "mcp-server" {
		t.Errorf("JSON plan missing shape, got %v", planEv["shape"])
	}
	if pricing, _ := planEv["pricing"].(map[string]any); pricing == nil || pricing["total"] != 12.5 {
		t.Errorf("JSON plan missing cost, got %v", planEv["pricing"])
	}

	// Console: renders shape + cost.
	cout := captureStdout(t, func() {
		NewConsoleWriter().SendPlanApprovalRequest(map[string]interface{}{
			"action": "deploy", "platform": "aws", "summary": "x",
			"shape":   "worker",
			"pricing": map[string]interface{}{"total": 7.0},
		})
	})
	if !strings.Contains(cout, "worker") {
		t.Errorf("console plan missing shape, got %q", cout)
	}
	if !strings.Contains(cout, "7.00") {
		t.Errorf("console plan missing cost, got %q", cout)
	}
}

// The deployment_complete event must carry the machine-readable fields a CI action needs:
// id (to reference the deploy) and name, plus duration_ms.
func TestDeploymentCompleteCarriesIDAndName(t *testing.T) {
	out := captureStdout(t, func() {
		w := NewJSONWriter()
		w.SendDeploymentComplete("fly", "success", "https://x.fly.dev", "", "op-9", "myapp-pr-7", 4200)
	})
	var got map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var e map[string]any
		if json.Unmarshal([]byte(line), &e) == nil && e["type"] == "deployment_complete" {
			got = e
		}
	}
	if got == nil {
		t.Fatalf("no deployment_complete event in output:\n%s", out)
	}
	if got["id"] != "op-9" || got["name"] != "myapp-pr-7" {
		t.Errorf("id=%v name=%v, want op-9 / myapp-pr-7", got["id"], got["name"])
	}
	if got["url"] != "https://x.fly.dev" || got["duration_ms"].(float64) != 4200 {
		t.Errorf("url/duration wrong: %v / %v", got["url"], got["duration_ms"])
	}
}
