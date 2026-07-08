package output

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-errors/errors"
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

	return slices.Contains(spinnerStatuses, status)
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
	SendDeploymentStart(platform, projectPath string)
	SendDeploymentComplete(platform, status, url, errorMsg, id, name string, durationMs int64)
	SendPlanApprovalRequest(plan map[string]interface{})
	SendEnvVarPrompt(varName, defaultValue, message string)
	// SendDoctorResult reports one `prod doctor` prerequisite check. status is
	// "ok" or "fail"; detail describes the outcome; fix is a (possibly
	// multi-line) remediation hint, empty when the check passed.
	SendDoctorResult(check, status, detail, fix string)
}

// InfoBoxWriter defines the interface for sending info box messages
type InfoBoxWriter interface {
	SendInfoBox(title string, content string, icon string)
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

// SendDeploymentStart does nothing
func (w *NoOpWriter) SendDeploymentStart(platform, projectPath string) {
	// Do nothing
}

// SendDeploymentComplete does nothing
func (w *NoOpWriter) SendDeploymentComplete(platform, status, url, errorMsg, id, name string, durationMs int64) {
	// Do nothing
}

// SendPlanApprovalRequest does nothing
func (w *NoOpWriter) SendPlanApprovalRequest(plan map[string]interface{}) {
	// Do nothing
}

// SendEnvVarPrompt does nothing
func (w *NoOpWriter) SendEnvVarPrompt(varName, defaultValue, message string) {
	// Do nothing
}

// SendDoctorResult does nothing
func (w *NoOpWriter) SendDoctorResult(check, status, detail, fix string) {
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

// SendDeploymentStart is a no-op for console writer
func (w *ConsoleWriter) SendDeploymentStart(platform, projectPath string) {
	// No-op
}

// SendDeploymentComplete prints the final deployment result.
func (w *ConsoleWriter) SendDeploymentComplete(platform, status, url, errorMsg, id, name string, durationMs int64) {
	switch status {
	case "success":
		if url != "" {
			fmt.Printf("✅ Deployed to %s — %s\n", platform, url)
		} else {
			fmt.Printf("✅ Deployed to %s\n", platform)
		}
	case "failed":
		if errorMsg != "" {
			fmt.Printf("❌ Deployment to %s failed: %s\n", platform, errorMsg)
		} else {
			fmt.Printf("❌ Deployment to %s failed\n", platform)
		}
	default:
		fmt.Printf("[%s] deployment to %s\n", strings.ToUpper(status), platform)
	}
}

// SendPlanApprovalRequest prints a concise summary of the deployment plan.
func (w *ConsoleWriter) SendPlanApprovalRequest(plan map[string]interface{}) {
	action, _ := plan["action"].(string)
	platform, _ := plan["platform"].(string)
	summary, _ := plan["summary"].(string)
	fmt.Printf("\nPlan: %s to %s\n", action, platform)
	if summary != "" {
		fmt.Printf("%s\n", summary)
	}
	if shape, _ := plan["shape"].(string); shape != "" && shape != "web" {
		fmt.Printf("Shape: %s\n", shape)
	}
	if pricing, ok := plan["pricing"].(map[string]interface{}); ok {
		if total, ok := pricing["total"].(float64); ok && total > 0 {
			fmt.Printf("Estimated cost: ~$%.2f/mo\n", total)
		}
	}
}

// SendEnvVarPrompt prints an environment-variable prompt.
func (w *ConsoleWriter) SendEnvVarPrompt(varName, defaultValue, message string) {
	if message != "" {
		fmt.Printf("%s\n", message)
	}
	if defaultValue != "" {
		fmt.Printf("%s [%s]: ", varName, defaultValue)
	} else {
		fmt.Printf("%s: ", varName)
	}
}

// SendDoctorResult renders one prerequisite check as a ✓/✗ line, followed by an
// indented fix hint when the check failed. The check name is padded to a fixed
// column so LLM/Docker lines align.
func (w *ConsoleWriter) SendDoctorResult(check, status, detail, fix string) {
	mark := "✓"
	if status != "ok" {
		mark = "✗"
	}
	fmt.Printf("  %s %-9s%s\n", mark, check, detail)
	if fix != "" {
		for _, line := range strings.Split(fix, "\n") {
			fmt.Printf("             %s\n", line)
		}
	}
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
		return 0, errors.Errorf("invalid input: %w", err)
	}

	if choice < 1 || choice > len(options) {
		return 0, errors.Errorf("choice out of range")
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
		return "", errors.Errorf("failed to read input: %w", err)
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

// JSONEvent represents a structured event emitted in JSON mode
type JSONEvent struct {
	Type      string    `json:"type"`
	Status    string    `json:"status,omitempty"`
	Message   string    `json:"message,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// JSONWriter implements StatusWriter for JSON-structured output
// Outputs one JSON object per line (JSON Lines format) to stdout
type JSONWriter struct {
	mu      sync.Mutex
	encoder *json.Encoder
}

// NewJSONWriter creates a new JSON writer
func NewJSONWriter() *JSONWriter {
	return &JSONWriter{
		encoder: json.NewEncoder(os.Stdout),
	}
}

// Write implements io.Writer - outputs raw logs as JSON events
func (w *JSONWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	event := JSONEvent{
		Type:      "log",
		Message:   string(p),
		Timestamp: time.Now(),
	}

	if err := w.encoder.Encode(event); err != nil {
		return 0, err
	}
	return len(p), nil
}

// SendStatus outputs status messages as JSON events
func (w *JSONWriter) SendStatus(status, message string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	event := JSONEvent{
		Type:      "status",
		Status:    status,
		Message:   message,
		Timestamp: time.Now(),
	}

	w.encoder.Encode(event)
}

// SendStatusComplete outputs completion messages as JSON events
func (w *JSONWriter) SendStatusComplete(status, message string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	event := JSONEvent{
		Type:      "status_complete",
		Status:    status,
		Message:   message,
		Timestamp: time.Now(),
	}

	w.encoder.Encode(event)
}

// SendDeploymentStart emits a deployment_start event
func (w *JSONWriter) SendDeploymentStart(platform, projectPath string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	event := map[string]interface{}{
		"type":         "deployment_start",
		"platform":     platform,
		"project_path": projectPath,
		"timestamp":    time.Now().Format(time.RFC3339),
	}

	w.encoder.Encode(event)
}

// SendDeploymentComplete emits a deployment_complete event
func (w *JSONWriter) SendDeploymentComplete(platform, status, url, errorMsg, id, name string, durationMs int64) {
	w.mu.Lock()
	defer w.mu.Unlock()

	event := map[string]interface{}{
		"type":        "deployment_complete",
		"platform":    platform,
		"status":      status,
		"duration_ms": durationMs,
		"timestamp":   time.Now().Format(time.RFC3339),
	}

	if url != "" {
		event["url"] = url
	}
	if errorMsg != "" {
		event["error"] = errorMsg
	}
	// id + name let a CI action reference the exact deployment (e.g. correlate to
	// `prod ls` or a later destroy) without re-parsing the app name it passed in.
	if id != "" {
		event["id"] = id
	}
	if name != "" {
		event["name"] = name
	}

	w.encoder.Encode(event)
}

// SendPlanApprovalRequest emits a plan_approval_request event
func (w *JSONWriter) SendPlanApprovalRequest(plan map[string]interface{}) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Add type and timestamp to the plan data
	plan["type"] = "plan_approval_request"
	plan["timestamp"] = time.Now().Format(time.RFC3339)

	if err := w.encoder.Encode(plan); err != nil {
		slog.Error("Failed to encode plan approval request", "error", err)
	}
}

// SendEnvVarPrompt emits an env_var_prompt event
func (w *JSONWriter) SendEnvVarPrompt(varName, defaultValue, message string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	event := map[string]interface{}{
		"type":          "env_var_prompt",
		"variable_name": varName,
		"default_value": defaultValue,
		"message":       message,
		"timestamp":     time.Now().Format(time.RFC3339),
	}

	if err := w.encoder.Encode(event); err != nil {
		slog.Error("Failed to encode env var prompt", "error", err)
	}
}

// SendDoctorResult emits a doctor_result event, one per prerequisite check.
func (w *JSONWriter) SendDoctorResult(check, status, detail, fix string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	event := map[string]interface{}{
		"type":      "doctor_result",
		"check":     check,
		"status":    status,
		"detail":    detail,
		"timestamp": time.Now().Format(time.RFC3339),
	}
	if fix != "" {
		event["fix"] = fix
	}

	if err := w.encoder.Encode(event); err != nil {
		slog.Error("Failed to encode doctor result", "error", err)
	}
}

// Ensure JSONWriter implements StatusWriter
var _ StatusWriter = (*JSONWriter)(nil)

// WriterType represents the type of writer to create
type WriterType string

const (
	WriterTypeTUI     WriterType = "tui"
	WriterTypeConsole WriterType = "console"
	WriterTypeNoOp    WriterType = "noop"
	WriterTypeJSON    WriterType = "json"
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

// SendDeploymentStart implements StatusWriter
func (p *ProxyWriter) SendDeploymentStart(platform, projectPath string) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	p.target.SendDeploymentStart(platform, projectPath)
}

// SendDeploymentComplete implements StatusWriter
func (p *ProxyWriter) SendDeploymentComplete(platform, status, url, errorMsg, id, name string, durationMs int64) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	p.target.SendDeploymentComplete(platform, status, url, errorMsg, id, name, durationMs)
}

// SendPlanApprovalRequest implements StatusWriter
func (p *ProxyWriter) SendPlanApprovalRequest(plan map[string]interface{}) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	p.target.SendPlanApprovalRequest(plan)
}

// SendEnvVarPrompt implements StatusWriter
func (p *ProxyWriter) SendEnvVarPrompt(varName, defaultValue, message string) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	p.target.SendEnvVarPrompt(varName, defaultValue, message)
}

// SendDoctorResult implements StatusWriter
func (p *ProxyWriter) SendDoctorResult(check, status, detail, fix string) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	p.target.SendDoctorResult(check, status, detail, fix)
}

// SendInfoBox forwards info box messages to the target if it supports them
func (p *ProxyWriter) SendInfoBox(title string, content string, icon string) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if sender, ok := p.target.(InfoBoxWriter); ok {
		sender.SendInfoBox(title, content, icon)
	} else {
		fmt.Fprintf(p.target, "\n%s %s\n%s\n", icon, title, content)
	}
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
	case WriterTypeJSON:
		return NewJSONWriter()
	case WriterTypeTUI:
		fallthrough
	default:
		return NewConsoleWriter() // Default to console writer
	}
}
