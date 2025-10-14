package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss/v2"
)

func (m Model) formatInfoBox(msg InfoBoxMessage) string {
	var result strings.Builder

	if msg.Title != "" {
		icon := msg.Icon
		if icon == "" {
			icon = "📋"
		}

		header := lipgloss.NewStyle().
			Margin(1, 0, 0, 0).
			Render(lipgloss.NewStyle().
				Foreground(primaryColor).
				Bold(true).
				Render(icon + " " + msg.Title))
		result.WriteString(header)
		result.WriteString("\n\n")
	}

	maxWidth := m.viewport.Width() - 20
	if maxWidth < 40 {
		maxWidth = 40
	}
	if maxWidth > 100 {
		maxWidth = 100
	}

	infoBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(primaryColor).
		Padding(1, 2).
		Margin(0, 1).
		Width(maxWidth).
		Render(msg.Content)

	result.WriteString(infoBox)
	result.WriteString("\n")

	return result.String()
}
