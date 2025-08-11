package output

import (
	"fmt"
	"io"
	"strings"
	"sync"
)

// Writer provides a simple interface for writing output messages
// without any knowledge of the underlying UI implementation
type Writer interface {
	Printf(format string, args ...any)
	Println(args ...any)
}

// SpinnerController defines the interface for controlling spinners
type SpinnerController interface {
	StartSpinner(message string)
	StopSpinner()
}

// UnifiedOutputWriter defines the interface for the unified output system
type UnifiedOutputWriter interface {
	io.Writer
	Writer // Printf, Println methods

	// Status methods (replaces StatusWriter)
	SendStatus(status, message string)
	SendStatusComplete(status, message string)

	// Spinner methods
	StartSpinner(message string)
	StopSpinner()

	// Configuration
	SetOutput(output io.Writer)
	SetSpinnerController(controller SpinnerController)
}

// UnifiedWriter is a single io.Writer compatible solution that handles all output types
// It implements UnifiedOutputWriter interface
type UnifiedWriter struct {
	mu            sync.RWMutex
	output        io.Writer
	spinnerWriter SpinnerController
	activeSpinner string
}

// Ensure UnifiedWriter implements UnifiedOutputWriter
var _ UnifiedOutputWriter = (*UnifiedWriter)(nil)

// NewUnifiedWriter creates a new unified output writer
func NewUnifiedWriter() *UnifiedWriter {
	return &UnifiedWriter{}
}

// SetOutput sets the underlying output writer (typically TeaWriter)
func (w *UnifiedWriter) SetOutput(output io.Writer) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.output = output
}

// SetSpinnerController sets the spinner controller
func (w *UnifiedWriter) SetSpinnerController(controller SpinnerController) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.spinnerWriter = controller
}

// Write implements io.Writer interface
func (w *UnifiedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	message := string(p)

	// Handle spinner logic
	w.handleSpinnerLogic(message)

	// Write to output if available
	if w.output != nil {
		return w.output.Write(p)
	}

	return len(p), nil
}

// Printf provides formatted output (for compatibility with existing code)
func (w *UnifiedWriter) Printf(format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	w.Write([]byte(message))
}

// Println provides line output (for compatibility with existing code)
func (w *UnifiedWriter) Println(args ...any) {
	message := fmt.Sprintln(args...)
	w.Write([]byte(message))
}

// SendStatus sends a workflow status message (replaces StatusWriter.SendStatus)
func (w *UnifiedWriter) SendStatus(status, message string) {
	w.mu.RLock()
	shouldSpin := w.shouldShowSpinnerForStatus(status)
	w.mu.RUnlock()

	if shouldSpin {
		w.mu.Lock()
		if w.spinnerWriter != nil {
			w.spinnerWriter.StartSpinner(message)
			w.activeSpinner = message
		}
		w.mu.Unlock()
		return // Don't write the message, just show spinner
	}

	w.Write([]byte(message + "\n"))
}

// SendStatusComplete sends a completion message and stops spinner (replaces StatusWriter.SendStatusComplete)
func (w *UnifiedWriter) SendStatusComplete(status, message string) {
	w.mu.Lock()
	if w.spinnerWriter != nil && w.activeSpinner != "" {
		w.spinnerWriter.StopSpinner()
		w.activeSpinner = ""
	}
	w.mu.Unlock()

	w.Write([]byte(message + "\n"))
}

// StartSpinner manually starts a spinner
func (w *UnifiedWriter) StartSpinner(message string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.spinnerWriter != nil {
		w.spinnerWriter.StartSpinner(message)
		w.activeSpinner = message
	}
}

// StopSpinner manually stops the spinner
func (w *UnifiedWriter) StopSpinner() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.spinnerWriter != nil && w.activeSpinner != "" {
		w.spinnerWriter.StopSpinner()
		w.activeSpinner = ""
	}
}

