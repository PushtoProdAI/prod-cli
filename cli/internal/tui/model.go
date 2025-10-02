package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/v2/spinner"
	"github.com/charmbracelet/bubbles/v2/textinput"
	"github.com/charmbracelet/bubbles/v2/viewport"
	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/charmbracelet/lipgloss/v2"
	"github.com/meroxa/prod/cli/internal/agent"
)

// UIMode represents the current mode of the TUI
type UIMode int

const (
	ModeNormal UIMode = iota
	ModeConfirmation
	ModeAuthSelection
	ModeAPIKey
	ModeSelect
	ModeText
)

// Constants for UI layout and history
const (
	maxHistoryLength = 500
	viewportPadding  = 4
	inputPadding     = 8
	promptHeight     = 5
	statusBarHeight  = 1
)

// SelectionState manages text selection in the viewport
type SelectionState struct {
	Active     bool     // Whether selection is currently active
	StartLine  int      // Starting line (0-based relative to viewport content)
	StartCol   int      // Starting column (0-based)
	EndLine    int      // Ending line (0-based relative to viewport content)
	EndCol     int      // Ending column (0-based)
	Content    []string // Currently selected text lines
	LastAction string   // Last selection action (for UI feedback)
}

type Model struct {
	agent               *agent.Agent
	viewport            viewport.Model
	textInput           textinput.Model
	program             *tea.Program
	ready               bool
	quitting            bool
	history             []string
	historyIndex        int
	historyFile         string
	currentMode         UIMode
	confirmationPrompt  *ConfirmationPrompt
	authSelectionPrompt *AuthSelectionPrompt
	apiKeyPrompt        *APIKeyPrompt
	selectPrompt        *SelectPrompt
	textPrompt          *TextPrompt
	width               int
	height              int
	content             []string // Store raw content lines
	spinner             spinner.Model
	spinnerActive       bool
	spinnerMessage      string
	currentDir          string // Current working directory

	// Text selection state
	selection    SelectionState
	mousePressed bool
	lastMouseX   int
	lastMouseY   int
}

// setMode changes the current UI mode
func (m *Model) setMode(mode UIMode) {
	m.currentMode = mode
}

// isMode checks if the current mode matches the given mode
func (m Model) isMode(mode UIMode) bool {
	return m.currentMode == mode
}

func NewModel(agent *agent.Agent) Model {
	vp := viewport.New()
	vp.MouseWheelEnabled = true
	vp.MouseWheelDelta = 3
	vp.SoftWrap = true // Ensure soft wrapping is enabled

	// Set initial reasonable dimensions
	vp.SetWidth(80)
	vp.SetHeight(20)

	// Apply the original beautiful styling directly to the viewport
	vp.Style = outputViewStyle

	// Get current working directory
	currentDir, err := os.Getwd()
	if err != nil {
		currentDir = "unknown"
	}

	// Initialize content with banner and greeting
	banner := getBanner()
	greeting := greetUser()

	initialContent := []string{
		headerStyle.Render(banner),
		"",
		logStyle.Render(greeting),
		"",
		logStyle.Render("Type 'exit' or press Ctrl+C to quit."),
		"",
	}

	vp.SetContentLines(initialContent)

	// Initialize text input
	ti := textinput.New()
	ti.Prompt = ""           // Remove default prompt
	ti.CharLimit = 0         // No character limit
	ti.VirtualCursor = false // Use real cursor for proper rendering
	ti.Focus()

	// Initialize spinner
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(primaryColor)

	m := Model{
		agent:         agent,
		viewport:      vp,
		textInput:     ti,
		ready:         false,
		history:       []string{},
		historyIndex:  0,
		historyFile:   "/tmp/.prodcli_app_history",
		currentMode:   ModeNormal,
		content:       initialContent,
		spinner:       s,
		spinnerActive: false,
		currentDir:    currentDir,
	}
	m.loadHistory()
	return m
}

