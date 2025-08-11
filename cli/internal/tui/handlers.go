package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// handleMouse processes mouse events
func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch msg.Action {
	case tea.MouseActionPress:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.viewport.ScrollUp(3)
			return m, nil
		case tea.MouseButtonWheelDown:
			m.viewport.ScrollDown(3)
			return m, nil
		case tea.MouseButtonLeft, tea.MouseButtonRight, tea.MouseButtonMiddle:
			// Handle mouse clicks - for now just ignore them
			return m, nil
		}
	case tea.MouseActionMotion:
		// Handle mouse motion - ignore to prevent character input
		return m, nil
	}
	// Return early for any other mouse event to prevent it from being processed as key input
	return m, nil
}

// handleWindowResize processes window resize events
func (m Model) handleWindowResize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width = msg.Width
	m.height = msg.Height

	// Reserve space for prompt and status bar
	outputHeight := m.height - promptHeight - statusBarHeight
	if outputHeight < promptHeight {
		outputHeight = promptHeight
	}

	m.viewport.Width = m.width - viewportPadding
	m.viewport.Height = outputHeight

	// Update textinput width to match terminal width
	m.textInput.Width = m.width - inputPadding

	m.ready = true
	return m, nil
}

// handleKey processes keyboard events
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		m.quitting = true
		m.saveHistoryOnExit()
		return m, tea.Quit
	case tea.KeyEnter:
		return m.handleEnterKey()
	case tea.KeyUp:
		return m.handleUpKey()
	case tea.KeyDown:
		return m.handleDownKey()
	case tea.KeyPgUp:
		m.viewport.HalfPageUp()
		return m, nil
	case tea.KeyPgDown:
		m.viewport.HalfPageDown()
		return m, nil
	case tea.KeyHome:
		m.viewport.GotoTop()
		return m, nil
	case tea.KeyEnd:
		m.viewport.GotoBottom()
		return m, nil
	case tea.KeyCtrlU:
		m.viewport.HalfPageUp()
		return m, nil
	case tea.KeyCtrlD:
		m.viewport.HalfPageDown()
		return m, nil
	case tea.KeyShiftUp:
		// Shift+Up for line-by-line scrolling up
		m.viewport.ScrollUp(1)
		return m, nil
	case tea.KeyShiftDown:
		// Shift+Down for line-by-line scrolling down
		m.viewport.ScrollDown(1)
		return m, nil
	default:
		// Handle special keys based on current mode
		if !m.isMode(ModeNormal) {
			return m.handleSpecialModeKeys(msg)
		}

		// Update text input for normal mode
		var cmd tea.Cmd
		m.textInput, cmd = m.textInput.Update(msg)
		return m, cmd
	}
}

// handleUpKey processes Up arrow key
func (m Model) handleUpKey() (tea.Model, tea.Cmd) {
	if !m.isMode(ModeNormal) {
		if m.isMode(ModeSelect) && m.selectPrompt != nil {
			if m.selectPrompt.Cursor > 0 {
				m.selectPrompt.Cursor--
			}
		}
		return m, nil
	}

	// History navigation in normal mode
	if m.historyIndex > 0 {
		m.historyIndex--
		m.textInput.SetValue(m.history[m.historyIndex])
		m.textInput.CursorEnd()
	}
	return m, nil
}

// handleDownKey processes Down arrow key
func (m Model) handleDownKey() (tea.Model, tea.Cmd) {
	if !m.isMode(ModeNormal) {
		if m.isMode(ModeSelect) && m.selectPrompt != nil {
			if m.selectPrompt.Cursor < len(m.selectPrompt.Options)-1 {
				m.selectPrompt.Cursor++
			}
		}
		return m, nil
	}

	// History navigation in normal mode
	if m.historyIndex < len(m.history) {
		m.historyIndex++
		if m.historyIndex == len(m.history) {
			m.textInput.SetValue("")
		} else {
			m.textInput.SetValue(m.history[m.historyIndex])
			m.textInput.CursorEnd()
		}
	}
	return m, nil
}

// handleSpecialModeKeys processes keys in special modes
func (m Model) handleSpecialModeKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		// Exit any special mode
		m.setMode(ModeNormal)
		m.confirmationPrompt = nil
		m.authSelectionPrompt = nil
		m.apiKeyPrompt = nil
		m.selectPrompt = nil
		m.textInput.SetValue("")
		return m, nil
	default:
		// Update text input for non-select modes
		if !m.isMode(ModeSelect) {
			var cmd tea.Cmd
			m.textInput, cmd = m.textInput.Update(msg)
			return m, cmd
		}
	}
	return m, nil
}

// handleUIMessage processes UI messages
func (m Model) handleUIMessage(msg UIMessage) (tea.Model, tea.Cmd) {
	// Calculate available width for text (accounting for padding and borders)
	availableWidth := m.viewport.Width - 2 // Account for border/padding
	if availableWidth < 20 {
		availableWidth = 20 // Minimum width
	}

	// Wrap the text to fit the viewport width
	wrappedLines := wrapText(msg.Content, availableWidth)

	// Style each wrapped line and add to content
	for _, line := range wrappedLines {
		m.content = append(m.content, m.styleLogMessage(line))
	}

	// Update viewport content
	m.viewport.SetContent(strings.Join(m.content, "\n"))

	// Auto-scroll to bottom
	m.viewport.GotoBottom()

	return m, nil
}
