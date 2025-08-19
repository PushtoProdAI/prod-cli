package tui

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"
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

// formatPlanAsTable formats the deployment plan data as a nicely styled table
func (m Model) formatPlanAsTable(plan PlanDisplayMessage) string {
	var result strings.Builder

	// Add dry run indicator if applicable
	if plan.DryRun {
		result.WriteString("🔍 DRY RUN MODE - No changes will be made\n")
	}

	// Create main deployment info table
	result.WriteString(tableHeaderStyle.Render("📋 Deployment Configuration"))
	result.WriteString("\n")
	result.WriteString(tableBorderStyle.Render("┌──────────────────┬─────────────────────────────────────────────────────────┐"))
	result.WriteString("\n")

	// Table rows
	rows := [][]string{
		{"Action", plan.Action},
		{"Platform", plan.Platform},
		{"Source", plan.Source},
		{"Name", plan.Name},
		{"Language", plan.Language},
	}

	for _, row := range rows {
		result.WriteString(m.formatTableRow(row[0], row[1]))
		result.WriteString("\n")
	}

	result.WriteString(tableBorderStyle.Render("└──────────────────┴─────────────────────────────────────────────────────────┘"))
	result.WriteString("\n")

	// Services section if any
	if len(plan.Services) > 0 {
		result.WriteString("\n")
		result.WriteString(tableHeaderStyle.Render("🔧 Services"))
		result.WriteString("\n")
		result.WriteString(tableBorderStyle.Render("┌──────────────────┬─────────────────────────────────────────────────────────┐"))
		result.WriteString("\n")

		for i, service := range plan.Services {
			result.WriteString(m.formatTableRow(service.Type, service.Provider))
			result.WriteString("\n")
			if i < len(plan.Services)-1 {
				result.WriteString(tableBorderStyle.Render("├──────────────────┼─────────────────────────────────────────────────────────┤"))
				result.WriteString("\n")
			}
		}

		result.WriteString(tableBorderStyle.Render("└──────────────────┴─────────────────────────────────────────────────────────┘"))
		result.WriteString("\n")
	}

	// Environment Variables section if any - use list format
	if len(plan.EnvVars) > 0 {
		envVarsList := m.formatEnvVarsList(plan.EnvVars)
		result.WriteString(envVarsList)
	}

	return result.String()
}

// formatTableRow formats a single table row with key and value
func (m Model) formatTableRow(key, value string) string {
	keyPadded := fmt.Sprintf("%-16s", key)
	valuePadded := fmt.Sprintf("%-55s", value)

	keyStyled := tableKeyStyle.Render(keyPadded)
	valueStyled := tableValueStyle.Render(valuePadded)

	return tableBorderStyle.Render("│ ") + keyStyled + tableBorderStyle.Render(" │ ") + valueStyled + tableBorderStyle.Render(" │")
}

// formatSingleColumnRow formats a single column table row
func (m Model) formatSingleColumnRow(value string) string {
	valuePadded := fmt.Sprintf("%-75s", value)
	valueStyled := tableValueStyle.Render(valuePadded)

	return tableBorderStyle.Render("│ ") + valueStyled + tableBorderStyle.Render(" │")
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
