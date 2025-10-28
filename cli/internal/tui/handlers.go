package tui

import (
	"fmt"
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
	// Check if this is a key press event (not a key release)
	// Only process key press events for our special handling
	isKeyPress := false
	if _, ok := msg.(tea.KeyPressMsg); ok {
		isKeyPress = true
	}

	// Only handle special keys if this is a key press event
	if isKeyPress {
		key := msg.Key()

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

		// Handle Ctrl+N (next match in search)
		if key.Code == 'n' && key.Mod == tea.ModCtrl && m.isMode(ModeSearch) {
			m.nextMatch()
			return m, nil
		}

		// Handle Ctrl+P (previous match in search)
		if key.Code == 'p' && key.Mod == tea.ModCtrl && m.isMode(ModeSearch) {
			m.prevMatch()
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
		case tea.KeyTab:
			// Handle Tab for slash command autocomplete
			if m.showSlashCommands && m.isMode(ModeNormal) {
				filtered := m.getFilteredSlashCommands()
				if len(filtered) > 0 && m.slashCommandCursor < len(filtered) {
					m.textInput.SetValue(filtered[m.slashCommandCursor].Command)
					m.textInput.CursorEnd()
					m.showSlashCommands = false
				}
				return m, nil
			}
		case tea.KeyEsc:
			// Exit search mode on Esc
			if m.isMode(ModeSearch) {
				m.setMode(ModeNormal)
				m.clearSearch()
				m.textInput.SetValue("")
				m.textInput.Placeholder = ""
				return m, nil
			}
			// Hide slash commands on Esc
			if m.showSlashCommands {
				m.showSlashCommands = false
				return m, nil
			}
			// Clear selection if active
			if m.selection.Active {
				m.clearSelection()
				return m, nil
			}
		default:
			// Check for number keys to toggle remediations
			if m.currentError != nil && key.Code >= '1' && key.Code <= '9' {
				index := int(key.Code - '1')
				if index < len(m.currentError.Remediations) {
					m.expandedRemediations[index] = !m.expandedRemediations[index]

					// Remove old error display
					if m.errorStartLine >= 0 && m.errorEndLine > m.errorStartLine {
						m.content = append(m.content[:m.errorStartLine], m.content[m.errorEndLine:]...)
					}

					// Re-render the error display at the same position
					m.errorStartLine = len(m.content)
					errorContent := m.formatErrorDisplay(*m.currentError)

					var newLines []string
					for _, line := range strings.Split(errorContent, "\n") {
						if line != "" {
							newLines = append(newLines, line)
						}
					}

					m.content = append(m.content, newLines...)
					m.errorEndLine = len(m.content)

					viewportContent := m.renderViewportContent()
					m.viewport.SetContent(viewportContent)
					m.viewport.GotoBottom()

					return m, nil
				}
			}
		}
	}

	// Always pass the message to special mode handler or text input
	// This ensures the textinput gets all key events it needs

	// WORKAROUND for Windows: If Text is empty but Code is a printable character,
	// create a new KeyPressMsg with Text populated from Code
	if isKeyPress {
		key := msg.Key()
		debugMsg := "DEBUG: Checking workaround - Text='" + key.Text + "', Code=" + string(rune(key.Code)) + fmt.Sprintf(", CodeInt=%d, Mod=%d", key.Code, key.Mod)
		m.content = append(m.content, debugMsg)
		if key.Text == "" && key.Code >= 32 && key.Code <= 126 {
			// Create a new Key with Text populated
			newKey := tea.Key{
				Text:        string(key.Code),
				Mod:         key.Mod,
				Code:        key.Code,
				ShiftedCode: key.ShiftedCode,
				BaseCode:    key.BaseCode,
				IsRepeat:    key.IsRepeat,
			}
			msg = tea.KeyPressMsg(newKey)
			m.content = append(m.content, "DEBUG: Created new KeyPressMsg with Text="+string(key.Code))
		} else {
			m.content = append(m.content, fmt.Sprintf("DEBUG: Condition failed - TextEmpty=%v, CodeRange=%v", key.Text == "", key.Code >= 32 && key.Code <= 126))
		}
	}

	// DEBUG: Log what we're about to pass to textinput
	key := msg.Key()
	m.content = append(m.content, "DEBUG: Before textinput - Text='"+key.Text+"', Code="+string(rune(key.Code))+", Value='"+m.textInput.Value()+"'")

	if !m.isMode(ModeNormal) {
		return m.handleSpecialModeKeys(msg)
	}

	// Update text input for normal mode
	var cmd tea.Cmd
	m.textInput, cmd = m.textInput.Update(msg)

	// DEBUG: Log after textinput update
	m.content = append(m.content, "DEBUG: After textinput - Value='"+m.textInput.Value()+"'")

	// Check if we should show slash commands
	m.updateSlashCommandVisibility()

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

	// Slash command navigation
	if m.showSlashCommands {
		filtered := m.getFilteredSlashCommands()
		if len(filtered) > 0 && m.slashCommandCursor > 0 {
			m.slashCommandCursor--
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

	// Slash command navigation
	if m.showSlashCommands {
		filtered := m.getFilteredSlashCommands()
		if len(filtered) > 0 && m.slashCommandCursor < len(filtered)-1 {
			m.slashCommandCursor++
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
	key := msg.Key()
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
	}

	// Update text input for non-select modes
	if !m.isMode(ModeSelect) {
		var cmd tea.Cmd
		m.textInput, cmd = m.textInput.Update(msg)
		return m, cmd
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
	// Display summary as an info box
	if msg.Summary != "" {
		summaryBox := m.formatInfoBox(InfoBoxMessage{
			Title:   "Deployment Plan",
			Content: msg.Summary,
			Icon:    "📋",
		})
		for _, line := range strings.Split(summaryBox, "\n") {
			if line != "" {
				m.content = append(m.content, line)
			}
		}
		m.content = append(m.content, "")
	}

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

func (m Model) handleErrorDisplayMessage(msg ErrorDisplayMessage) (tea.Model, tea.Cmd) {
	msgCopy := msg
	m.currentError = &msgCopy
	m.errorStartLine = len(m.content)

	errorContent := m.formatErrorDisplay(msg)

	for _, line := range strings.Split(errorContent, "\n") {
		if line != "" {
			m.content = append(m.content, line)
		}
	}

	m.errorEndLine = len(m.content)

	if len(m.content) > maxHistoryLength {
		m.content = m.content[len(m.content)-maxHistoryLength:]
	}

	viewportContent := m.renderViewportContent()
	m.viewport.SetContent(viewportContent)

	if m.autoScrollEnabled {
		m.viewport.GotoBottom()
	}

	return m, nil
}

func (m Model) handleWarningDisplayMessage(msg WarningDisplayMessage) (tea.Model, tea.Cmd) {
	m.errorStartLine = len(m.content)

	warningContent := m.formatWarningDisplay(msg)

	for _, line := range strings.Split(warningContent, "\n") {
		if line != "" {
			m.content = append(m.content, line)
		}
	}

	m.errorEndLine = len(m.content)

	if len(m.content) > maxHistoryLength {
		m.content = m.content[len(m.content)-maxHistoryLength:]
	}

	viewportContent := m.renderViewportContent()
	m.viewport.SetContent(viewportContent)

	if m.autoScrollEnabled {
		m.viewport.GotoBottom()
	}

	return m, nil
}

func (m Model) handleClearScreen() (tea.Model, tea.Cmd) {
	banner := getBanner()
	greeting := greetUser()

	var initialContent []string
	bannerLines := strings.Split(banner, "\n")
	initialContent = append(initialContent, bannerLines...)
	initialContent = append(initialContent, "")
	initialContent = append(initialContent, logStyle.Render(greeting))
	initialContent = append(initialContent, "")
	initialContent = append(initialContent, logStyle.Render("Type 'exit' or press Ctrl+C to quit."))
	initialContent = append(initialContent, "")

	m.content = initialContent
	m.viewport.SetContentLines(initialContent)
	m.viewport.GotoBottom()
	m.autoScrollEnabled = true

	return m, nil
}

func (m *Model) updateSlashCommandVisibility() {
	input := m.textInput.Value()

	// Show slash commands if input starts with /
	if strings.HasPrefix(input, "/") && m.isMode(ModeNormal) {
		m.showSlashCommands = true
		m.slashCommandCursor = 0
	} else {
		m.showSlashCommands = false
	}
}

func (m Model) getFilteredSlashCommands() []SlashCommand {
	input := m.textInput.Value()

	if !strings.HasPrefix(input, "/") {
		return []SlashCommand{}
	}

	var filtered []SlashCommand
	for _, cmd := range m.availableCommands {
		if strings.HasPrefix(cmd.Command, input) {
			filtered = append(filtered, cmd)
		}
	}

	return filtered
}

func (m *Model) performSearch(query string) {
	m.searchQuery = query
	m.searchMatches = []SearchMatch{}
	m.currentMatchIndex = 0

	if query == "" {
		return
	}

	queryLower := strings.ToLower(query)
	queryRunes := []rune(queryLower)
	queryLen := len(queryRunes)

	// Search through all content lines
	for lineIdx, line := range m.content {
		cleanLine := stripANSI(line)
		cleanLineRunes := []rune(cleanLine)
		cleanLineLower := strings.ToLower(string(cleanLineRunes))
		cleanLineLowerRunes := []rune(cleanLineLower)

		// Find all occurrences in this line using rune positions
		for startPos := 0; startPos <= len(cleanLineLowerRunes)-queryLen; startPos++ {
			// Check if query matches at this position
			match := true
			for i := 0; i < queryLen; i++ {
				if cleanLineLowerRunes[startPos+i] != queryRunes[i] {
					match = false
					break
				}
			}

			if match {
				m.searchMatches = append(m.searchMatches, SearchMatch{
					LineIndex: lineIdx,
					StartCol:  startPos,
					EndCol:    startPos + queryLen,
				})
			}
		}
	}

	// Jump to first match
	if len(m.searchMatches) > 0 {
		m.jumpToMatch(0)
	}
}

func (m *Model) jumpToMatch(index int) {
	if index < 0 || index >= len(m.searchMatches) {
		return
	}

	m.currentMatchIndex = index
	match := m.searchMatches[index]

	// Calculate the line to scroll to (center the match in viewport)
	targetLine := match.LineIndex - m.viewport.Height()/2
	if targetLine < 0 {
		targetLine = 0
	}

	m.viewport.SetYOffset(targetLine)
	m.autoScrollEnabled = false
}

func (m *Model) nextMatch() {
	if len(m.searchMatches) == 0 {
		return
	}

	nextIndex := (m.currentMatchIndex + 1) % len(m.searchMatches)
	m.jumpToMatch(nextIndex)
}

func (m *Model) prevMatch() {
	if len(m.searchMatches) == 0 {
		return
	}

	prevIndex := m.currentMatchIndex - 1
	if prevIndex < 0 {
		prevIndex = len(m.searchMatches) - 1
	}
	m.jumpToMatch(prevIndex)
}

func (m *Model) clearSearch() {
	m.searchQuery = ""
	m.searchMatches = []SearchMatch{}
	m.currentMatchIndex = 0
}

func (m Model) handleSuccessDisplayMessage(msg SuccessDisplayMessage) (tea.Model, tea.Cmd) {
	successContent := m.formatSuccessDisplay(msg)

	for _, line := range strings.Split(successContent, "\n") {
		if line != "" {
			m.content = append(m.content, line)
		}
	}

	if len(m.content) > maxHistoryLength {
		m.content = m.content[len(m.content)-maxHistoryLength:]
	}

	viewportContent := m.renderViewportContent()
	m.viewport.SetContent(viewportContent)

	if m.autoScrollEnabled {
		m.viewport.GotoBottom()
	}

	return m, nil
}

func (m Model) handleInfoBoxMessage(msg InfoBoxMessage) (tea.Model, tea.Cmd) {
	infoContent := m.formatInfoBox(msg)

	for _, line := range strings.Split(infoContent, "\n") {
		if line != "" {
			m.content = append(m.content, line)
		}
	}

	if len(m.content) > maxHistoryLength {
		m.content = m.content[len(m.content)-maxHistoryLength:]
	}

	viewportContent := m.renderViewportContent()
	m.viewport.SetContent(viewportContent)

	if m.autoScrollEnabled {
		m.viewport.GotoBottom()
	}

	return m, nil
}
