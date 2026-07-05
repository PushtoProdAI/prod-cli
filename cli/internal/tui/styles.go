package tui

import "github.com/charmbracelet/lipgloss/v2"

var (
	// Accent palette — these read acceptably on both light and dark terminals,
	// so they are kept as fixed colors regardless of the terminal theme.
	primaryColor   = lipgloss.Color("#05B55E") // Green as used in our branding
	secondaryColor = lipgloss.Color("#7C3AED") // Purple
	accentColor    = lipgloss.Color("#F59E0B") // Amber
	textColor      = lipgloss.Color("#F3F4F6") // Light gray — only used on fixed dark surfaces (e.g. status bar)
	mutedColor     = lipgloss.Color("#9CA3AF") // Gray
	borderColor    = lipgloss.Color("#374151") // Medium gray
	errorColor     = lipgloss.Color("#EF4444") // Red
	successColor   = lipgloss.Color("#05B55E") // Green
	warningColor   = lipgloss.Color("#F59E0B") // Amber

	// onAccentColor is a fixed dark foreground painted ON TOP of a bright accent
	// background (chips/cursor/selection). Dark text on a bright accent is
	// readable on both light and dark terminals, so this stays fixed. It is
	// intentionally distinct from any notion of "app background".
	onAccentColor = lipgloss.Color("#111827") // Dark gray

	// Output view styles. No Background/Foreground is set so the terminal's own
	// theme (background + default foreground) shows through instead of a forced
	// dark box.
	outputViewStyle = lipgloss.NewStyle().
			Padding(1, 2).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(borderColor)

	// Prompt view styles. Terminal-native background/foreground (see above).
	promptViewStyle = lipgloss.NewStyle().
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

	// Input text style. Foreground left unset so typed text uses the terminal's
	// default foreground (readable on both light and dark themes).
	inputStyle = lipgloss.NewStyle()

	// Cursor styles. Dark text on the bright primary accent — kept fixed.
	cursorStyle = lipgloss.NewStyle().
			Background(primaryColor).
			Foreground(onAccentColor).
			Bold(true)
	// Header style
	headerStyle = lipgloss.NewStyle().
			Foreground(primaryColor).
			Bold(true).
			Padding(0, 2)

	// Log message styles. Default log text inherits the terminal foreground.
	logStyle = lipgloss.NewStyle()

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

	// Table values inherit the terminal foreground (sit on terminal surface).
	tableValueStyle = lipgloss.NewStyle()

	// List styles. Items inherit the terminal foreground.
	listItemStyle = lipgloss.NewStyle()

	listBulletStyle = lipgloss.NewStyle().
			Foreground(primaryColor).
			Bold(true)

	// Container background left unset so the terminal theme shows through.
	listContainerStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(borderColor).
				Padding(1, 2).
				Margin(0, 1)

	// Text selection styles. Dark text on the bright primary accent — fixed.
	selectionStyle = lipgloss.NewStyle().
			Background(primaryColor).
			Foreground(onAccentColor).
			Bold(true)

	selectionIndicatorStyle = lipgloss.NewStyle().
				Foreground(primaryColor).
				Bold(true)

	copyFeedbackStyle = lipgloss.NewStyle().
				Foreground(successColor).
				Bold(true)

	// Error display styles
	errorHeaderStyle = lipgloss.NewStyle().
				Foreground(errorColor).
				Bold(true)

	warningHeaderStyle = lipgloss.NewStyle().
				Foreground(warningColor).
				Bold(true)

	errorSummaryStyle = lipgloss.NewStyle().
				Padding(1, 2)

	errorContainerStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(errorColor).
				Padding(1, 2).
				Margin(0, 1)

	remediationHeaderStyle = lipgloss.NewStyle().
				Foreground(warningColor).
				Bold(true)

	remediationItemStyle = lipgloss.NewStyle().
				Padding(0, 2)

	codeBlockStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#1F2937")).
			Foreground(lipgloss.Color("#A5F3FC")).
			Padding(0, 1).
			Margin(0, 2)

	expandIconStyle = lipgloss.NewStyle().
			Foreground(accentColor).
			Bold(true)
)
