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
	w.SendDeploymentComplete("aws", "success", "https://x.us-east-1.awsapprunner.com", "", 1234)
	w.SendDeploymentComplete("aws", "failed", "", "something broke", 100)
	w.SendPlanApprovalRequest(map[string]interface{}{"action": "deploy", "platform": "aws", "summary": "deploy to aws"})
	w.SendEnvVarPrompt("DATABASE_URL", "", "Enter your database URL")
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
	} {
		if !seen[want] {
			t.Errorf("JSON writer never emitted event type %q; saw %s", want, fmt.Sprint(seen))
		}
	}
}
