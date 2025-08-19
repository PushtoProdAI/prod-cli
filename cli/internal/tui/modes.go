package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// handleEnterKey processes Enter key based on current mode
func (m Model) handleEnterKey() (tea.Model, tea.Cmd) {
	if m.isMode(ModeConfirmation) {
		return m.handleConfirmationEnter()
	} else if m.isMode(ModeAuthSelection) {
		return m.handleAuthSelectionEnter()
	} else if m.isMode(ModeAPIKey) {
		return m.handleAPIKeyEnter()
	} else if m.isMode(ModeSelect) {
		return m.handleSelectEnter()
	} else if m.isMode(ModeText) {
		return m.handleTextEnter()
	} else {
		return m.handleNormalEnter()
	}
}

// handleConfirmationEnter processes Enter in confirmation mode
func (m Model) handleConfirmationEnter() (tea.Model, tea.Cmd) {
	response := strings.ToLower(strings.TrimSpace(m.textInput.Value()))
	if response == "y" || response == "yes" || response == "n" || response == "no" {
		// Send response back to agent
		inputToProcess := m.textInput.Value()
		// Exit confirmation mode
		m.setMode(ModeNormal)
		m.confirmationPrompt = nil
		m.textInput.SetValue("")
		// Process the confirmation response with the agent
		if m.agent != nil {
			go func() {
				ctx := context.Background()
				m.agent.Process(ctx, inputToProcess, nil)
			}()
		}
		return m, nil
	}
	// Invalid response, stay in confirmation mode
	return m, nil
}

// handleAuthSelectionEnter processes Enter in auth selection mode
func (m Model) handleAuthSelectionEnter() (tea.Model, tea.Cmd) {
	selection := strings.TrimSpace(m.textInput.Value())
	if selection == "0" || selection == "1" {
		// Send selection back to agent
		inputToProcess := m.textInput.Value()
		// Exit auth selection mode
		m.setMode(ModeNormal)
		m.authSelectionPrompt = nil
		m.textInput.SetValue("")
		// Process the auth selection response with the agent
		if m.agent != nil {
			go func() {
				ctx := context.Background()
				m.agent.Process(ctx, inputToProcess, nil)
			}()
		}
		return m, nil
	}
	// Invalid selection, stay in auth selection mode
	return m, nil
}

// handleAPIKeyEnter processes Enter in API key mode
func (m Model) handleAPIKeyEnter() (tea.Model, tea.Cmd) {
	apiKey := strings.TrimSpace(m.textInput.Value())
	if apiKey != "" {
		// Send API key back to agent
		inputToProcess := m.textInput.Value()
		// Exit API key mode
		m.setMode(ModeNormal)
		m.apiKeyPrompt = nil
		m.textInput.SetValue("")
		// Process the API key response with the agent
		if m.agent != nil {
			go func() {
				ctx := context.Background()
				m.agent.Process(ctx, inputToProcess, nil)
			}()
		}
		return m, nil
	}
	// Empty API key, stay in API key mode
	return m, nil
}

// handleSelectEnter processes Enter in select mode
func (m Model) handleSelectEnter() (tea.Model, tea.Cmd) {
	if m.selectPrompt != nil {
		selection := fmt.Sprintf("%d", m.selectPrompt.Cursor)
		// Exit select mode
		m.setMode(ModeNormal)
		m.selectPrompt = nil
		m.textInput.SetValue("")
		// Process the selection response with the agent
		if m.agent != nil {
			go func() {
				ctx := context.Background()
				m.agent.Process(ctx, selection, nil)
			}()
		}
		return m, nil
	}
	return m, nil
}

// handleNormalEnter processes Enter in normal mode
func (m Model) handleNormalEnter() (tea.Model, tea.Cmd) {
	input := strings.TrimSpace(m.textInput.Value())
	if input == "" {
		return m, nil
	}

	if input == "exit" {
		m.quitting = true
		m.saveHistoryOnExit()
		return m, tea.Quit
	}

	// Add to history and clear input
	m.addToHistory(input)
	m.textInput.SetValue("")

	// Process input with agent
	if m.agent != nil {
		go func() {
			ctx := context.Background()
			m.agent.Process(ctx, input, nil)
		}()
	}

	return m, nil
}

// handleTextEnter processes Enter in text prompt mode
func (m Model) handleTextEnter() (tea.Model, tea.Cmd) {
	if m.textPrompt == nil {
		return m, nil
	}

	input := m.textInput.Value()

	// Reset mode and clear prompt
	m.setMode(ModeNormal)
	m.textPrompt = nil
	m.textInput.SetValue("")

	// Process input with agent
	if m.agent != nil {
		go func() {
			ctx := context.Background()
			m.agent.Process(ctx, input, nil)
		}()
	}

	return m, nil
}
