package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
	vp := viewport.New(120, 20)

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

	vp.SetContent(strings.Join(initialContent, "\n"))

	// Initialize text input
	ti := textinput.New()
	ti.Focus()
	ti.CharLimit = 0 // No limit
	ti.Width = 120
	ti.Prompt = "" // Remove the default prompt since we'll render our own

	// Style the textinput to match our theme
	ti.TextStyle = inputStyle
	ti.Cursor.Style = lipgloss.NewStyle().Foreground(primaryColor).Bold(true)
	ti.Cursor.TextStyle = lipgloss.NewStyle().Background(primaryColor).Foreground(backgroundColor)
	ti.Cursor.SetMode(cursor.CursorStatic) // Use a static block cursor

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
	default:
		// Update spinner if active
		if m.spinnerActive {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil
	}
}

func (m Model) View() string {
	if !m.ready {
		return lipgloss.NewStyle().
			Foreground(textColor).
			Background(backgroundColor).
			Padding(1).
			Render("Loading...")
	}

	if m.quitting {
		return lipgloss.NewStyle().
			Foreground(successColor).
			Background(backgroundColor).
			Padding(1).
			Render("Goodbye! 👋")
	}

	// Output view (top panel) with scroll indicators
	viewportContent := m.viewport.View()

	// Add scroll indicators if content is scrollable
	scrollInfo := ""
	extraHeight := 0
	if m.viewport.TotalLineCount() > m.viewport.Height {
		scrollPercent := int((float64(m.viewport.YOffset) / float64(m.viewport.TotalLineCount()-m.viewport.Height)) * 100)
		if scrollPercent < 0 {
			scrollPercent = 0
		}
		if scrollPercent > 100 {
			scrollPercent = 100
		}

		// Show current line position and total lines
		currentLine := m.viewport.YOffset + 1
		totalLines := m.viewport.TotalLineCount()

		// Create scroll info text with more detail
		scrollText := lipgloss.JoinHorizontal(lipgloss.Left,
			"🖱️ Mouse wheel • PgUp/PgDown • Ctrl+U/D • Home/End • Shift+↑/↓ • ",
			lipgloss.NewStyle().Foreground(primaryColor).Bold(true).Render(fmt.Sprintf("Line %d/%d", currentLine, totalLines)),
			" • ",
			lipgloss.NewStyle().Foreground(primaryColor).Bold(true).Render(fmt.Sprintf("%d%%", scrollPercent)))

		scrollInfo = lipgloss.NewStyle().
			Foreground(mutedColor).
			Align(lipgloss.Right).
			Width(m.width - 6).
			Render(scrollText)
		extraHeight = 1
	}

	outputContent := viewportContent
	if scrollInfo != "" {
		outputContent = lipgloss.JoinVertical(lipgloss.Left, viewportContent, scrollInfo)
	}

	outputView := outputViewStyle.
		Width(m.width - 4).
		Height(m.viewport.Height + 2 + extraHeight).
		Render(outputContent)

	// Prompt view (bottom panel)
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
		if m.isMode(ModeAPIKey) {
			// For API key mode, create a masked version
			maskedInput := strings.Repeat("*", len(m.textInput.Value()))
			inputWithCursor = inputStyle.Render(maskedInput)
		} else {
			// Use the textinput component's view
			inputWithCursor = m.textInput.View()
		}
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

	// Add spinner view if active
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

	// Combine views vertically
	return lipgloss.JoinVertical(lipgloss.Left, views...)
}
