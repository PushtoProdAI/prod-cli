package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss/v2"
)

func (m Model) formatErrorDisplay(errMsg ErrorDisplayMessage) string {
	var result strings.Builder

	header := lipgloss.NewStyle().
		Margin(1, 0, 0, 0).
		Render(errorHeaderStyle.Render("❌ Deployment Error"))
	result.WriteString(header)
	result.WriteString("\n\n")

	maxWidth := m.viewport.Width() - 20
	if maxWidth < 40 {
		maxWidth = 40
	}
	if maxWidth > 100 {
		maxWidth = 100
	}

	summaryBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(errorColor).
		Padding(1, 2).
		Margin(0, 1).
		Width(maxWidth).
		Render(errMsg.Summary)

	result.WriteString(summaryBox)
	result.WriteString("\n")

	if len(errMsg.Remediations) > 0 {
		headerText := "💡 Suggested Fixes"

		header := lipgloss.NewStyle().
			Margin(1, 0, 0, 0).
			Render(remediationHeaderStyle.Render(headerText))
		result.WriteString(header)
		result.WriteString("\n")

		for i, remediation := range errMsg.Remediations {
			result.WriteString(m.formatRemediation(i, remediation))
			result.WriteString("\n")
		}

		retryMsg := lipgloss.NewStyle().
			Foreground(mutedColor).
			Italic(true).
			Margin(1, 1, 0, 1).
			Render("Once you're ready to retry, just let me know!")
		result.WriteString(retryMsg)
		result.WriteString("\n")
	}

	return result.String()
}

func (m Model) formatWarningDisplay(warnMsg WarningDisplayMessage) string {
	var result strings.Builder

	header := lipgloss.NewStyle().
		Margin(1, 0, 0, 0).
		Render(warningHeaderStyle.Render("⚠️  Deployment Warning"))
	result.WriteString(header)
	result.WriteString("\n\n")

	maxWidth := m.viewport.Width() - 20
	if maxWidth < 40 {
		maxWidth = 40
	}
	if maxWidth > 100 {
		maxWidth = 100
	}

	summaryBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(warningColor).
		Padding(1, 2).
		Margin(0, 1).
		Width(maxWidth).
		Render(warnMsg.Summary)

	result.WriteString(summaryBox)
	result.WriteString("\n")

	if len(warnMsg.Remediations) > 0 {
		headerText := "💡 Suggested Fixes"

		header := lipgloss.NewStyle().
			Margin(1, 0, 0, 0).
			Render(remediationHeaderStyle.Render(headerText))
		result.WriteString(header)
		result.WriteString("\n")

		for i, remediation := range warnMsg.Remediations {
			result.WriteString(m.formatRemediation(i, remediation))
			result.WriteString("\n")
		}

		retryMsg := lipgloss.NewStyle().
			Foreground(mutedColor).
			Italic(true).
			Margin(1, 1, 0, 1).
			Render("Once you're ready to retry, just let me know!")
		result.WriteString(retryMsg)
		result.WriteString("\n")
	}

	return result.String()
}

func (m Model) formatRemediation(index int, remediation RemediationItem) string {
	var result strings.Builder

	numberStyled := lipgloss.NewStyle().
		Foreground(accentColor).
		Bold(true).
		Render(fmt.Sprintf("[%d]", index+1))

	descriptionStyled := lipgloss.NewStyle().
		Foreground(textColor).
		Bold(false).
		Render(remediation.Description)

	headerLine := fmt.Sprintf("  %s %s", numberStyled, descriptionStyled)

	var content string
	if remediation.CliCommand != "" {
		commandBlock := m.formatCodeBlock(remediation.CliCommand)
		content = headerLine + "\n" + commandBlock
	} else {
		content = headerLine
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(1, 2).
		Margin(0, 1).
		Render(content)

	result.WriteString(box)

	return result.String()
}

func (m Model) formatCodeBlock(command string) string {
	codeContent := "  $ " + command

	styledCode := codeBlockStyle.Render(codeContent)

	return lipgloss.NewStyle().
		Margin(1, 0, 0, 2).
		Render(styledCode)
}
