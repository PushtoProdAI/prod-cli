package tui

import (
	"fmt"
	"io"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
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

// Printf provides formatted output (implements output.Writer interface)
func (w *TeaWriter) Printf(format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	w.Write([]byte(message))
}

// Println provides line output (implements output.Writer interface)
func (w *TeaWriter) Println(args ...any) {
	message := fmt.Sprintln(args...)
	w.Write([]byte(message))
}

// SendStatus sends a workflow status message with spinner logic
func (w *TeaWriter) SendStatus(status, message string) {
	w.mu.RLock()
	shouldSpin := w.shouldShowSpinnerForStatus(status)
	w.mu.RUnlock()

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
	if w.shouldStartSpinner(message) {
		spinnerMessage := w.extractSpinnerMessage(message)
		w.mu.Lock()
		w.send(SpinnerStartMsg{Message: spinnerMessage})
		w.activeSpinner = spinnerMessage
		w.mu.Unlock()
		return // Don't write the original message
	}

	// Check if this message should stop a spinner
	if w.shouldStopSpinner(message) && w.activeSpinner != "" {
		w.mu.Lock()
		w.send(SpinnerStopMsg{})
		w.activeSpinner = ""
		w.mu.Unlock()
	}
}

// shouldShowSpinnerForStatus determines if a workflow status should show a spinner
func (w *TeaWriter) shouldShowSpinnerForStatus(status string) bool {
	spinnerStatuses := []string{
		"planning",
		"analyzing",
		"summarizing",
		"deploying",
		"retrieving",
	}

	for _, spinnerStatus := range spinnerStatuses {
		if status == spinnerStatus {
			return true
		}
	}
	return false
}

// shouldStartSpinner determines if a message should start a spinner
func (w *TeaWriter) shouldStartSpinner(message string) bool {
	spinnerTriggers := []string{
		// Docker operations
		"Generating Dockerfile",
		"Building Docker image",
		"Tagging image for registry",
		"Pushing image to registry",
		// Render operations
		"🔄 Attempting rollback",
		"🔄 Attempting resource-based rollback",
		// Deployment step execution
		"🔄 Executing:",
		// Authentication
		"Checking Render authentication",
		"🔍 Validating API key",
	}

	for _, trigger := range spinnerTriggers {
		if strings.Contains(message, trigger) {
			return true
		}
	}
	return false
}

// shouldStopSpinner determines if a message should stop a spinner
func (w *TeaWriter) shouldStopSpinner(message string) bool {
	stopTriggers := []string{
		"✓ Successfully",
		"✓ Completed",
		"❌ Failed",
		"✗ Failed",
		"Error:",
		"✅ API key validated successfully",
		"✅ Authentication successful",
	}

	for _, trigger := range stopTriggers {
		if strings.Contains(message, trigger) {
			return true
		}
	}
	return false
}

// extractSpinnerMessage extracts a friendly spinner message from the log message
func (w *TeaWriter) extractSpinnerMessage(message string) string {
	messageMap := map[string]string{
		// Docker operations
		"Generating Dockerfile":      "Generating Dockerfile...",
		"Building Docker image":      "Building Docker image...",
		"Tagging image for registry": "Tagging image for registry...",
		"Pushing image to registry":  "Pushing image to registry...",
		// Render operations
		"🔄 Attempting rollback":                "Rolling back deployment...",
		"🔄 Attempting resource-based rollback": "Cleaning up resources...",
		// Authentication
		"Checking Render authentication": "Checking authentication...",
		"🔍 Validating API key":           "Validating API key...",
	}

	for trigger, spinnerMsg := range messageMap {
		if strings.Contains(message, trigger) {
			return spinnerMsg
		}
	}

	// Handle deployment step execution messages
	if strings.Contains(message, "🔄 Executing:") {
		// Extract the step description from "🔄 Executing: Creating web service..."
		parts := strings.SplitN(message, "🔄 Executing:", 2)
		if len(parts) > 1 {
			stepDesc := strings.TrimSpace(parts[1])
			// Remove trailing "..." if present and add it back for consistency
			stepDesc = strings.TrimSuffix(stepDesc, "...")
			stepDesc = strings.TrimSuffix(stepDesc, "\n")
			return stepDesc + "..."
		}
	}

	// Fallback: extract the first part of the message
	if strings.Contains(message, "...") {
		parts := strings.Split(message, "...")
		if len(parts) > 0 {
			return strings.TrimSpace(parts[0]) + "..."
		}
	}

	return "Working..."
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

// Ensure TeaWriter implements both interfaces
var (
	_ output.UnifiedOutputWriter = (*TeaWriter)(nil)
	_ output.AuthInteractor      = (*TeaWriter)(nil)
)
