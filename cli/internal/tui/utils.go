package tui

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/charmbracelet/lipgloss/v2"
)

var rng = rand.New(rand.NewSource(time.Now().UnixNano()))

func getBanner() string {
	return `
______              _ 
| ___ \            | |
| |_/ / __ ___   __| |
|  __/ '__/ _ \ / _` + "`" + ` |
| |  | | | (_) | (_| |
\_|  |_|  \___/ \__,_|
`
}

func greetUser() string {
	prompts := []string{
		"What would you like to deploy today?",
		"Ready to launch something new?",
		"What's next on your cloud adventure?",
		"Need a hand with your app or infra today?",
		"What's cooking—deployments, logs, or maybe scaling?",
		"What can I help you ship today?",
		"How can I make your cloud life easier?",
		"Working on something exciting? Let's get it live.",
		"Want to check on a service, deploy something, or try something new?",
		"Let's turn code into something live—what's the plan?",
		"Your cloud assistant is ready. What's on the agenda?",
		"Deploy. Debug. Discover. What's your move?",
		"One terminal. Infinite possibility. What shall we do?",
		"Just me and you—what should we take care of today?",
		"Looking to deploy, inspect, or tweak something?",
		"Need insights, deployments, or just a friend in the cloud?",
		"What mission are we embarking on today?",
		"Want to push some code or peek under the hood?",
		"Cloud control is yours. What's first?",
		"I'm all ears (and APIs). What's the task?",
	}

	prompt := prompts[rng.Intn(len(prompts))]
	return prompt
}

// formatCurrentDir formats the current directory for display
func (m Model) formatCurrentDir() string {
	if m.currentDir == "" || m.currentDir == "unknown" {
		return "📁 unknown"
	}

	// Get home directory for shortening paths
	homeDir, err := os.UserHomeDir()
	if err == nil && strings.HasPrefix(m.currentDir, homeDir) {
		// Replace home directory with ~
		shortPath := "~" + strings.TrimPrefix(m.currentDir, homeDir)
		return fmt.Sprintf("📁 %s", shortPath)
	}

	// For very long paths, show just the last few components
	if len(m.currentDir) > 50 {
		parts := strings.Split(m.currentDir, string(filepath.Separator))
		if len(parts) > 3 {
			shortPath := "..." + string(filepath.Separator) + strings.Join(parts[len(parts)-2:], string(filepath.Separator))
			return fmt.Sprintf("📁 %s", shortPath)
		}
	}

	return fmt.Sprintf("📁 %s", m.currentDir)
}

// updateCurrentDir updates the current directory (for future use)
func (m *Model) updateCurrentDir() {
	if newDir, err := os.Getwd(); err == nil {
		m.currentDir = newDir
	}
}

// wrapText wraps text to fit within the specified width
func wrapText(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}

	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{text}
	}

	var lines []string
	var currentLine strings.Builder

	for _, word := range words {
		// If adding this word would exceed the width, start a new line
		if currentLine.Len() > 0 && currentLine.Len()+1+len(word) > width {
			lines = append(lines, currentLine.String())
			currentLine.Reset()
		}

		// Add word to current line
		if currentLine.Len() > 0 {
			currentLine.WriteString(" ")
		}
		currentLine.WriteString(word)
	}

	// Add the last line if it has content
	if currentLine.Len() > 0 {
		lines = append(lines, currentLine.String())
	}

	return lines
}

func (m *Model) styleLogMessage(content string) string {
	lower := strings.ToLower(content)

	// Error messages
	if strings.Contains(lower, "error") || strings.Contains(lower, "failed") || strings.Contains(lower, "❌") {
		return errorLogStyle.Render(content)
	}

	// Success messages
	if strings.Contains(lower, "success") || strings.Contains(lower, "deployed") || strings.Contains(lower, "✅") || strings.Contains(lower, "🚀") {
		return successLogStyle.Render(content)
	}

	// Warning messages
	if strings.Contains(lower, "warning") || strings.Contains(lower, "⚠️") || strings.Contains(lower, "dry run") {
		return warningLogStyle.Render(content)
	}

	// Default styling
	return logStyle.Render(content)
}

// clearSelection clears the current text selection
func (m *Model) clearSelection() {
	m.selection = SelectionState{}
}