// handleSpinnerLogic processes automatic spinner start/stop based on message content
func (w *UnifiedWriter) handleSpinnerLogic(message string) {
	// Check if this message should start a spinner
	if w.shouldStartSpinner(message) {
		spinnerMessage := w.extractSpinnerMessage(message)
		if w.spinnerWriter != nil {
			w.spinnerWriter.StartSpinner(spinnerMessage)
			w.activeSpinner = spinnerMessage
		}
		return // Don't write the original message
	}

	// Check if this message should stop a spinner
	if w.shouldStopSpinner(message) && w.activeSpinner != "" {
		if w.spinnerWriter != nil {
			w.spinnerWriter.StopSpinner()
		}
		w.activeSpinner = ""
	}
}

// TODO: this spinner stuff could use some work, triggering off keywords is a bit brittle

// shouldShowSpinnerForStatus determines if a workflow status should show a spinner
func (w *UnifiedWriter) shouldShowSpinnerForStatus(status string) bool {
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
func (w *UnifiedWriter) shouldStartSpinner(message string) bool {
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
	}

	for _, trigger := range spinnerTriggers {
		if strings.Contains(message, trigger) {
			return true
		}
	}
	return false
}

// shouldStopSpinner determines if a message should stop a spinner
func (w *UnifiedWriter) shouldStopSpinner(message string) bool {
	stopTriggers := []string{
		"✓ Successfully",
		"✓ Completed",
		"❌ Failed",
		"✗ Failed",
		"Error:",
	}

	for _, trigger := range stopTriggers {
		if strings.Contains(message, trigger) {
			return true
		}
	}
	return false
}

