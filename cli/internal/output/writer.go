package output

import (
	"fmt"
	"io"
	"strings"
	"sync"
)

// TODO: this spinner stuff could use some work, triggering off keywords is a bit brittle

// SpinnerTriggers contains the patterns that should start spinners
var SpinnerTriggers = []string{
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
	// Authentication operations
	"🔍 Validating API key",
	"🔗 Connecting to Render authentication server",
	"Checking Render authentication",
	// Environment variable operations
	"🔍 Categorizing environment variables",
}

// SpinnerStopTriggers contains the patterns that should stop spinners
var SpinnerStopTriggers = []string{
	"✓ Successfully",
	"✓ Completed",
	"❌ Failed",
	"✗ Failed",
	"Error:",
	"✅ API key validated successfully",
	"✅ Authentication successful",
}

// ShouldStartSpinner determines if a message should start a spinner
func ShouldStartSpinner(message string) bool {
	for _, trigger := range SpinnerTriggers {
		if strings.Contains(message, trigger) {
			return true
		}
	}
	return false
}

// ShouldStopSpinner determines if a message should stop a spinner
func ShouldStopSpinner(message string) bool {
	for _, trigger := range SpinnerStopTriggers {
		if strings.Contains(message, trigger) {
			return true
		}
	}
	return false
}

// ShouldShowSpinnerForStatus determines if a workflow status should show a spinner
func ShouldShowSpinnerForStatus(status string) bool {
	spinnerStatuses := []string{
		"planning",
		"analyzing",
		"summarizing",
		"deploying",
		"retrieving",
		"pricing",
	}

	for _, spinnerStatus := range spinnerStatuses {
		if status == spinnerStatus {
			return true
		}
	}
	return false
}

// ExtractSpinnerMessage extracts a friendly spinner message from the log message
func ExtractSpinnerMessage(message string) string {
	messageMap := map[string]string{
		// Docker operations
		"Generating Dockerfile":      "Generating Dockerfile...",
		"Building Docker image":      "Building Docker image...",
		"Tagging image for registry": "Tagging image for registry...",
		"Pushing image to registry":  "Pushing image to registry...",
		// Render operations
		"🔄 Attempting rollback":                "Rolling back deployment...",
		"🔄 Attempting resource-based rollback": "Cleaning up resources...",
		// Authentication operations
		"🔍 Validating API key":                         "Validating API key...",
		"🔗 Connecting to Render authentication server": "Connecting to authentication server...",
		"Checking Render authentication":               "Checking authentication...",
		// Environment variable operations
		"🔍 Categorizing environment variables": "Categorizing environment variables...",
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

// SpinnerController defines the interface for controlling spinners
type SpinnerController interface {
	StartSpinner(message string)
	StopSpinner()
}

// StatusWriter defines the interface for sending workflow status messages
type StatusWriter interface {
	io.Writer
	SendStatus(status, message string)
	SendStatusComplete(status, message string)
}

// NoOpWriter implements Writer but discards all output
type NoOpWriter struct{}

// NewNoOpWriter creates a writer that discards all output
func NewNoOpWriter() *NoOpWriter {
	return &NoOpWriter{}
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

// Ensure NoOpWriter implements StatusWriter
var _ StatusWriter = (*NoOpWriter)(nil)

// ConsoleWriter implements StatusWriter for simple console output
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
	_ StatusWriter = (*ConsoleWriter)(nil)
)

// WriterType represents the type of writer to create
type WriterType string

const (
	WriterTypeTUI     WriterType = "tui"
	WriterTypeConsole WriterType = "console"
	WriterTypeNoOp    WriterType = "noop"
)

// ProxyWriter is a writer that forwards calls to another writer that can be updated
type ProxyWriter struct {
	mu     sync.RWMutex
	target StatusWriter
}

// NewProxyWriter creates a new proxy writer with an initial target
func NewProxyWriter(initial StatusWriter) *ProxyWriter {
	return &ProxyWriter{target: initial}
}

// SetTarget updates the target writer
func (p *ProxyWriter) SetTarget(target StatusWriter) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.target = target
}

// Write implements io.Writer
func (p *ProxyWriter) Write(data []byte) (int, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.target.Write(data)
}

// SendStatus implements StatusWriter
func (p *ProxyWriter) SendStatus(status, message string) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	p.target.SendStatus(status, message)
}

// SendStatusComplete implements StatusWriter
func (p *ProxyWriter) SendStatusComplete(status, message string) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	p.target.SendStatusComplete(status, message)
}

// Ensure ProxyWriter implements StatusWriter
var _ StatusWriter = (*ProxyWriter)(nil)

// NewWriter creates a writer based on the specified type
func NewWriter(writerType WriterType) StatusWriter {
	switch writerType {
	case WriterTypeConsole:
		return NewConsoleWriter()
	case WriterTypeNoOp:
		return NewNoOpWriter()
	case WriterTypeTUI:
		fallthrough
	default:
		return NewConsoleWriter() // Default to console writer
	}
}