// selectAll selects all visible content in the viewport
func (m *Model) selectAll() {
	if len(m.content) == 0 {
		return
	}

	m.selection = SelectionState{
		Active:     true,
		StartLine:  0,
		StartCol:   0,
		EndLine:    len(m.content) - 1,
		EndCol:     0, // Will be set to end of last line
		LastAction: "Select All",
	}

	// Set end column to the end of the last line
	if len(m.content) > 0 {
		lastLine := m.content[len(m.content)-1]
		// Remove ANSI codes to get actual text length
		cleanLine := stripANSI(lastLine)
		m.selection.EndCol = len(cleanLine)
	}

	m.updateSelectionContent()
}

// isLineInSelection checks if a given line is within the current selection
func (m Model) isLineInSelection(lineIndex int) bool {
	if !m.selection.Active {
		return false
	}

	startLine := m.selection.StartLine
	endLine := m.selection.EndLine

	// Ensure start <= end
	if startLine > endLine {
		startLine, endLine = endLine, startLine
	}

	return lineIndex >= startLine && lineIndex <= endLine
}

// isCharInSelection checks if a character at line, col is within selection
func (m Model) isCharInSelection(lineIndex, charIndex int) bool {
	if !m.selection.Active {
		return false
	}

	startLine, startCol := m.selection.StartLine, m.selection.StartCol
	endLine, endCol := m.selection.EndLine, m.selection.EndCol

	// Normalize selection bounds (ensure start <= end)
	if startLine > endLine || (startLine == endLine && startCol > endCol) {
		startLine, startCol, endLine, endCol = endLine, endCol, startLine, startCol
	}

	// Check if line is in range
	if lineIndex < startLine || lineIndex > endLine {
		return false
	}

	// For single line selection
	if startLine == endLine {
		return lineIndex == startLine && charIndex >= startCol && charIndex < endCol
	}

	// For multi-line selection
	if lineIndex == startLine {
		return charIndex >= startCol
	} else if lineIndex == endLine {
		return charIndex < endCol
	} else {
		return true // Middle lines are fully selected
	}
}

// updateSelectionContent updates the Content field of selection with current text
func (m *Model) updateSelectionContent() {
	if !m.selection.Active {
		m.selection.Content = []string{}
		return
	}

	// Get the actual viewport content that the user sees and selects from
	viewportContent := m.viewport.GetContent()
	if viewportContent == "" {
		m.selection.Content = []string{}
		return
	}

	// Split into lines, removing empty trailing lines
	availableLines := strings.Split(viewportContent, "\n")
	for len(availableLines) > 0 && availableLines[len(availableLines)-1] == "" {
		availableLines = availableLines[:len(availableLines)-1]
	}

	if len(availableLines) == 0 {
		m.selection.Content = []string{}
		return
	}

	startLine, startCol := m.selection.StartLine, m.selection.StartCol
	endLine, endCol := m.selection.EndLine, m.selection.EndCol

	// Normalize selection bounds
	if startLine > endLine || (startLine == endLine && startCol > endCol) {
		startLine, startCol, endLine, endCol = endLine, endCol, startLine, startCol
	}

	// Ensure bounds are within the actual visible content
	if startLine < 0 {
		startLine = 0
	}
	if endLine >= len(availableLines) {
		endLine = len(availableLines) - 1
	}
	if startLine >= len(availableLines) {
		m.selection.Content = []string{}
		return
	}

	var selectedLines []string

	for i := startLine; i <= endLine && i < len(availableLines); i++ {
		line := cleanForClipboard(availableLines[i]) // Clean for clipboard

		if startLine == endLine {
			// Single line selection
			if startCol < len(line) && startCol >= 0 {
				endColClamped := endCol
				if endColClamped > len(line) {
					endColClamped = len(line)
				}
				if startCol < endColClamped {
					selectedLines = append(selectedLines, line[startCol:endColClamped])
				}
			}
		} else if i == startLine {
			// First line of multi-line selection
			if startCol < len(line) && startCol >= 0 {
				selectedLines = append(selectedLines, line[startCol:])
			} else if startCol <= 0 {
				selectedLines = append(selectedLines, line)
			}
		} else if i == endLine {
			// Last line of multi-line selection
			endColClamped := endCol
			if endColClamped > len(line) {
				endColClamped = len(line)
			}
			if endColClamped > 0 {
				selectedLines = append(selectedLines, line[:endColClamped])
			}
		} else {
			// Middle lines - select entire line
			selectedLines = append(selectedLines, line)
		}
	}

	m.selection.Content = selectedLines
}

