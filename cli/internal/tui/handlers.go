package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/v2/textinput"
	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// handleMouse processes mouse events including text selection
func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// Handle text selection mouse events first
	switch mouseMsg := msg.(type) {
	case tea.MouseClickMsg:
		if mouseMsg.Button == ansi.MouseLeft {
			// Start text selection
			line := m.viewportLineFromY(mouseMsg.Y)
			col := mouseMsg.X - 2 // Account for viewport padding
			if col < 0 {
				col = 0
			}

			// Clear any existing selection and start new one
			m.selection = SelectionState{
				Active:     true,
				StartLine:  line,
				StartCol:   col,
				EndLine:    line,
				EndCol:     col,
				LastAction: "Selection Started",
			}
			m.mousePressed = true
			m.lastMouseX = mouseMsg.X
			m.lastMouseY = mouseMsg.Y

			return m, nil
		}

	case tea.MouseMotionMsg:
		if m.mousePressed && m.selection.Active {
			// Update selection end point during drag
			line := m.viewportLineFromY(mouseMsg.Y)
			col := mouseMsg.X - 2 // Account for viewport padding
			if col < 0 {
				col = 0
			}

			m.selection.EndLine = line
			m.selection.EndCol = col
			m.lastMouseX = mouseMsg.X
			m.lastMouseY = mouseMsg.Y

			// Update selection content
			m.updateSelectionContent()

			return m, nil
		}

	case tea.MouseReleaseMsg:
		if m.mousePressed {
			m.mousePressed = false
			if m.selection.Active && len(m.selection.Content) > 0 {
				m.selection.LastAction = "Text Selected"
			}
			return m, nil
		}

	case tea.MouseWheelMsg:
		// Let viewport handle scrolling, but clear selection if active
		if m.selection.Active {
			m.clearSelection()
		}
		// Fall through to let viewport handle the scroll
	}

	// Let the viewport handle other mouse events (like scrolling)
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

// handleWindowResize processes window resize events
func (m Model) handleWindowResize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width = msg.Width
	m.height = msg.Height

	// Reserve space for prompt and status bar and styling
	// Account for: prompt (5) + status (1) + scroll indicator (1) + borders/padding (4)
	reservedHeight := promptHeight + statusBarHeight + 1 + 4
	outputHeight := m.height - reservedHeight
	if outputHeight < 10 {
		outputHeight = 10 // Minimum height
	}

	// Set viewport size accounting for the lipgloss border and padding that will be applied
	// outputView has Border + Padding(1,2) = 2 (top/bottom) + 4 (left/right padding)
	viewportWidth := m.width - viewportPadding - 4 // Account for lipgloss padding
	m.viewport.SetWidth(viewportWidth)
	m.viewport.SetHeight(outputHeight)

	// Update textinput width to match terminal width
	m.textInput.SetWidth(m.width - inputPadding)

	m.ready = true
	return m, nil
}

// handleKey processes keyboard events
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Check if it's a key press event
	if keyPress, ok := msg.(tea.KeyPressMsg); ok {
		switch keyPress.String() {
		case "ctrl+c":
			// If text is selected, copy to clipboard instead of quitting
			if m.selection.Active && len(m.selection.Content) > 0 {
				return m, m.copySelectionToClipboard()
			}
			// Otherwise quit as usual
			m.quitting = true
			m.saveHistoryOnExit()
			return m, tea.Quit
		case "enter":
			return m.handleEnterKey()
		case "up":
			return m.handleUpKey()
		case "down":
			return m.handleDownKey()
		case "pgup":
			m.viewport.HalfViewUp()
			return m, nil
		case "pgdown":
			m.viewport.HalfViewDown()
			return m, nil
		case "home":
			m.viewport.GotoTop()
			return m, nil
		case "end":
			m.viewport.GotoBottom()
			return m, nil
		case "ctrl+u":
			m.viewport.HalfViewUp()
			return m, nil
		case "ctrl+d":
			m.viewport.HalfViewDown()
			return m, nil
		case "shift+up":
			// Shift+Up for line-by-line scrolling up
			m.viewport.LineUp(1)
			return m, nil
		case "shift+down":
			// Shift+Down for line-by-line scrolling down
			m.viewport.LineDown(1)
			return m, nil
		case "ctrl+a":
			// Select all text
			m.selectAll()
			return m, nil
		case "esc":
			// Clear selection if active
			if m.selection.Active {
				m.clearSelection()
				return m, nil
			}
			// Fall through to default behavior
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

	// Handle other key events (like release)
	if !m.isMode(ModeNormal) {
		return m.handleSpecialModeKeys(msg)
	}

	// Update text input for normal mode
	var cmd tea.Cmd
	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
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
		m.textPrompt = nil
		m.textInput.SetValue("")
		// Restore normal echo mode
		m.textInput.EchoMode = textinput.EchoNormal
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
	// Calculate available width for text to match viewport width
	// Viewport is set to m.width - viewportPadding (4), with additional padding from styling
	availableWidth := m.viewport.Width() - 4 // Account for lipgloss padding
	if availableWidth < 20 {
		availableWidth = 20 // Minimum width
	}

	// Wrap the text to fit the viewport width
	wrappedLines := wrapText(msg.Content, availableWidth)

	// Add wrapped lines to content with basic styling
	for _, line := range wrappedLines {
		m.content = append(m.content, m.styleLogMessage(line))
	}

	// Limit content length to prevent memory issues and display problems
	if len(m.content) > maxHistoryLength {
		// Keep the last maxHistoryLength lines
		m.content = m.content[len(m.content)-maxHistoryLength:]
	}

	// Update viewport content using SetContentLines for better line management
	m.viewport.SetContentLines(m.content)

	// Auto-scroll to bottom
	m.viewport.GotoBottom()

	return m, nil
}

// handlePlanDisplayMessage processes plan display messages and renders them as a table
func (m Model) handlePlanDisplayMessage(msg PlanDisplayMessage) (tea.Model, tea.Cmd) {
	// Add the summary first with basic styling
	m.content = append(m.content, m.styleLogMessage(msg.Summary))
	m.content = append(m.content, "")

	// Format the plan information as a table
	tableContent := m.formatPlanAsTable(msg)

	// Add each line of the table to content
	for _, line := range strings.Split(tableContent, "\n") {
		if line != "" {
			m.content = append(m.content, line)
		}
	}

	// Limit content length to prevent memory issues and display problems
	if len(m.content) > maxHistoryLength {
		// Keep the last maxHistoryLength lines
		m.content = m.content[len(m.content)-maxHistoryLength:]
	}

	// Update viewport content using SetContentLines for better line management
	m.viewport.SetContentLines(m.content)

	// Auto-scroll to bottom
	m.viewport.GotoBottom()

	return m, nil
}