// extractSpinnerMessage extracts a friendly spinner message from the log message
func (w *UnifiedWriter) extractSpinnerMessage(message string) string {
	messageMap := map[string]string{
		// Docker operations
		"Generating Dockerfile":      "Generating Dockerfile...",
		"Building Docker image":      "Building Docker image...",
		"Tagging image for registry": "Tagging image for registry...",
		"Pushing image to registry":  "Pushing image to registry...",
		// Render operations
		"🔄 Attempting rollback":                "Rolling back deployment...",
		"🔄 Attempting resource-based rollback": "Cleaning up resources...",
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

// NoOpWriter implements Writer but discards all output
type NoOpWriter struct{}

// NewNoOpWriter creates a writer that discards all output
func NewNoOpWriter() *NoOpWriter {
	return &NoOpWriter{}
}

// Printf discards the output
func (w *NoOpWriter) Printf(format string, args ...any) {
	// Do nothing
}

// Println discards the output
func (w *NoOpWriter) Println(args ...any) {
	// Do nothing
}

// Write implements io.Writer and discards the output
func (w *NoOpWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

// SendStatus discards the status message
func (w *NoOpWriter) SendStatus(status, message string) {
	// Do nothing
}

// SendStatusComplete discards the completion message
func (w *NoOpWriter) SendStatusComplete(status, message string) {
	// Do nothing
}

// StartSpinner does nothing
func (w *NoOpWriter) StartSpinner(message string) {
	// Do nothing
}

// StopSpinner does nothing
func (w *NoOpWriter) StopSpinner() {
	// Do nothing
}

// SetOutput does nothing
func (w *NoOpWriter) SetOutput(output io.Writer) {
	// Do nothing
}

// SetSpinnerController does nothing
func (w *NoOpWriter) SetSpinnerController(controller SpinnerController) {
	// Do nothing
}

// Ensure NoOpWriter implements UnifiedOutputWriter
var _ UnifiedOutputWriter = (*NoOpWriter)(nil)

// ConsoleWriter implements UnifiedOutputWriter for simple console output
// No TUI, no spinners - just plain text output to stdout/stderr
type ConsoleWriter struct {
	mu sync.RWMutex
}

// NewConsoleWriter creates a new console writer
func NewConsoleWriter() *ConsoleWriter {
	return &ConsoleWriter{}
}

// Write implements io.Writer - outputs directly to stdout
func (w *ConsoleWriter) Write(p []byte) (int, error) {
	return fmt.Print(string(p))
}

// Printf outputs formatted text to stdout
func (w *ConsoleWriter) Printf(format string, args ...any) {
	fmt.Printf(format, args...)
}

// Println outputs text with newline to stdout
func (w *ConsoleWriter) Println(args ...any) {
	fmt.Println(args...)
}

// SendStatus outputs status messages to stdout (no spinner)
func (w *ConsoleWriter) SendStatus(status, message string) {
	fmt.Printf("[%s] %s\n", strings.ToUpper(status), message)
}

// SendStatusComplete outputs completion messages to stdout
func (w *ConsoleWriter) SendStatusComplete(status, message string) {
	fmt.Printf("[%s] %s\n", strings.ToUpper(status), message)
}

// StartSpinner outputs spinner message to stdout (no actual spinner)
func (w *ConsoleWriter) StartSpinner(message string) {
	fmt.Printf("⏳ %s\n", message)
}

// StopSpinner does nothing for console output
func (w *ConsoleWriter) StopSpinner() {
	// No-op for console
}

// SetOutput does nothing for console writer (always uses stdout)
func (w *ConsoleWriter) SetOutput(output io.Writer) {
	// No-op - console writer always uses stdout
}

// SetSpinnerController does nothing for console writer
func (w *ConsoleWriter) SetSpinnerController(controller SpinnerController) {
	// No-op - console writer doesn't use spinners
}

// PromptSelection prompts user to select from options using console
func (w *ConsoleWriter) PromptSelection(message string, options []string) (int, error) {
	fmt.Printf("%s:\n", message)
	for i, option := range options {
		fmt.Printf("%d. %s\n", i+1, option)
	}

	var choice int
	fmt.Print("Enter your choice (number): ")
	_, err := fmt.Scanf("%d", &choice)
	if err != nil {
		return 0, fmt.Errorf("invalid input: %w", err)
	}

	if choice < 1 || choice > len(options) {
		return 0, fmt.Errorf("choice out of range")
	}

	return choice - 1, nil // Convert to 0-based index
}

// PromptInput prompts user for text input using console
func (w *ConsoleWriter) PromptInput(message string, masked bool) (string, error) {
	fmt.Printf("%s: ", message)

	var input string
	if masked {
		// For masked input, we'd need a more sophisticated approach
		// For now, just use regular input with a warning
		fmt.Print("(input will be visible) ")
	}

	_, err := fmt.Scanln(&input)
	if err != nil {
		return "", fmt.Errorf("failed to read input: %w", err)
	}

	return input, nil
}

// ShowProgress shows progress message for console
func (w *ConsoleWriter) ShowProgress(message string) {
	fmt.Printf("⏳ %s\n", message)
}

// HideProgress does nothing for console
func (w *ConsoleWriter) HideProgress() {
	// No-op for console
}

// Ensure ConsoleWriter implements both interfaces
var (
	_ UnifiedOutputWriter = (*ConsoleWriter)(nil)
	_ AuthInteractor      = (*ConsoleWriter)(nil)
)

// WriterType represents the type of writer to create
type WriterType string

const (
	WriterTypeTUI     WriterType = "tui"
	WriterTypeConsole WriterType = "console"
	WriterTypeNoOp    WriterType = "noop"
)

// AuthInteractor defines the interface for authentication interactions
// This is generic and doesn't tie to any specific UI implementation
type AuthInteractor interface {
	// PromptSelection prompts user to select from a list of options
	// Returns the index of the selected option
	PromptSelection(message string, options []string) (int, error)

	// PromptInput prompts user for text input with optional masking
	PromptInput(message string, masked bool) (string, error)

	// ShowProgress shows progress for a long-running operation
	ShowProgress(message string)

	// HideProgress hides the progress indicator
	HideProgress()
}

// NewWriter creates a writer based on the specified type
func NewWriter(writerType WriterType) UnifiedOutputWriter {
	switch writerType {
	case WriterTypeConsole:
		return NewConsoleWriter()
	case WriterTypeNoOp:
		return NewNoOpWriter()
	case WriterTypeTUI:
		fallthrough
	default:
		return NewUnifiedWriter()
	}
}
