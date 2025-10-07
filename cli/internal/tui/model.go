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

type UIMode int

const (
	ModeNormal UIMode = iota
	ModeConfirmation
	ModeAuthSelection
	ModeAPIKey
	ModeSelect
	ModeText
	ModeSearch
)

const (
	maxHistoryLength = 500
	promptHeight     = 5
	statusBarHeight  = 1
)

type SelectionState struct {
	Active        bool
	StartX        int
	StartY        int
	EndX          int
	EndY          int
	Content       []string
	LastAction    string
	DragStartLine int
	DragStartCol  int
}

type SlashCommand struct {
	Command     string
	Description string
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
	content             []string
	spinner             spinner.Model
	spinnerActive       bool
	spinnerMessage      string
	currentDir          string
	lastContentLen      int
	autoScrollEnabled   bool

	selection            SelectionState
	mousePressed         bool
	expandedRemediations map[int]bool
	currentError         *ErrorDisplayMessage
	errorStartLine       int
	errorEndLine         int
	showSlashCommands    bool
	slashCommandCursor   int
	availableCommands    []SlashCommand

	// Search state
	searchQuery       string
	searchMatches     []SearchMatch
	currentMatchIndex int
}

type SearchMatch struct {
	LineIndex int
	StartCol  int
	EndCol    int
}

func (m *Model) setMode(mode UIMode) {
	m.currentMode = mode
}

func (m Model) isMode(mode UIMode) bool {
	return m.currentMode == mode
}

func NewModel(agent *agent.Agent) Model {
	vp := viewport.New()
	vp.MouseWheelEnabled = true
	vp.MouseWheelDelta = 3
	vp.SoftWrap = false

	vp.SetWidth(80)
	vp.SetHeight(20)

	vp.Style = outputViewStyle

	currentDir, err := os.Getwd()
	if err != nil {
		currentDir = "unknown"
	}

	banner := getBanner()
	greeting := greetUser()

	// Split banner into individual lines (it contains embedded newlines)
	var initialContent []string
	bannerLines := strings.Split(headerStyle.Render(banner), "\n")
	initialContent = append(initialContent, bannerLines...)
	initialContent = append(initialContent, "")
	initialContent = append(initialContent, logStyle.Render(greeting))
	initialContent = append(initialContent, "")
	initialContent = append(initialContent, logStyle.Render("Type 'exit' or press Ctrl+C to quit."))
	initialContent = append(initialContent, "")

	vp.SetContentLines(initialContent)

	ti := textinput.New()
	ti.Prompt = ""
	ti.CharLimit = 0
	ti.VirtualCursor = true
	ti.Focus()

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(primaryColor)

	m := Model{
		agent:                agent,
		viewport:             vp,
		textInput:            ti,
		ready:                false,
		history:              []string{},
		historyIndex:         0,
		historyFile:          "/tmp/.prodcli_app_history",
		currentMode:          ModeNormal,
		content:              initialContent,
		spinner:              s,
		spinnerActive:        false,
		currentDir:           currentDir,
		autoScrollEnabled:    true,
		showSlashCommands:    false,
		slashCommandCursor:   0,
		expandedRemediations: make(map[int]bool),
	}

	// Load slash commands from agent
	if agent != nil {
		agentCommands := agent.GetAvailableSlashCommands()
		m.availableCommands = make([]SlashCommand, len(agentCommands))
		for i, cmd := range agentCommands {
			m.availableCommands[i] = SlashCommand{
				Command:     cmd.Name,
				Description: cmd.Description,
			}
		}
	}

	m.loadHistory()
	return m
}

