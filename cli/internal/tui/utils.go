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
	cyan := lipgloss.NewStyle().Foreground(lipgloss.Color("#7DD3FC"))
	yellow := lipgloss.NewStyle().Foreground(lipgloss.Color("#FDE047"))
	green := lipgloss.NewStyle().Foreground(lipgloss.Color("#4ADE80"))
	gray := lipgloss.NewStyle().Foreground(lipgloss.Color("#E5E7EB"))
	white := lipgloss.NewStyle().Foreground(lipgloss.Color("#F3F4F6"))

	flag := cyan.Render("   ███████") + "\n" +
		yellow.Render("   ███████") + "\n" +
		green.Render("   ███████") + "\n" +
		gray.Render("   ███    ") + "\n" +
		"           " + "\n" +
		"           "

	text := white.Render(`______              _ 
| ___ \            | |
| |_/ / __ ___   __| |
|  __/ '__/ _ \ / _` + "`" + ` |
| |  | | | (_) | (_| |
\_|  |_|  \___/ \__,_|`)

	return "\n" + lipgloss.JoinHorizontal(lipgloss.Top, flag, text) + "\n"
}

func greetUser() string {
	// Tool voice: name what prod actually does (deploy, rollback) with a concrete
	// example — not a chatty "cloud assistant" persona or capabilities it lacks.
	prompts := []string{
		"What should I deploy?  e.g. deploy this to fly with a postgres",
		"Describe a deploy in plain English — or type: rollback",
		"Ready. Try:  deploy this to render",
		"What are we shipping? (deploy · rollback)",
		"Type a deploy request, or `rollback` to undo the last one.",
	}

	return prompts[rng.Intn(len(prompts))]
}

// formatCurrentDir formats the current directory for display
func (m Model) formatCurrentDir() string {
	var dirPart string

	if m.currentDir == "" || m.currentDir == "unknown" {
		dirPart = "📁 unknown"
	} else {
		// Get home directory for shortening paths
		homeDir, err := os.UserHomeDir()
		if err == nil && strings.HasPrefix(m.currentDir, homeDir) {
			// Replace home directory with ~
			shortPath := "~" + strings.TrimPrefix(m.currentDir, homeDir)
			dirPart = fmt.Sprintf("📁 %s", shortPath)
		} else if len(m.currentDir) > 50 {
			// For very long paths, show just the last few components
			parts := strings.Split(m.currentDir, string(filepath.Separator))
			if len(parts) > 3 {
				shortPath := "..." + string(filepath.Separator) + strings.Join(parts[len(parts)-2:], string(filepath.Separator))
				dirPart = fmt.Sprintf("📁 %s", shortPath)
			} else {
				dirPart = fmt.Sprintf("📁 %s", m.currentDir)
			}
		} else {
			dirPart = fmt.Sprintf("📁 %s", m.currentDir)
		}
	}

	// Add selection status if active
	if m.selection.Active && len(m.selection.Content) > 0 {
		selectionInfo := fmt.Sprintf("📋 Selected: %d lines", len(m.selection.Content))
		if m.selection.LastAction != "" {
			selectionInfo += " • " + m.selection.LastAction
		}
		selectionInfo += " • Ctrl+C to copy • Esc to clear"
		return dirPart + " • " + selectionInfo
	}

	return dirPart
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

type logKind int

const (
	logDefault logKind = iota
	logError
	logSuccess
	logWarning
)

// classifyLog picks a severity for a log line from its content. It guards the
// error case against negated phrasings ("0 errors", "no errors", "without
// errors") so a clean result isn't rendered as a failure.
func classifyLog(content string) logKind {
	lower := strings.ToLower(content)

	// "failed" is a strong signal on its own; only "error" gets the negation guard,
	// so a mixed line like "0 errors, 1 failed" still reads as a failure.
	isError := strings.Contains(content, "❌") ||
		strings.Contains(lower, "failed") ||
		(strings.Contains(lower, "error") &&
			!strings.Contains(lower, "0 error") &&
			!strings.Contains(lower, "no error") &&
			!strings.Contains(lower, "without error"))
	switch {
	case isError:
		return logError
	case strings.Contains(lower, "success") || strings.Contains(lower, "deployed") ||
		strings.Contains(content, "✅") || strings.Contains(content, "🚀"):
		return logSuccess
	case strings.Contains(lower, "warning") || strings.Contains(content, "⚠️"):
		return logWarning
	default:
		return logDefault
	}
}

func (m *Model) styleLogMessage(content string) string {
	switch classifyLog(content) {
	case logError:
		return errorLogStyle.Render(content)
	case logSuccess:
		return successLogStyle.Render(content)
	case logWarning:
		return warningLogStyle.Render(content)
	default:
		return logStyle.Render(content)
	}
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
		StartY:     0,
		StartX:     0,
		EndY:       len(m.content) - 1,
		EndX:       0,
		LastAction: "Select All",
	}

	if len(m.content) > 0 {
		lastLine := m.content[len(m.content)-1]
		cleanLine := stripANSI(lastLine)
		m.selection.EndX = len(cleanLine)
	}

	m.updateSelectionContent()
}