func (m *Model) SetProgram(program *tea.Program) {
	m.program = program
	// Set up the agent with TeaWriter that implements StatusWriter
	if m.agent != nil {
		teaWriter := NewTeaWriter(func(msg tea.Msg) {
			program.Send(msg)
		})
		m.agent.UIOutput = teaWriter
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.handleWindowResize(msg)
	case tea.KeyMsg:
		return m.handleKey(msg)
	case tea.MouseMsg:
		return m.handleMouse(msg)
	case UIMessage:
		return m.handleUIMessage(msg)
	case PlanDisplayMessage:
		return m.handlePlanDisplayMessage(msg)
	case ConfirmationPrompt:
		m.confirmationPrompt = &msg
		m.setMode(ModeConfirmation)
		m.textInput.SetValue("")
		return m, nil
	case AuthSelectionPrompt:
		m.authSelectionPrompt = &msg
		m.setMode(ModeAuthSelection)
		m.textInput.SetValue("")
		return m, nil
	case APIKeyPrompt:
		m.apiKeyPrompt = &msg
		m.setMode(ModeAPIKey)
		m.textInput.SetValue("")
		// Set password mode for API key input
		m.textInput.EchoMode = textinput.EchoPassword
		m.textInput.EchoCharacter = '*'
		return m, nil
	case SelectPrompt:
		m.selectPrompt = &msg
		m.setMode(ModeSelect)
		m.textInput.SetValue("")
		return m, nil
	case TextPrompt:
		m.textPrompt = &msg
		m.setMode(ModeText)
		// Pre-fill with default value if provided
		if msg.DefaultValue != "" {
			m.textInput.SetValue(msg.DefaultValue)
			m.textInput.CursorEnd()
		} else {
			m.textInput.SetValue("")
		}
		return m, nil
	case SpinnerStartMsg:
		m.spinnerActive = true
		m.spinnerMessage = msg.Message
		return m, m.spinner.Tick
	case SpinnerStopMsg:
		m.spinnerActive = false
		m.spinnerMessage = ""
		return m, nil
	case ClipboardCopyMsg:
		// Handle clipboard copy results
		if msg.Success {
			m.selection.LastAction = "Copied to clipboard"
			// Optionally clear selection after copy
			// m.clearSelection()
		} else {
			m.selection.LastAction = "Copy failed: " + msg.Error
		}
		return m, nil
	case tea.PasteMsg:
		// Handle paste events - forward to textinput if not in select mode
		if !m.isMode(ModeSelect) {
			var cmd tea.Cmd
			m.textInput, cmd = m.textInput.Update(msg)
			return m, cmd
		}
		return m, nil
	default:
		// Update spinner if active
		if m.spinnerActive {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}

		// Update viewport for any unhandled messages (like mouse events)
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
}

func (m Model) View() (string, *tea.Cursor) {
	if !m.ready {
		return lipgloss.NewStyle().
			Foreground(textColor).
			Render("Loading..."), nil
	}

	if m.quitting {
		return lipgloss.NewStyle().
			Foreground(successColor).
			Render("Goodbye! 👋"), nil
	}

	// Get viewport content with selection highlighting
	viewportContent := m.viewport.GetContent()
	contentLines := strings.Split(viewportContent, "\n")

	// Apply selection highlighting if active
	if m.selection.Active {
		contentLines = m.renderContentWithSelection(contentLines)
		// Update viewport with highlighted content
		m.viewport.SetContentLines(contentLines)
	}

	outputView := m.viewport.View()

	// Prompt view (bottom panel) - restored original styling
	var promptText string
	var promptPrefix string

	if m.isMode(ModeConfirmation) && m.confirmationPrompt != nil {
		promptPrefix = confirmationPromptStyle.Render(m.confirmationPrompt.Message + " (y/n)")
	} else if m.isMode(ModeAuthSelection) && m.authSelectionPrompt != nil {
		// Show simple selection prompt (options are shown in output area)
		promptPrefix = confirmationPromptStyle.Render(m.authSelectionPrompt.Message)
	} else if m.isMode(ModeAPIKey) && m.apiKeyPrompt != nil {
		promptPrefix = confirmationPromptStyle.Render(m.apiKeyPrompt.Message)
	} else if m.isMode(ModeText) && m.textPrompt != nil {
		promptPrefix = confirmationPromptStyle.Render(m.textPrompt.Message)
	} else if m.isMode(ModeSelect) && m.selectPrompt != nil {
		// Render select options in the prompt area
		selectText := m.selectPrompt.Message + "\n"
		for i, option := range m.selectPrompt.Options {
			if i == m.selectPrompt.Cursor {
				selectText += fmt.Sprintf("❯ %s\n", option)
			} else {
				selectText += fmt.Sprintf("  %s\n", option)
			}
		}
		selectText += "Use ↑/↓ to navigate, Enter to select"
		promptPrefix = confirmationPromptStyle.Render(selectText)
	} else {
		promptPrefix = promptStyle.Render("❯")
	}

	// Build input with cursor (mask API key input, hide input in select mode)
	var inputWithCursor string
	if m.isMode(ModeSelect) {
		// In select mode, don't show input field
		inputWithCursor = ""
	} else {
		// Always use the textinput component's view for proper cursor handling
		inputWithCursor = m.textInput.View()

		// For API key mode, we need to set up the textinput to show masked characters
		// This should be handled in the textinput configuration, not in rendering
	}

	promptText = promptPrefix + " " + inputWithCursor

	promptView := promptViewStyle.
		Width(m.width - 4).
		Render(promptText)

	// Status bar view
	statusBarContent := m.formatCurrentDir()
	statusBarView := statusBarStyle.
		Width(m.width).
		Render(statusBarContent)

	// Add scroll indicators if content is scrollable - restored original beautiful version
	var scrollIndicator string
	totalLines := m.viewport.TotalLineCount()
	viewportHeight := m.viewport.Height()
	if totalLines > viewportHeight {
		scrollPercent := int((float64(m.viewport.YOffset) / float64(totalLines-viewportHeight)) * 100)
		if scrollPercent < 0 {
			scrollPercent = 0
		}
		if scrollPercent > 100 {
			scrollPercent = 100
		}

		// Show current line position and total lines
		currentLine := m.viewport.YOffset + 1

		// Create scroll info text with selection status
		var scrollText string
		if m.selection.Active && len(m.selection.Content) > 0 {
			// Show selection info when text is selected
			selectionInfo := fmt.Sprintf("Selected: %d lines", len(m.selection.Content))
			if m.selection.LastAction != "" {
				selectionInfo += " • " + m.selection.LastAction
			}
			scrollText = lipgloss.JoinHorizontal(lipgloss.Left,
				selectionIndicatorStyle.Render("📋 "),
				selectionIndicatorStyle.Render(selectionInfo),
				" • Ctrl+C to copy • Esc to clear • ",
				lipgloss.NewStyle().Foreground(primaryColor).Bold(true).Render(fmt.Sprintf("Line %d/%d", currentLine, totalLines)))
		} else {
			// Original scroll text when no selection
			scrollText = lipgloss.JoinHorizontal(lipgloss.Left,
				"🖱️ Mouse wheel • PgUp/PgDown • Ctrl+U/D • Home/End • Shift+↑/↓ • Ctrl+A • ",
				lipgloss.NewStyle().Foreground(primaryColor).Bold(true).Render(fmt.Sprintf("Line %d/%d", currentLine, totalLines)),
				" • ",
				lipgloss.NewStyle().Foreground(primaryColor).Bold(true).Render(fmt.Sprintf("%d%%", scrollPercent)))
		}

		scrollIndicator = lipgloss.NewStyle().
			Foreground(mutedColor).
			Align(lipgloss.Right).
			Width(m.width - 6).
			Render(scrollText)

		// Restored original sophisticated layout with proper view management
		views := []string{outputView, scrollIndicator}

		// Add spinner view if active
		if m.spinnerActive {
			spinnerText := m.spinner.View() + " " + m.spinnerMessage
			spinnerView := lipgloss.NewStyle().
				Width(m.width-4).
				Padding(0, 2).
				Render(spinnerText)
			views = append(views, spinnerView)
		}

		views = append(views, promptView, statusBarView)

		// Get cursor from textinput if focused and not in select mode
		var cursor *tea.Cursor
		if m.textInput.Focused() && !m.isMode(ModeSelect) {
			cursor = m.textInput.Cursor()
			if cursor != nil {
				// Calculate cursor position offset based on layout
				// Account for: output view + scroll indicator + spinner (if active) + prompt prefix
				yOffset := 0

				// Add output view height (this includes viewport + borders/padding)
				yOffset += m.viewport.Height() + 4 // viewport + border + padding

				// Add scroll indicator line
				yOffset += 1

				// Add spinner height if active
				if m.spinnerActive {
					yOffset += 1
				}

				// Add padding for prompt area (prompt has padding internally)
				yOffset += 1

				// X offset includes prompt prefix and padding
				promptPrefixLen := 0
				if m.isMode(ModeNormal) {
					promptPrefixLen = 2 // "❯ " length
				} else {
					// For other modes, we'd calculate based on the actual prompt text
					// For now, assume similar length
					promptPrefixLen = 2
				}

				xOffset := promptPrefixLen + 2 // prompt prefix + padding from promptViewStyle

				// Apply the offset
				cursor.Position.Y += yOffset
				cursor.Position.X += xOffset
			}
		}

		return lipgloss.JoinVertical(lipgloss.Left, views...), cursor
	}

	// Layout without scroll indicator but with original styling
	views := []string{outputView}

	if m.spinnerActive {
		spinnerText := m.spinner.View() + " " + m.spinnerMessage
		spinnerView := lipgloss.NewStyle().
			Width(m.width-4).
			Padding(0, 2).
			Render(spinnerText)
		views = append(views, spinnerView)
	}

	views = append(views, promptView, statusBarView)

	// Get cursor from textinput if focused and not in select mode
	var cursor *tea.Cursor
	if m.textInput.Focused() && !m.isMode(ModeSelect) {
		cursor = m.textInput.Cursor()
		if cursor != nil {
			// Calculate cursor position offset based on layout
			// Account for: output view + spinner (if active) + prompt prefix
			yOffset := 0

			// Add output view height (this includes viewport + borders/padding)
			yOffset += m.viewport.Height() + 4 // viewport + border + padding

			// Add spinner height if active
			if m.spinnerActive {
				yOffset += 1
			}

			// Add padding for prompt area (prompt has padding internally)
			yOffset += 1

			// X offset includes prompt prefix and padding
			promptPrefixLen := 0
			if m.isMode(ModeNormal) {
				promptPrefixLen = 2 // "❯ " length
			} else {
				// For other modes, we'd calculate based on the actual prompt text
				// For now, assume similar length
				promptPrefixLen = 2
			}

			xOffset := promptPrefixLen + 2 // prompt prefix + padding from promptViewStyle

			// Apply the offset
			cursor.Position.Y += yOffset
			cursor.Position.X += xOffset
		}
	}

	return lipgloss.JoinVertical(lipgloss.Left, views...), cursor
}