// viewportLineFromY converts a Y coordinate to a line index relative to viewport content
func (m Model) viewportLineFromY(y int) int {
	// Account for viewport padding and borders when mapping Y to line
	lineInViewport := y - 2 // Account for border/padding
	if lineInViewport < 0 {
		lineInViewport = 0
	}

	// Get the actual viewport content to determine line count
	viewportContent := m.viewport.GetContent()
	if viewportContent == "" {
		return 0
	}

	availableLines := strings.Split(viewportContent, "\n")
	// Remove empty trailing lines
	for len(availableLines) > 0 && availableLines[len(availableLines)-1] == "" {
		availableLines = availableLines[:len(availableLines)-1]
	}

	if lineInViewport >= len(availableLines) {
		lineInViewport = len(availableLines) - 1
	}
	if lineInViewport < 0 {
		lineInViewport = 0
	}

	return lineInViewport
}

// stripANSI removes ANSI escape codes from text for accurate length calculation
func stripANSI(text string) string {
	// Simple ANSI stripping - in practice you might want a more robust solution
	// This handles basic lipgloss color codes
	var result strings.Builder
	inEscape := false

	for _, char := range text {
		if char == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if char == 'm' {
				inEscape = false
			}
			continue
		}
		result.WriteRune(char)
	}

	return result.String()
}

// cleanForClipboard removes ANSI codes and converts Unicode box drawing to ASCII for clipboard
func cleanForClipboard(text string) string {
	// First strip ANSI escape codes
	cleaned := stripANSI(text)

	// Unicode box drawing character mappings to ASCII equivalents
	boxCharMap := map[rune]string{
		// Top borders
		'┌': "+", // top-left corner
		'┬': "+", // top junction
		'┐': "+", // top-right corner
		'╭': "+", // rounded top-left
		'╮': "+", // rounded top-right

		// Bottom borders
		'└': "+", // bottom-left corner
		'┴': "+", // bottom junction
		'┘': "+", // bottom-right corner
		'╰': "+", // rounded bottom-left
		'╯': "+", // rounded bottom-right

		// Side borders
		'├': "+", // left junction
		'┤': "+", // right junction
		'┼': "+", // cross junction

		// Horizontal lines
		'─': "-", // horizontal line
		'━': "-", // thick horizontal line

		// Vertical lines
		'│': "|", // vertical line
		'┃': "|", // thick vertical line
		'║': "|", // double vertical line

		// Double line characters
		'╔': "+", // double top-left
		'╦': "+", // double top junction
		'╗': "+", // double top-right
		'╠': "+", // double left junction
		'╬': "+", // double cross
		'╣': "+", // double right junction
		'╚': "+", // double bottom-left
		'╩': "+", // double bottom junction
		'╝': "+", // double bottom-right
		'═': "=", // double horizontal

		// Other common characters
		'•': "*", // bullet point
		'◦': "-", // white bullet
		'▸': ">", // triangle
		'▪': "*", // small square
		'▫': "-", // small white square
	}

	var result strings.Builder
	for _, char := range cleaned {
		if replacement, exists := boxCharMap[char]; exists {
			result.WriteString(replacement)
		} else if char > 127 {
			// For other Unicode characters, check if they're printable ASCII equivalents
			// or skip them if they're likely decorative
			switch {
			case char >= 0x2500 && char <= 0x257F: // Box Drawing block
				result.WriteString("+") // Default box char replacement
			case char >= 0x2580 && char <= 0x259F: // Block Elements
				result.WriteString("#") // Block replacement
			case char >= 0x25A0 && char <= 0x25FF: // Geometric Shapes
				result.WriteString("*") // Shape replacement
			default:
				// Keep other Unicode characters as-is (like emojis)
				result.WriteRune(char)
			}
		} else {
			result.WriteRune(char)
		}
	}

	return result.String()
}

