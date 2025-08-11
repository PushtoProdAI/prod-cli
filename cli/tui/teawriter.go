package tui

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/meroxa/prod/cli/internal/output"
)

type TeaWriter struct {
	send func(tea.Msg)
}

func NewTeaWriter(send func(tea.Msg)) *TeaWriter {
	return &TeaWriter{send: send}
}

func (w *TeaWriter) Write(p []byte) (int, error) {
	w.send(UIMessage{Content: string(p)})
	return len(p), nil
}

func (w *TeaWriter) SendConfirmation(message string, callback func(bool)) {
	w.send(ConfirmationPrompt{
		Message: message,
	})
}

func (w *TeaWriter) StartSpinner(message string) {
	w.send(SpinnerStartMsg{
		Message: message,
	})
}

func (w *TeaWriter) StopSpinner() {
	w.send(SpinnerStopMsg{})
}

func (w *TeaWriter) SendAuthSelection(message string, options []AuthOption) {
	w.send(AuthSelectionPrompt{
		Message: message,
		Options: options,
	})
}

func (w *TeaWriter) SendAPIKeyPrompt(message string) {
	w.send(APIKeyPrompt{
		Message: message,
	})
}

func (w *TeaWriter) SendSelect(message string, options []string) {
	w.send(SelectPrompt{
		Message: message,
		Options: options,
		Cursor:  0,
	})
}

// PromptSelection implements AuthInteractor interface
func (w *TeaWriter) PromptSelection(message string, options []string) (int, error) {
	w.SendSelect(message, options)
	// Note: This is async - the TUI will handle the response
	// For now, return an error indicating this needs to be handled differently
	return 0, fmt.Errorf("TUI selection is async - use callback pattern instead")
}

// PromptInput implements AuthInteractor interface
func (w *TeaWriter) PromptInput(message string, masked bool) (string, error) {
	if masked {
		w.SendAPIKeyPrompt(message)
	} else {
		// For non-masked input, we'd need a different prompt type
		w.send(UIMessage{Content: message + ": "})
	}
	// Note: This is async - the TUI will handle the response
	return "", fmt.Errorf("TUI input is async - use callback pattern instead")
}

// ShowProgress implements AuthInteractor interface
func (w *TeaWriter) ShowProgress(message string) {
	w.StartSpinner(message)
}

// HideProgress implements AuthInteractor interface
func (w *TeaWriter) HideProgress() {
	w.StopSpinner()
}

// Ensure TeaWriter implements AuthInteractor
var _ output.AuthInteractor = (*TeaWriter)(nil)
