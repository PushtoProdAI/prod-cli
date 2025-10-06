package agent

import (
	"context"
	"io"
)

// SlashCommand represents a command that can be executed from the TUI
type SlashCommand struct {
	Name        string
	Description string
	Handler     func(*Agent, context.Context, io.Writer) (stateFn, error)
}

// GetAvailableSlashCommands returns all available slash commands
func (a *Agent) GetAvailableSlashCommands() []SlashCommand {
	return []SlashCommand{
		{
			Name:        "/clear",
			Description: "Clear the screen",
			Handler:     (*Agent).handleClearCommand,
		},
		{
			Name:        "/logout",
			Description: "Logout from Prod CLI",
			Handler:     (*Agent).handleLogoutCommand,
		},
		{
			Name:        "/quit",
			Description: "Exit the application",
			Handler:     (*Agent).handleQuitCommand,
		},
		{
			Name:        "/search",
			Description: "Search through the output buffer",
			Handler:     (*Agent).handleSearchCommand,
		},
	}
}

// Command handlers

func (a *Agent) handleClearCommand(ctx context.Context, out io.Writer) (stateFn, error) {
	tuiWriter, ok := out.(TUIWriter)
	if ok {
		tuiWriter.ClearScreen()
	}
	return a.plan, nil
}

func (a *Agent) handleLogoutCommand(ctx context.Context, out io.Writer) (stateFn, error) {
	a.internalAuth.Logout(ctx)
	return a.plan, nil
}

func (a *Agent) handleQuitCommand(ctx context.Context, out io.Writer) (stateFn, error) {
	tuiWriter, ok := out.(TUIWriter)
	if ok {
		tuiWriter.Quit()
	}
	return nil, nil
}

func (a *Agent) handleSearchCommand(ctx context.Context, out io.Writer) (stateFn, error) {
	tuiWriter, ok := out.(TUIWriter)
	if ok {
		tuiWriter.Search()
	}
	return a.plan, nil
}
