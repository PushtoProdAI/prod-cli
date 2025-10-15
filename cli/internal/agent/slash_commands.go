package agent

import (
	"context"
	"io"
)

// SlashCommand represents a command that can be executed from the TUI
type SlashCommand struct {
	Name        string
	Description string
	Handler     func(context.Context, io.Writer) (stateFn, error)
}

// GetAvailableSlashCommands returns all available slash commands
func (a *Agent) GetAvailableSlashCommands() []SlashCommand {
	return []SlashCommand{
		{
			Name:        "/clear",
			Description: "Clear the screen",
			Handler:     a.handleClearCommand,
		},
		{
			Name:        "/login",
			Description: "Login to Prod CLI",
			Handler:     a.handleLoginCommand,
		},

		{
			Name:        "/logout",
			Description: "Logout from Prod CLI",
			Handler:     a.handleLogoutCommand,
		},
		{
			Name:        "/quit",
			Description: "Exit the application",
			Handler:     a.handleQuitCommand,
		},
		{
			Name:        "/search",
			Description: "Search through the output buffer",
			Handler:     a.handleSearchCommand,
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

func (a *Agent) handleLoginCommand(ctx context.Context, out io.Writer) (stateFn, error) {
	a.authenticateCLI(ctx)
	return a.sm.currentState, nil
}
