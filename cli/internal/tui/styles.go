package tui

import "github.com/charmbracelet/lipgloss/v2"

var (
	// Dark theme colors
	primaryColor    = lipgloss.Color("#05B55E") // Green as used in our branding
	secondaryColor  = lipgloss.Color("#7C3AED") // Purple
	accentColor     = lipgloss.Color("#F59E0B") // Amber
	textColor       = lipgloss.Color("#F3F4F6") // Light gray
	mutedColor      = lipgloss.Color("#9CA3AF") // Gray
	backgroundColor = lipgloss.Color("#111827") // Dark gray
	borderColor     = lipgloss.Color("#374151") // Medium gray
	errorColor      = lipgloss.Color("#EF4444") // Red
	successColor    = lipgloss.Color("#05B55E") // Green
	warningColor    = lipgloss.Color("#F59E0B") // Amber

	// Output view styles
	outputViewStyle = lipgloss.NewStyle().
			Background(backgroundColor).
			Foreground(textColor).
			Padding(1, 2).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(borderColor)

	// Prompt view styles
	promptViewStyle = lipgloss.NewStyle().
			Background(backgroundColor).
			Foreground(textColor).
			Padding(0, 2, 1, 2).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(borderColor)

	// Prompt prefix styles
	promptStyle = lipgloss.NewStyle().
			Foreground(primaryColor).
			Bold(true)

	confirmationPromptStyle = lipgloss.NewStyle().
				Foreground(warningColor).
				Bold(true)

	// Input text style
	inputStyle = lipgloss.NewStyle().
			Foreground(textColor)

	// Cursor styles
	cursorStyle = lipgloss.NewStyle().
			Background(primaryColor).
			Foreground(backgroundColor).
			Bold(true)
	// Header style
	headerStyle = lipgloss.NewStyle().
			Foreground(primaryColor).
			Bold(true).
			Padding(0, 2)

	// Log message styles
	logStyle = lipgloss.NewStyle().
			Foreground(textColor)

	errorLogStyle = lipgloss.NewStyle().
			Foreground(errorColor)

	successLogStyle = lipgloss.NewStyle().
			Foreground(successColor)

	warningLogStyle = lipgloss.NewStyle().
			Foreground(warningColor)

	// Status bar style
	statusBarStyle = lipgloss.NewStyle().
			Background(borderColor).
			Foreground(textColor).
			Padding(0, 1)

	// Table styles
	tableHeaderStyle = lipgloss.NewStyle().
				Foreground(primaryColor).
				Bold(true)

	tableBorderStyle = lipgloss.NewStyle().
				Foreground(borderColor)

	tableKeyStyle = lipgloss.NewStyle().
			Foreground(accentColor).
			Bold(true)

	tableValueStyle = lipgloss.NewStyle().
			Foreground(textColor)

	// List styles
	listItemStyle = lipgloss.NewStyle().
			Foreground(textColor)

	listBulletStyle = lipgloss.NewStyle().
			Foreground(primaryColor).
			Bold(true)

	listContainerStyle = lipgloss.NewStyle().
				Background(backgroundColor).
				Border(lipgloss.RoundedBorder()).
				BorderForeground(borderColor).
				Padding(1, 2).
				Margin(0, 1)

	// Text selection styles
	selectionStyle = lipgloss.NewStyle().
			Background(primaryColor).
			Foreground(backgroundColor).
			Bold(true)

	selectionIndicatorStyle = lipgloss.NewStyle().
				Foreground(primaryColor).
				Bold(true)

	copyFeedbackStyle = lipgloss.NewStyle().
				Foreground(successColor).
				Bold(true)
)
