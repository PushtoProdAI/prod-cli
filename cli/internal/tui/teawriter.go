package tui

import (
	"fmt"
	"io"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/meroxa/prod/cli/internal/agent"
	"github.com/meroxa/prod/cli/internal/output"
)

type TeaWriter struct {
	send          func(tea.Msg)
	mu            sync.RWMutex
	activeSpinner string
}

func NewTeaWriter(send func(tea.Msg)) *TeaWriter {
	return &TeaWriter{send: send}
}

func (w *TeaWriter) Write(p []byte) (int, error) {
	message := string(p)

	// Handle spinner logic
	w.handleSpinnerLogic(message)

	// Send the message to TUI
	w.send(UIMessage{Content: message})
	return len(p), nil
}

// SendStatus sends a workflow status message with spinner logic
func (w *TeaWriter) SendStatus(status, message string) {
	shouldSpin := output.ShouldShowSpinnerForStatus(status)

	if shouldSpin {
		w.mu.Lock()
		w.send(SpinnerStartMsg{Message: message})
		w.activeSpinner = message
		w.mu.Unlock()
		return // Don't write the message, just show spinner
	}

	w.Write([]byte(message + "\n"))
}

// SendStatusComplete sends a completion message and stops spinner
func (w *TeaWriter) SendStatusComplete(status, message string) {
	w.mu.Lock()
	if w.activeSpinner != "" {
		w.send(SpinnerStopMsg{})
		w.activeSpinner = ""
	}
	w.mu.Unlock()

	w.Write([]byte(message + "\n"))
}

// StartSpinner manually starts a spinner
func (w *TeaWriter) StartSpinner(message string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.send(SpinnerStartMsg{Message: message})
	w.activeSpinner = message
}

// StopSpinner manually stops the spinner
func (w *TeaWriter) StopSpinner() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.activeSpinner != "" {
		w.send(SpinnerStopMsg{})
		w.activeSpinner = ""
	}
}

// SetOutput is a no-op for TeaWriter (it always sends to TUI)
func (w *TeaWriter) SetOutput(output io.Writer) {
	// No-op - TeaWriter always sends to TUI
}

// SetSpinnerController is a no-op for TeaWriter (it controls its own spinner)
func (w *TeaWriter) SetSpinnerController(controller output.SpinnerController) {
	// No-op - TeaWriter controls its own spinner
}

// handleSpinnerLogic processes automatic spinner start/stop based on message content
func (w *TeaWriter) handleSpinnerLogic(message string) {
	// Check if this message should start a spinner
	if output.ShouldStartSpinner(message) {
		spinnerMessage := output.ExtractSpinnerMessage(message)
		w.send(SpinnerStartMsg{Message: spinnerMessage})
		w.mu.Lock()
		w.activeSpinner = spinnerMessage
		w.mu.Unlock()
		return // Don't write the original message
	}

	// Check if this message should stop a spinner
	if output.ShouldStopSpinner(message) && w.activeSpinner != "" {
		w.send(SpinnerStopMsg{})
		w.mu.Lock()
		w.activeSpinner = ""
		w.mu.Unlock()
	}
}

func (w *TeaWriter) SendConfirmation(message string, callback func(bool)) {
	w.send(ConfirmationPrompt{
		Message: message,
	})
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

func (w *TeaWriter) SendTextPrompt(message string) {
	w.send(TextPrompt{
		Message: message,
	})
}

func (w *TeaWriter) SendTextPromptWithDefault(message string, defaultValue string) {
	w.send(TextPrompt{
		Message:      message,
		DefaultValue: defaultValue,
	})
}

func (w *TeaWriter) SendPlan(plan agent.DeployPlan, dryRun bool) {
	// Convert agent types to TUI types
	tuiServices := make([]ServiceRequirement, len(plan.Spec.ServiceRequirements))
	for i, s := range plan.Spec.ServiceRequirements {
		tuiServices[i] = ServiceRequirement{Type: s.Type, Provider: s.Provider}
	}

	tuiEnvVars := make([]EnvVarRequirement, len(plan.Spec.EnvVars))
	for i, e := range plan.Spec.EnvVars {
		tuiEnvVars[i] = EnvVarRequirement{Name: e.VarName}
	}

	planMessage := PlanDisplayMessage{
		Summary:  plan.Summary,
		Action:   plan.Action.String(),
		Platform: plan.Platform.String(),
		Source:   plan.Source,
		Name:     plan.Spec.Name,
		Language: plan.Spec.Language,
		DryRun:   dryRun,
		Services: tuiServices,
		EnvVars:  tuiEnvVars,
	}

	w.send(planMessage)
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

// IsTeaWriter returns true to identify this as a TeaWriter
func (w *TeaWriter) IsTeaWriter() bool {
	return true
}

// Ensure TeaWriter implements StatusWriter interface
var (
	_ output.StatusWriter = (*TeaWriter)(nil)
)
