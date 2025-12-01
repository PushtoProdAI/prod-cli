package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss/v2"
)

// createDeploymentConfigTable creates a table for deployment configuration
func (m Model) createDeploymentConfigTable(plan PlanDisplayMessage) string {
	rows := [][]string{
		{"Action", plan.Action},
	}

	if len(plan.DetectedPlatforms) > 1 {
		platformList := strings.Join(plan.DetectedPlatforms, ", ")
		rows = append(rows, []string{"Detected Platforms", platformList})
	} else {
		rows = append(rows, []string{"Platform", plan.Platform})
	}

	rows = append(rows, [][]string{
		{"Source", plan.Source},
		{"Name", plan.Name},
		{"Language", plan.Language},
	}...)

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

// createPricingTable creates a table for pricing information
func (m Model) createPricingTable(pricing PricingInfo) string {
	if len(pricing.Services) == 0 {
		return ""
	}

	// Create rows for pricing
	rows := make([][]string, len(pricing.Services)+1) // +1 for total row

	// Service rows
	for i, service := range pricing.Services {
		var description string
		if service.Plan != "" {
			if service.Storage > 0 {
				description = fmt.Sprintf("%s (%s, %dGB storage)", service.Name, service.Plan, service.Storage)
			} else {
				description = fmt.Sprintf("%s (%s)", service.Name, service.Plan)
			}
		} else {
			description = service.Name
		}

		// Format cost
		var costStr string
		if service.Cost > 0 {
			costStr = fmt.Sprintf("$%.2f/month", service.Cost)
		} else {
			costStr = "$0.00/month"
		}

		rows[i] = []string{description, costStr}
	}

	// Total row
	rows[len(pricing.Services)] = []string{"Total", fmt.Sprintf("$%.2f/month", pricing.Total)}

	columnWidths := []int{50, 22}
	table := m.createTable(rows, columnWidths)

	styledTable := lipgloss.NewStyle().
		Margin(0, 0, 1, 0).
		Render(table)

	return styledTable
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

// createDeploymentHistoryTable creates a table for deployment history
func (m Model) createDeploymentHistoryTable(deployments []DeploymentHistoryEntry) string {
	if len(deployments) == 0 {
		return ""
	}

	// Create rows for deployments
	rows := make([][]string, len(deployments))
	for i, dep := range deployments {
		// Format the status with an icon
		statusDisplay := formatStatus(dep.Status)

		// Format duration
		durationStr := formatDuration(dep.Duration)

		// Format the date nicely
		formattedDate := formatDeploymentDate(dep.CompletedAt)

		// Format platform name (title case, but AWS stays uppercase)
		platformName := formatPlatformName(dep.Platform)

		rows[i] = []string{dep.ResourceName, platformName, statusDisplay, formattedDate, durationStr}
	}

	// Column widths: Name, Platform, Status, Date, Duration
	columnWidths := []int{20, 8, 12, 18, 10}
	table := m.createTable(rows, columnWidths)

	styledTable := lipgloss.NewStyle().
		Margin(0, 0, 1, 0).
		Render(table)

	return styledTable
}

// formatStatus formats the deployment status with appropriate styling
func formatStatus(status string) string {
	switch strings.ToLower(status) {
	case "success":
		return "✅ Success"
	case "failed":
		return "❌ Failed"
	case "in_progress", "running":
		return "🔄 In Progress"
	case "pending":
		return "⏳ Pending"
	default:
		return status
	}
}

// formatDuration formats the duration in seconds to a human-readable string
func formatDuration(seconds int) string {
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	} else if seconds < 3600 {
		mins := seconds / 60
		secs := seconds % 60
		return fmt.Sprintf("%dm %ds", mins, secs)
	} else {
		hours := seconds / 3600
		mins := (seconds % 3600) / 60
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
}

// formatPlatformName formats the platform name with proper casing
func formatPlatformName(platform string) string {
	lower := strings.ToLower(platform)
	switch lower {
	case "aws":
		return "AWS"
	default:
		return strings.Title(lower)
	}
}

// formatDeploymentDate formats the deployment date to a human-readable string
func formatDeploymentDate(dateStr string) string {
	// Try to parse various common formats
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05.999999Z07:00",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05Z",
	}

	var parsedTime time.Time
	var err error

	for _, format := range formats {
		parsedTime, err = time.Parse(format, dateStr)
		if err == nil {
			break
		}
	}

	if err != nil {
		// If we can't parse, return the original string (truncated if too long)
		if len(dateStr) > 18 {
			return dateStr[:15] + "..."
		}
		return dateStr
	}

	// Convert to local timezone for display
	parsedTime = parsedTime.Local()

	// Format as "Jan 2, 3:04 PM"
	now := time.Now()

	// If it's today, just show the time
	if parsedTime.Year() == now.Year() && parsedTime.YearDay() == now.YearDay() {
		return "Today " + parsedTime.Format("3:04 PM")
	}

	// If it's yesterday
	yesterday := now.AddDate(0, 0, -1)
	if parsedTime.Year() == yesterday.Year() && parsedTime.YearDay() == yesterday.YearDay() {
		return "Yesterday " + parsedTime.Format("3:04PM")
	}

	// If it's within the last 7 days, show day of week
	daysAgo := int(now.Sub(parsedTime).Hours() / 24)
	if daysAgo < 7 && daysAgo > 0 {
		return parsedTime.Format("Mon 3:04 PM")
	}

	// If it's this year, show month and day
	if parsedTime.Year() == now.Year() {
		return parsedTime.Format("Jan 2, 3:04PM")
	}

	// Otherwise show full date
	return parsedTime.Format("Jan 2 '06")
}
