package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea/v2"

	"github.com/pushtoprodai/prod-cli/internal/output"
)

// TeaWriter must satisfy the StatusWriter contract at compile time.
var _ output.StatusWriter = (*TeaWriter)(nil)

// TestTeaWriterNoPanicOnAllEvents closes the parity gap that
// internal/output/writer_parity_test.go cannot cover: TeaWriter lives in this
// package, so exercising it there would import-cycle (output <- tui). Every
// StatusWriter event must run on a TeaWriter without panicking — the same
// cross-writer anti-drift guarantee the output-package parity test gives the other
// writers (W0.4).
//
// A TeaWriter sends to a callback (normally the Bubble Tea program); here it's a
// no-op sink, so the test asserts the writer's own logic never panics regardless of
// what the program does with the messages.
func TestTeaWriterNoPanicOnAllEvents(t *testing.T) {
	w := NewTeaWriter(func(tea.Msg) {}) // discard messages; we only care that nothing panics

	_, _ = w.Write([]byte("a raw log line\n"))
	w.SendStatus("building", "Building image") // non-spinner status
	w.SendStatus("planning", "Planning")       // spinner-triggering status
	w.SendStatusComplete("planning", "Planned")
	w.SendStatusComplete("building", "Built")
	w.SendDeploymentStart("aws", "/path/to/project")
	w.SendDeploymentComplete("aws", "success", "https://x.us-east-1.awsapprunner.com", "", "op-1", "app", 1234)
	w.SendDeploymentComplete("aws", "failed", "", "something broke", "op-2", "app", 5)
	w.SendPlanApprovalRequest(map[string]any{
		"action": "deploy", "platform": "aws", "shape": "mcp-server",
		"pricing": map[string]any{"total": 12.5},
	})
	w.SendEnvVarPrompt("DATABASE_URL", "", "Enter your database URL")
	w.SendDoctorResult("LLM", "ok", "OpenAI (gpt-4o)", "")
	w.SendDoctorResult("Docker", "fail", "not running", "https://docs.docker.com/get-docker/")
	w.StartSpinner("working")
	w.StopSpinner()
	if ib, ok := any(w).(output.InfoBoxWriter); ok {
		ib.SendInfoBox("Title", "content", "info")
	}
}