func (m *Model) SetProgram(program *tea.Program) {
	m.program = program
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
	case tea.MouseWheelMsg:
		// IMPORTANT: Set viewport content before processing scroll
		// The viewport needs content to calculate scroll bounds
		viewportContent := m.renderViewportContent()
		m.viewport.SetContent(viewportContent)

		// Now handle the scroll
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		m.autoScrollEnabled = m.viewport.AtBottom()

		return m, cmd
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
		if msg.Success {
			m.selection.LastAction = "Copied to clipboard"
		} else {
			m.selection.LastAction = "Copy failed: " + msg.Error
		}
		return m, nil
	case ErrorDisplayMessage:
		return m.handleErrorDisplayMessage(msg)
	case SuccessDisplayMessage:
		return m.handleSuccessDisplayMessage(msg)
	case ClearScreenMsg:
		return m.handleClearScreen()
	case QuitMsg:
		m.quitting = true
		m.saveHistoryOnExit()
		return m, tea.Quit
	case SearchMsg:
		m.setMode(ModeSearch)
		m.textInput.SetValue("")
		m.textInput.Placeholder = "Search..."
		return m, nil
	case tea.PasteMsg:
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

		// Don't pass mouse events to viewport here - they're handled above
		// This prevents double-handling of MouseWheelMsg
		return m, nil
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

	viewportContent := m.renderViewportContent()

	contentChanged := len(m.content) != m.lastContentLen
	m.lastContentLen = len(m.content)

	m.viewport.SetContent(viewportContent)

	if contentChanged && m.autoScrollEnabled {
		m.viewport.GotoBottom()
	}

	outputView := m.viewport.View()

	var promptText string
	var promptPrefix string
	var slashCommandMenu string

	if m.isMode(ModeConfirmation) && m.confirmationPrompt != nil {
		promptPrefix = confirmationPromptStyle.Render(m.confirmationPrompt.Message + " (y/n)")
	} else if m.isMode(ModeAuthSelection) && m.authSelectionPrompt != nil {
		promptPrefix = confirmationPromptStyle.Render(m.authSelectionPrompt.Message)
	} else if m.isMode(ModeAPIKey) && m.apiKeyPrompt != nil {
		promptPrefix = confirmationPromptStyle.Render(m.apiKeyPrompt.Message)
	} else if m.isMode(ModeText) && m.textPrompt != nil {
		promptPrefix = confirmationPromptStyle.Render(m.textPrompt.Message)
	} else if m.isMode(ModeSelect) && m.selectPrompt != nil {
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
	} else if m.isMode(ModeSearch) {
		searchInfo := "🔍 Search"
		if m.searchQuery != "" && len(m.searchMatches) > 0 {
			searchInfo = fmt.Sprintf("🔍 Search: %d/%d matches (Ctrl+N: next, Ctrl+P: prev, Esc: exit)",
				m.currentMatchIndex+1, len(m.searchMatches))
		} else if m.searchQuery != "" {
			searchInfo = "🔍 Search: No matches (Esc: exit)"
		}
		promptPrefix = confirmationPromptStyle.Render(searchInfo)
	} else {
		promptPrefix = promptStyle.Render("❯")
	}

	// Build slash command menu if visible
	if m.showSlashCommands && m.isMode(ModeNormal) {
		filtered := m.getFilteredSlashCommands()
		if len(filtered) > 0 {
			var menuText strings.Builder
			for i, cmd := range filtered {
				if i == m.slashCommandCursor {
					menuText.WriteString(fmt.Sprintf("❯ %s - %s\n", cmd.Command, cmd.Description))
				} else {
					menuText.WriteString(fmt.Sprintf("  %s - %s\n", cmd.Command, cmd.Description))
				}
			}
			menuText.WriteString("Use ↑/↓ to navigate, Tab/Enter to select, Esc to cancel")
			slashCommandMenu = confirmationPromptStyle.Render(menuText.String())
		}
	}

	var inputWithCursor string
	if m.isMode(ModeSelect) {
		inputWithCursor = ""
	} else {
		inputWithCursor = m.textInput.View()
	}

	promptText = promptPrefix + " " + inputWithCursor

	promptView := promptViewStyle.
		Width(m.width - 4).
		Render(promptText)

	statusBarContent := m.formatCurrentDir()
	statusBarView := statusBarStyle.
		Width(m.width).
		Render(statusBarContent)

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

		currentLine := m.viewport.YOffset + 1

		// Simplified scroll indicator (selection info moved to status bar)
		scrollText := lipgloss.JoinHorizontal(lipgloss.Left,
			"🖱️ Mouse wheel • PgUp/PgDown • Ctrl+U/D • Home/End • Shift+↑/↓ • Ctrl+A • ",
			lipgloss.NewStyle().Foreground(primaryColor).Bold(true).Render(fmt.Sprintf("Line %d/%d", currentLine, totalLines)),
			" • ",
			lipgloss.NewStyle().Foreground(primaryColor).Bold(true).Render(fmt.Sprintf("%d%%", scrollPercent)))

		scrollIndicator = lipgloss.NewStyle().
			Foreground(mutedColor).
			Align(lipgloss.Right).
			Width(m.width - 6).
			Render(scrollText)

		views := []string{outputView, scrollIndicator}

		if m.spinnerActive {
			spinnerText := m.spinner.View() + " " + m.spinnerMessage
			spinnerView := lipgloss.NewStyle().
				Width(m.width-4).
				Padding(0, 2).
				Render(spinnerText)
			views = append(views, spinnerView)
		}

		// Add slash command menu if visible
		if slashCommandMenu != "" {
			views = append(views, slashCommandMenu)
		}

		views = append(views, promptView, statusBarView)

		var cursor *tea.Cursor
		if m.textInput.Focused() && !m.isMode(ModeSelect) {
			cursor = m.textInput.Cursor()
			if cursor != nil {
				yOffset := 0

				yOffset += m.viewport.Height() + 4

				yOffset += 1

				if m.spinnerActive {
					yOffset += 1
				}

				yOffset += 1

				promptPrefixLen := 0
				if m.isMode(ModeNormal) {
					promptPrefixLen = 2
				} else {
					promptPrefixLen = 2
				}

				xOffset := promptPrefixLen + 2

				cursor.Y += yOffset
				cursor.X += xOffset
			}
		}

		return lipgloss.JoinVertical(lipgloss.Left, views...), cursor
	}

	views := []string{outputView}

	if m.spinnerActive {
		spinnerText := m.spinner.View() + " " + m.spinnerMessage
		spinnerView := lipgloss.NewStyle().
			Width(m.width-4).
			Padding(0, 2).
			Render(spinnerText)
		views = append(views, spinnerView)
	}

	// Add slash command menu if visible
	if slashCommandMenu != "" {
		views = append(views, slashCommandMenu)
	}

	views = append(views, promptView, statusBarView)

	var cursor *tea.Cursor
	if m.textInput.Focused() && !m.isMode(ModeSelect) {
		cursor = m.textInput.Cursor()
		if cursor != nil {
			yOffset := 0

			yOffset += m.viewport.Height() + 4

			if m.spinnerActive {
				yOffset += 1
			}

			yOffset += 1

			promptPrefixLen := 0
			if m.isMode(ModeNormal) {
				promptPrefixLen = 2
			} else {
				promptPrefixLen = 2
			}

			xOffset := promptPrefixLen + 2

			cursor.Y += yOffset
			cursor.X += xOffset
		}
	}

	return lipgloss.JoinVertical(lipgloss.Left, views...), cursor
}

