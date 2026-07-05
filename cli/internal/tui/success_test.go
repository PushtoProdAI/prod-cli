package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/v2/textinput"
)

func TestSuccessBoxRollbackHint(t *testing.T) {
	m := Model{} // zero viewport → the width falls back to the minimum

	nonAWS := m.formatSuccessDisplay(SuccessDisplayMessage{Platform: "FlyIO", AppName: "myapp", Url: "https://x.fly.dev"})
	if !strings.Contains(nonAWS, "rollback") {
		t.Errorf("a non-AWS success should surface the rollback hint, got:\n%s", nonAWS)
	}

	// App Runner rollback isn't supported, so AWS must NOT offer it.
	aws := m.formatSuccessDisplay(SuccessDisplayMessage{Platform: "AWS", AppName: "x", Url: "https://y"})
	if strings.Contains(aws, "rollback") {
		t.Errorf("AWS success must not offer rollback, got:\n%s", aws)
	}
}

func TestSetModeNormalUnmasksInput(t *testing.T) {
	m := Model{}
	m.textInput.EchoMode = textinput.EchoPassword // as if a masked prompt just ran
	m.setMode(ModeNormal)
	if m.textInput.EchoMode != textinput.EchoNormal {
		t.Error("returning to normal mode must un-mask the input (no sticky masking)")
	}
}
