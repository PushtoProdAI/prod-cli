package agent

import (
	"bytes"
	"strings"
	"testing"
)

// A bytes.Buffer is an io.Writer but NOT a TUIWriter. These helpers used to do
// an unchecked out.(TUIWriter) assertion and panic on console/JSON writers;
// they must now degrade to plain text instead.
func TestSendHelpersDegradeOnNonTUIWriter(t *testing.T) {
	a := &Agent{}
	var buf bytes.Buffer

	// None of these should panic on a non-TUI writer.
	a.sendConfirmation(&buf, "Proceed?")
	a.sendSelect(&buf, "Pick one", []string{"alpha", "beta"})
	a.sendAPIKeyPrompt(&buf, "API key?")
	a.sendTextPrompt(&buf, "Name?")
	a.sendTextPromptWithDefault(&buf, "Region?", "us-east-1")
	a.stopSpinner(&buf) // no output expected, must not panic

	out := buf.String()
	for _, want := range []string{"Proceed?", "Pick one", "alpha", "beta", "API key?", "Name?", "Region?", "us-east-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("fallback output missing %q; got:\n%s", want, out)
		}
	}
}
