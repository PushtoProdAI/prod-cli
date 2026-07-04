package agent

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/pushtoprodai/prod-cli/internal/backend"
	"github.com/pushtoprodai/prod-cli/internal/config"
	"github.com/pushtoprodai/prod-cli/internal/history"
)

// SlashCommand represents a command that can be executed from the TUI
type SlashCommand struct {
	Name        string
	Description string
	Handler     func(context.Context, io.Writer) (stateFn, error)
}

// GetAvailableSlashCommands returns all available slash commands.
func (a *Agent) GetAvailableSlashCommands() []SlashCommand {
	commands := []SlashCommand{
		{Name: "/clear", Description: "Clear the screen", Handler: a.handleClearCommand},
		{Name: "/deploys", Description: "Show deployment history", Handler: a.handleDeploysCommand},
	}

	// /login and /logout only make sense with a managed backend to sign into.
	// In local mode there's no account, and /login would open an OAuth page
	// against a backend that isn't there and hang — so hide them.
	if config.BackendConfigured() {
		commands = append(
			commands,
			SlashCommand{Name: "/login", Description: "Login to Prod CLI", Handler: a.handleLoginCommand},
			SlashCommand{Name: "/logout", Description: "Logout from Prod CLI", Handler: a.handleLogoutCommand},
		)
	}

	return append(
		commands,
		SlashCommand{Name: "/quit", Description: "Exit the application", Handler: a.handleQuitCommand},
		SlashCommand{Name: "/search", Description: "Search through the output buffer", Handler: a.handleSearchCommand},
		SlashCommand{Name: "/version", Description: "Show the current Prod CLI version", Handler: a.handleVersionCommand},
	)
}

// Command handlers

func (a *Agent) handleClearCommand(ctx context.Context, out io.Writer) (stateFn, error) {
	tuiWriter, ok := out.(TUIWriter)
	if ok {
		tuiWriter.ClearScreen()
	}
	return a.checkPrerequisites, nil
}

func (a *Agent) handleLogoutCommand(ctx context.Context, out io.Writer) (stateFn, error) {
	a.internalAuth.Logout(ctx)
	return a.checkPrerequisites, nil
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
	return a.checkPrerequisites, nil
}

func (a *Agent) handleLoginCommand(ctx context.Context, out io.Writer) (stateFn, error) {
	// Defense in depth: even if invoked directly, don't attempt a browser login
	// against a backend that isn't configured (it would hang for minutes).
	if !config.BackendConfigured() {
		fmt.Fprintln(out, "Login isn't needed in this build — deploys use your own platform credentials.")
		return a.checkPrerequisites, nil
	}
	a.authenticateCLI(ctx)
	return a.sm.currentState, nil
}

func (a *Agent) handleDeploysCommand(ctx context.Context, out io.Writer) (stateFn, error) {
	tuiWriter, ok := out.(TUIWriter)
	if !ok {
		return a.checkPrerequisites, nil
	}

	// Local mode: read from the local history store; no login required.
	if !config.BackendConfigured() {
		store, err := history.NewStore()
		if err != nil {
			fmt.Fprintf(out, "❌ Failed to open local history: %v\n", err)
			return a.checkPrerequisites, nil
		}
		records, err := store.List(20)
		if err != nil {
			fmt.Fprintf(out, "❌ Failed to read local history: %v\n", err)
			return a.checkPrerequisites, nil
		}
		if len(records) == 0 {
			fmt.Fprintln(out, "ℹ️  No deployments found.")
			return a.checkPrerequisites, nil
		}
		tuiWriter.SendDeploymentHistory(historyRecordsToItems(records))
		return a.checkPrerequisites, nil
	}

	// Check if user is authenticated
	if !a.internalAuth.IsAuthenticated() {
		fmt.Fprintln(out, "❌ You must be logged in to view deployment history. Use /login to authenticate.")
		return a.checkPrerequisites, nil
	}

	session, err := a.internalAuth.GetSession()
	if err != nil || session == nil {
		fmt.Fprintln(out, "❌ Failed to get session. Please login again with /login.")
		return a.checkPrerequisites, nil
	}

	// Show message while fetching
	fmt.Fprintln(out, "📊 Fetching deployment history...")

	// Create backend client and fetch deployment history
	backendClient := backend.NewClient()

	// Query all deployments (not filtered by service name)
	opts := backend.DeploymentQueryOptions{
		Limit: 20, // Show last 20 deployments
		Page:  1,
	}

	response, err := backendClient.QueryDeployments(ctx, session.AccessToken, opts)
	if err != nil {
		fmt.Fprintf(out, "❌ Failed to fetch deployment history: %v\n", err)
		return a.checkPrerequisites, nil
	}

	if len(response.Data) == 0 {
		fmt.Fprintln(out, "ℹ️  No deployments found.")
		return a.checkPrerequisites, nil
	}

	// Send deployment history to TUI for display
	tuiWriter.SendDeploymentHistory(response.Data)

	return a.checkPrerequisites, nil
}

// historyRecordsToItems adapts local history records to the display type shared
// with the backend-backed path, so the TUI renders both identically.
func historyRecordsToItems(records []history.Record) []backend.DeploymentHistoryItem {
	items := make([]backend.DeploymentHistoryItem, 0, len(records))
	for _, r := range records {
		completed := ""
		if r.CompletedAt != nil {
			completed = r.CompletedAt.Format(time.RFC3339)
		}
		items = append(items, backend.DeploymentHistoryItem{
			OperationID:   r.ID,
			OperationType: r.OperationType,
			ResourceName:  r.ResourceName,
			Status:        r.Status,
			Platform:      r.Platform,
			Language:      r.Language,
			StartedAt:     r.StartedAt.Format(time.RFC3339),
			CompletedAt:   completed,
			Metadata:      r.Metadata,
		})
	}
	return items
}

func (a *Agent) handleVersionCommand(ctx context.Context, out io.Writer) (stateFn, error) {
	fmt.Fprintf(out, "Prod CLI version: %s\n", config.Version)
	return a.checkPrerequisites, nil
}
