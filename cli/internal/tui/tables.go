package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// createDeploymentConfigTable creates a table for deployment configuration
func (m Model) createDeploymentConfigTable(plan PlanDisplayMessage) string {
	rows := [][]string{
		{"Action", plan.Action},
		{"Platform", plan.Platform},
		{"Source", plan.Source},
		{"Name", plan.Name},
		{"Language", plan.Language},
	}
	columnWidths := []int{18, 54}

	table := m.createTable(rows, columnWidths)

	styledTable := lipgloss.NewStyle().
		Margin(0, 0, 1, 0).
		Render(table)

	return styledTable
}

// createServicesTable creates a table for services
func (m Model) createServicesTable(services []ServiceRequirement) string {
	rows := make([][]string, len(services))
	for i, service := range services {
		rows[i] = []string{service.Type, service.Provider}
	}
	columnWidths := []int{18, 54}

	table := m.createTable(rows, columnWidths)

	styledTable := lipgloss.NewStyle().
		Margin(0, 0, 1, 0).
		Render(table)

	return styledTable
}

// createTable creates a headerless table
func (m Model) createTable(rows [][]string, columnWidths []int) string {
	if len(rows) == 0 {
		return ""
	}

	var result strings.Builder

	// Create data rows (no headers)
	for _, row := range rows {
		dataRow := m.createTableRow(row, columnWidths)
		result.WriteString(dataRow)
		result.WriteString("\n")
	}

	// Remove the trailing newline
	tableContent := strings.TrimSuffix(result.String(), "\n")

	// Wrap the entire table with an outside border
	borderedTable := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Render(tableContent)

	return borderedTable
}

// createTableRow creates a single table row with proper styling
func (m Model) createTableRow(cells []string, columnWidths []int) string {
	var rowParts []string

	for i, cell := range cells {
		if i >= len(columnWidths) {
			break
		}

		// Truncate or pad cell content to fit column width
		cellContent := cell
		if len(cellContent) > columnWidths[i] {
			cellContent = cellContent[:columnWidths[i]-3] + "..."
		}

		// Create styled cell with proper width
		var styledCell string
		if i == 0 {
			// First column: bold and accent color
			styledCell = lipgloss.NewStyle().
				Width(columnWidths[i]).
				Align(lipgloss.Left).
				Foreground(accentColor).
				Bold(true).
				Render(cellContent)
		} else {
			// Other columns: regular styling
			styledCell = tableValueStyle.
				Width(columnWidths[i]).
				Align(lipgloss.Left).
				Render(cellContent)
		}

		rowParts = append(rowParts, styledCell)
	}

	// Join cells with border separator
	borderSep := tableBorderStyle.Render("│")
	return borderSep + strings.Join(rowParts, borderSep) + borderSep
}