func (m Model) renderViewportContent() string {
	if len(m.content) == 0 {
		return ""
	}

	// If no special rendering needed, return plain content
	if !m.selection.Active && len(m.searchMatches) == 0 {
		return strings.Join(m.content, "\n")
	}

	lines := make([]string, len(m.content))
	copy(lines, m.content)

	// Handle search highlighting (simplified - just highlight all matches)
	if len(m.searchMatches) > 0 {
		// Group matches by line
		matchesByLine := make(map[int][]SearchMatch)
		for matchIdx, match := range m.searchMatches {
			match.StartCol = match.StartCol // Keep match data
			matchesByLine[match.LineIndex] = append(matchesByLine[match.LineIndex], m.searchMatches[matchIdx])
		}

		// Apply highlighting to each line with matches
		for lineIdx, lineMatches := range matchesByLine {
			if lineIdx >= len(lines) {
				continue
			}

			cleanLine := stripANSI(lines[lineIdx])
			lines[lineIdx] = m.highlightSearchMatches(cleanLine, lineMatches, lineIdx)
		}

		if !m.selection.Active {
			return strings.Join(lines, "\n")
		}
	}

	// Handle selection rendering

	startY, endY := m.selection.StartY, m.selection.EndY
	startX, endX := m.selection.StartX, m.selection.EndX

	if startY > endY || (startY == endY && startX > endX) {
		startY, endY = endY, startY
		startX, endX = endX, startX
	}

	for i := range lines {
		if i < startY || i > endY {
			continue
		}

		cleanLine := stripANSI(lines[i])
		if len(cleanLine) == 0 {
			continue
		}

		var renderedLine strings.Builder
		runes := []rune(cleanLine)

		for j := range len(runes) {
			isSelected := false
			if i == startY && i == endY {
				isSelected = j >= startX && j < endX
			} else if i == startY {
				isSelected = j >= startX
			} else if i == endY {
				isSelected = j < endX
			} else {
				isSelected = true
			}

			if isSelected {
				renderedLine.WriteString(selectionStyle.Render(string(runes[j])))
			} else {
				renderedLine.WriteRune(runes[j])
			}
		}

		lines[i] = renderedLine.String()
	}

	return strings.Join(lines, "\n")
}

func (m Model) highlightSearchMatches(cleanLine string, matches []SearchMatch, lineIdx int) string {
	runes := []rune(cleanLine)
	if len(runes) == 0 {
		return cleanLine
	}

	var result strings.Builder
	lastEnd := 0

	for _, match := range matches {
		if match.StartCol < 0 || match.StartCol >= len(runes) {
			continue
		}

		endCol := match.EndCol
		if endCol > len(runes) {
			endCol = len(runes)
		}

		// Add text before match
		if match.StartCol > lastEnd {
			result.WriteString(string(runes[lastEnd:match.StartCol]))
		}

		// Add highlighted match
		matchText := string(runes[match.StartCol:endCol])

		// Check if this is the current match
		isCurrentMatch := false
		for idx, sm := range m.searchMatches {
			if sm.LineIndex == lineIdx && sm.StartCol == match.StartCol && idx == m.currentMatchIndex {
				isCurrentMatch = true
				break
			}
		}

		if isCurrentMatch {
			// Current match - use bright purple/magenta highlight
			result.WriteString(lipgloss.NewStyle().
				Background(secondaryColor).
				Foreground(backgroundColor).
				Bold(true).
				Render(matchText))
		} else {
			// Other matches - use muted purple
			result.WriteString(lipgloss.NewStyle().
				Background(lipgloss.Color("#9333EA")).
				Foreground(backgroundColor).
				Render(matchText))
		}

		lastEnd = endCol
	}

	// Add remaining text after last match
	if lastEnd < len(runes) {
		result.WriteString(string(runes[lastEnd:]))
	}

	return result.String()
}