// renderContentWithSelection renders content with visual selection highlighting
func (m Model) renderContentWithSelection(content []string) []string {
	if !m.selection.Active {
		return content
	}

	result := make([]string, len(content))

	for i, line := range content {
		if m.isLineInSelection(i) {
			// For lines in selection, we need character-level highlighting
			result[i] = m.renderLineWithSelection(line, i)
		} else {
			result[i] = line
		}
	}

	return result
}

// renderLineWithSelection renders a single line with character-level selection highlighting
func (m Model) renderLineWithSelection(line string, lineIndex int) string {
	if !m.isLineInSelection(lineIndex) {
		return line
	}

	cleanLine := stripANSI(line)
	var result strings.Builder

	for i, char := range cleanLine {
		if m.isCharInSelection(lineIndex, i) {
			result.WriteString(selectionStyle.Render(string(char)))
		} else {
			result.WriteRune(char)
		}
	}

	return result.String()
}

// copySelectionToClipboard copies the current selection to the system clipboard
func (m Model) copySelectionToClipboard() tea.Cmd {
	if !m.selection.Active || len(m.selection.Content) == 0 {
		return func() tea.Msg {
			return ClipboardCopyMsg{
				Success: false,
				Error:   "No text selected",
			}
		}
	}

	content := strings.Join(m.selection.Content, "\n")

	return func() tea.Msg {
		err := clipboard.WriteAll(content)
		if err != nil {
			return ClipboardCopyMsg{
				Success: false,
				Content: content,
				Error:   err.Error(),
			}
		}

		return ClipboardCopyMsg{
			Success: true,
			Content: content,
		}
	}
}

// formatPlanAsTable formats the deployment plan data using clean lipgloss tables
func (m Model) formatPlanAsTable(plan PlanDisplayMessage) string {
	var result strings.Builder

	// Add dry run indicator if applicable
	if plan.DryRun {
		result.WriteString("🔍 DRY RUN MODE - No changes will be made\n\n")
	}

	// Create main deployment configuration table
	header := lipgloss.NewStyle().
		Margin(1, 0, 0, 0).
		Render(tableHeaderStyle.Render("📋 Deployment Configuration"))
	result.WriteString(header)
	result.WriteString("\n")

	configTable := m.createDeploymentConfigTable(plan)
	result.WriteString(configTable)
	result.WriteString("\n")

	// Services section if any
	if len(plan.Services) > 0 {
		header := lipgloss.NewStyle().
			Margin(1, 0, 0, 0).
			Render(tableHeaderStyle.Render("🔧 Services"))
		result.WriteString(header)
		result.WriteString("\n")

		servicesTable := m.createServicesTable(plan.Services)
		result.WriteString(servicesTable)
		result.WriteString("\n")
	}

	// Pricing section if any services are available
	if len(plan.Pricing.Services) > 0 {
		header := lipgloss.NewStyle().
			Margin(1, 0, 0, 0).
			Render(tableHeaderStyle.Render("💰 Estimated Monthly Costs"))
		result.WriteString(header)
		result.WriteString("\n")

		pricingTable := m.createPricingTable(plan.Pricing)
		result.WriteString(pricingTable)

		result.WriteString("\n")
	}

	// Routes section removed per user request

	// Environment Variables section if any - use list format
	if len(plan.EnvVars) > 0 {
		envVarsList := m.formatEnvVarsList(plan.EnvVars)
		result.WriteString(envVarsList)
	}

	return result.String()
}

// formatEnvVarsList formats environment variables as a styled list
func (m Model) formatEnvVarsList(envVars []EnvVarRequirement) string {
	if len(envVars) == 0 {
		return ""
	}

	var result strings.Builder

	result.WriteString("\n")
	result.WriteString(tableHeaderStyle.Render("🔐 Environment Variables"))
	result.WriteString("\n")

	// Create a list-style display with subtle background
	listContainer := ""

	for _, envVar := range envVars {
		// Use consistent bullet for all items
		bullet := "•"

		// Style the bullet separately for better color control
		styledBullet := listBulletStyle.Render(bullet)

		// Create the list item with proper spacing
		listItem := fmt.Sprintf("  %s %s", styledBullet, envVar.Name)
		styledItem := listItemStyle.Render(listItem)
		listContainer += styledItem + "\n"
	}

	// Add the list with a subtle border
	result.WriteString(listContainerStyle.Render(listContainer))
	result.WriteString("\n")

	return result.String()
}
