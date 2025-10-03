package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/v2/textinput"
	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// handleMouse processes mouse events including text selection
func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// Handle text selection for clicks
	switch mouseMsg := msg.(type) {
	case tea.MouseClickMsg:
		if mouseMsg.Button == ansi.MouseLeft {
			// Account for viewport border (1) + padding top (1)
			viewportY := mouseMsg.Y - 2
			if viewportY < 0 {
				viewportY = 0
			}

			absoluteLine := m.viewport.YOffset + viewportY

			// Clamp to actual content bounds
			if absoluteLine >= len(m.content) {
				absoluteLine = len(m.content) - 1
			}
			if absoluteLine < 0 {
				absoluteLine = 0
			}

			// Account for viewport border (1) + padding left (2)
			col := mouseMsg.X - 3
			if col < 0 {
				col = 0
			}

			// Clamp column to line length
			if absoluteLine < len(m.content) {
				cleanLine := stripANSI(m.content[absoluteLine])
				lineLen := len([]rune(cleanLine))
				if col > lineLen {
					col = lineLen
				}
			}

			m.selection = SelectionState{
				Active:        true,
				StartY:        absoluteLine,
				StartX:        col,
				EndY:          absoluteLine,
				EndX:          col,
				LastAction:    "Selection Started",
				DragStartLine: absoluteLine,
				DragStartCol:  col,
			}
			m.mousePressed = true

			return m, nil
		}

	case tea.MouseMotionMsg:
		if m.mousePressed && m.selection.Active {
			// Account for viewport border (1) + padding top (1)
			viewportY := mouseMsg.Y - 2
			if viewportY < 0 {
				viewportY = 0
			}

			absoluteLine := m.viewport.YOffset + viewportY

			// Clamp to actual content bounds
			if absoluteLine >= len(m.content) {
				absoluteLine = len(m.content) - 1
			}
			if absoluteLine < 0 {
				absoluteLine = 0
			}

			// Account for viewport border (1) + padding left (2)
			col := mouseMsg.X - 3
			if col < 0 {
				col = 0
			}

			// Clamp column to line length
			if absoluteLine < len(m.content) {
				cleanLine := stripANSI(m.content[absoluteLine])
				lineLen := len([]rune(cleanLine))
				if col > lineLen {
					col = lineLen
				}
			}

			m.selection.EndY = absoluteLine
			m.selection.EndX = col

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
	}

	// Pass all other mouse events to viewport
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

// handleWindowResize processes window resize events
func (m Model) handleWindowResize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width = msg.Width
	m.height = msg.Height

	reservedHeight := promptHeight + statusBarHeight
	outputHeight := m.height - reservedHeight
	if outputHeight < 10 {
		outputHeight = 10
	}

	viewportWidth := m.width - 8
	m.viewport.SetWidth(viewportWidth)
	m.viewport.SetHeight(outputHeight)

	m.textInput.SetWidth(m.width - 8)

	m.ready = true
	return m, nil
}

// handleKey processes keyboard events
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Check if it's a key press event
	if keyPress, ok := msg.(tea.KeyPressMsg); ok {
		key := keyPress.Key()

		// Handle Ctrl+C
		if key.Code == 'c' && key.Mod == tea.ModCtrl {
			// If text is selected, copy to clipboard instead of quitting
			if m.selection.Active && len(m.selection.Content) > 0 {
				return m, m.copySelectionToClipboard()
			}
			// Otherwise quit as usual
			m.quitting = true
			m.saveHistoryOnExit()
			return m, tea.Quit
		}

		// Handle Ctrl+U
		if key.Code == 'u' && key.Mod == tea.ModCtrl {
			m.viewport.HalfViewUp()
			m.autoScrollEnabled = false
			return m, nil
		}

		// Handle Ctrl+D
		if key.Code == 'd' && key.Mod == tea.ModCtrl {
			m.viewport.HalfViewDown()
			m.autoScrollEnabled = m.viewport.AtBottom()
			return m, nil
		}

		// Handle Ctrl+A
		if key.Code == 'a' && key.Mod == tea.ModCtrl {
			// Select all text
			m.selectAll()
			return m, nil
		}

		// Handle Shift+Up
		if key.Code == tea.KeyUp && key.Mod == tea.ModShift {
			m.viewport.LineUp(1)
			m.autoScrollEnabled = false
			return m, nil
		}

		// Handle Shift+Down
		if key.Code == tea.KeyDown && key.Mod == tea.ModShift {
			m.viewport.LineDown(1)
			m.autoScrollEnabled = m.viewport.AtBottom()
			return m, nil
		}

		switch key.Code {
		case tea.KeyEnter:
			return m.handleEnterKey()
		case tea.KeyUp:
			return m.handleUpKey()
		case tea.KeyDown:
			return m.handleDownKey()
		case tea.KeyPgUp:
			m.viewport.HalfViewUp()
			m.autoScrollEnabled = false
			return m, nil
		case tea.KeyPgDown:
			m.viewport.HalfViewDown()
			m.autoScrollEnabled = m.viewport.AtBottom()
			return m, nil
		case tea.KeyHome:
			m.viewport.GotoTop()
			m.autoScrollEnabled = false
			return m, nil
		case tea.KeyEnd:
			m.viewport.GotoBottom()
			m.autoScrollEnabled = true
			return m, nil
		case tea.KeyEsc:
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
	if keyPress, ok := msg.(tea.KeyPressMsg); ok {
		key := keyPress.Key()
		switch key.Code {
		case tea.KeyEsc:
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
	}
	return m, nil
}

// handleUIMessage processes UI messages
func (m Model) handleUIMessage(msg UIMessage) (tea.Model, tea.Cmd) {
	availableWidth := m.viewport.Width() - 4
	if availableWidth < 20 {
		availableWidth = 20
	}

	wrappedLines := wrapText(msg.Content, availableWidth)

	for _, line := range wrappedLines {
		styledLine := m.styleLogMessage(line)
		// Split on embedded newlines to ensure one line per content entry
		splitLines := strings.Split(styledLine, "\n")
		for _, splitLine := range splitLines {
			m.content = append(m.content, splitLine)
		}
	}

	if len(m.content) > maxHistoryLength {
		m.content = m.content[len(m.content)-maxHistoryLength:]
	}

	// Immediately update viewport content to keep in sync
	viewportContent := m.renderViewportContent()
	m.viewport.SetContent(viewportContent)

	// If auto-scroll is enabled, scroll to bottom immediately
	if m.autoScrollEnabled {
		m.viewport.GotoBottom()
	}

	return m, nil
}

// handlePlanDisplayMessage processes plan display messages and renders them as a table
func (m Model) handlePlanDisplayMessage(msg PlanDisplayMessage) (tea.Model, tea.Cmd) {
	// Split summary in case it has embedded newlines
	summaryLines := strings.Split(m.styleLogMessage(msg.Summary), "\n")
	m.content = append(m.content, summaryLines...)
	m.content = append(m.content, "")

	tableContent := m.formatPlanAsTable(msg)

	for _, line := range strings.Split(tableContent, "\n") {
		if line != "" {
			m.content = append(m.content, line)
		}
	}

	if len(m.content) > maxHistoryLength {
		m.content = m.content[len(m.content)-maxHistoryLength:]
	}

	// Immediately update viewport content to keep in sync
	viewportContent := m.renderViewportContent()
	m.viewport.SetContent(viewportContent)

	// If auto-scroll is enabled, scroll to bottom immediately
	if m.autoScrollEnabled {
		m.viewport.GotoBottom()
	}

	return m, nil
}
