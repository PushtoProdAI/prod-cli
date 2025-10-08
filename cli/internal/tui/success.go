package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss/v2"
)

func (m Model) formatSuccessDisplay(msg SuccessDisplayMessage) string {
	var result strings.Builder

	header := lipgloss.NewStyle().
		Margin(1, 0, 0, 0).
		Render(lipgloss.NewStyle().
			Foreground(successColor).
			Bold(true).
			Render("🚀 Deployment Successful!"))
	result.WriteString(header)
	result.WriteString("\n\n")

	maxWidth := m.viewport.Width() - 20
	if maxWidth < 40 {
		maxWidth = 40
	}
	if maxWidth > 100 {
		maxWidth = 100
	}

	var contentLines []string

	if msg.Platform != "" {
		styledPlatform := lipgloss.NewStyle().
			Foreground(accentColor).
			Bold(true).
			Render("Platform: ")
		platformValue := lipgloss.NewStyle().
			Foreground(textColor).
			Render(msg.Platform)
		contentLines = append(contentLines, styledPlatform+platformValue)
	}

	if msg.AppName != "" {
		styledApp := lipgloss.NewStyle().
			Foreground(accentColor).
			Bold(true).
			Render("Application: ")
		appValue := lipgloss.NewStyle().
			Foreground(textColor).
			Render(msg.AppName)
		contentLines = append(contentLines, styledApp+appValue)
	}

	if msg.Url != "" {
		contentLines = append(contentLines, "")
		styledUrl := lipgloss.NewStyle().
			Foreground(accentColor).
			Bold(true).
			Render("🔗 URL: ")

		urlValue := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#60A5FA")).
			Underline(true).
			Render(msg.Url)
		contentLines = append(contentLines, styledUrl+urlValue)
	}

	content := strings.Join(contentLines, "\n")

	successBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(successColor).
		Padding(1, 2).
		Margin(0, 1).
		Width(maxWidth).
		Render(content)

	result.WriteString(successBox)
	result.WriteString("\n")

	celebrationMsg := lipgloss.NewStyle().
		Foreground(mutedColor).
		Italic(true).
		Margin(1, 1, 0, 1).
		Render("Your application is now live! 🎉")
	result.WriteString(celebrationMsg)
	result.WriteString("\n")

	return result.String()
}