// isLineInSelection checks if a given line is within the current selection
func (m Model) isLineInSelection(lineIndex int) bool {
	if !m.selection.Active {
		return false
	}

	startLine := m.selection.StartY
	endLine := m.selection.EndY

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

	startLine, startCol := m.selection.StartY, m.selection.StartX
	endLine, endCol := m.selection.EndY, m.selection.EndX

	if startLine > endLine || (startLine == endLine && startCol > endCol) {
		startLine, startCol, endLine, endCol = endLine, endCol, startLine, startCol
	}

	if lineIndex < startLine || lineIndex > endLine {
		return false
	}

	if startLine == endLine {
		return lineIndex == startLine && charIndex >= startCol && charIndex < endCol
	}

	if lineIndex == startLine {
		return charIndex >= startCol
	} else if lineIndex == endLine {
		return charIndex < endCol
	} else {
		return true
	}
}

// updateSelectionContent updates the Content field of selection with current text
func (m *Model) updateSelectionContent() {
	if !m.selection.Active {
		m.selection.Content = []string{}
		return
	}

	if len(m.content) == 0 {
		m.selection.Content = []string{}
		return
	}

	startLine, startCol := m.selection.StartY, m.selection.StartX
	endLine, endCol := m.selection.EndY, m.selection.EndX

	if startLine > endLine || (startLine == endLine && startCol > endCol) {
		startLine, startCol, endLine, endCol = endLine, endCol, startLine, startCol
	}

	if startLine < 0 {
		startLine = 0
	}
	if endLine >= len(m.content) {
		endLine = len(m.content) - 1
	}
	if startLine >= len(m.content) {
		m.selection.Content = []string{}
		return
	}

	var selectedLines []string

	for i := startLine; i <= endLine && i < len(m.content); i++ {
		line := cleanForClipboard(m.content[i])

		if startLine == endLine {
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
			if startCol < len(line) && startCol >= 0 {
				selectedLines = append(selectedLines, line[startCol:])
			} else if startCol <= 0 {
				selectedLines = append(selectedLines, line)
			}
		} else if i == endLine {
			endColClamped := endCol
			if endColClamped > len(line) {
				endColClamped = len(line)
			}
			if endColClamped > 0 {
				selectedLines = append(selectedLines, line[:endColClamped])
			}
		} else {
			selectedLines = append(selectedLines, line)
		}
	}

	m.selection.Content = selectedLines
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

// formatDeploymentHistoryAsTable formats the deployment history data using clean lipgloss tables
func (m Model) formatDeploymentHistoryAsTable(history DeploymentHistoryDisplayMessage) string {
	var result strings.Builder

	if len(history.Deployments) == 0 {
		return "No deployments found."
	}

	// Create deployment history table
	header := lipgloss.NewStyle().
		Margin(1, 0, 0, 0).
		Render(tableHeaderStyle.Render("🚀 Recent Deployments"))
	result.WriteString(header)
	result.WriteString("\n")

	deploymentTable := m.createDeploymentHistoryTable(history.Deployments)
	result.WriteString(deploymentTable)
	result.WriteString("\n")

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
