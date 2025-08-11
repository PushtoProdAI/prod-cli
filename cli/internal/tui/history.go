package tui

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

func (m *Model) loadHistory() {
	file, err := os.Open(m.historyFile)
	if err != nil {
		// File doesn't exist yet, that's ok
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			m.history = append(m.history, line)
		}
	}

	// Truncate history if it's too long
	if len(m.history) > maxHistoryLength {
		m.history = m.history[len(m.history)-maxHistoryLength:]
	}

	m.historyIndex = len(m.history)
}

func (m *Model) saveHistory() {
	file, err := os.Create(m.historyFile)
	if err != nil {
		// Log error but don't crash
		fmt.Fprintf(os.Stderr, "Warning: Failed to save history: %v\n", err)
		return
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	for _, cmd := range m.history {
		if _, err := writer.WriteString(cmd + "\n"); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to write history entry: %v\n", err)
			return
		}
	}

	if err := writer.Flush(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to flush history: %v\n", err)
	}
}

func (m *Model) addToHistory(cmd string) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" || cmd == "exit" {
		return
	}

	// Don't add duplicate consecutive entries
	if len(m.history) > 0 && m.history[len(m.history)-1] == cmd {
		return
	}

	m.history = append(m.history, cmd)

	// Truncate history if it gets too long
	if len(m.history) > maxHistoryLength {
		m.history = m.history[1:] // Remove oldest entry
	}

	m.historyIndex = len(m.history)
	// Note: We don't save on every addition anymore for performance
	// History will be saved on exit or periodically
}

// saveHistoryOnExit saves history when the application exits
func (m *Model) saveHistoryOnExit() {
	m.saveHistory()
}
