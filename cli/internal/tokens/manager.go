package tokens

import (
	"context"
	"fmt"
	"io"
	"log/slog"
)

// Manager is the high-level interface for all token operations.
// Agents should interact with Manager, not Client directly.
type Manager struct {
	client         *Client
	getAccessToken func() (string, error)
	supabaseURL    string
	uiOutput       interface{} // For TUI updates
}

// NewManager creates a new token manager
func NewManager(getAccessToken func() (string, error), supabaseURL string) *Manager {
	return &Manager{
		client:         NewClient(getAccessToken),
		getAccessToken: getAccessToken,
		supabaseURL:    supabaseURL,
	}
}

// SetUIOutput sets the UI output for TUI status bar updates
func (m *Manager) SetUIOutput(output interface{}) {
	m.uiOutput = output
}

// Client returns the underlying token client for direct API access
func (m *Manager) Client() *Client {
	return m.client
}

// ShowBalanceAfterLogin fetches and displays token balance, updating TUI if available
func (m *Manager) ShowBalanceAfterLogin(ctx context.Context, out io.Writer) error {
	fmt.Fprintln(out, "\n💰 Checking your token balance...")

	summary, err := m.client.GetSummary(ctx)
	if err != nil {
		slog.Warn("Failed to get token balance", "error", err)
		return err
	}

	available := summary.PlanTokens + summary.BonusTokens - summary.UsedTokens
	fmt.Fprintf(out, "   You have %d tokens available\n\n", available)

	// Update TUI status bar immediately
	if teaWriter, ok := m.uiOutput.(interface{ UpdateTokenBalance(int) }); ok {
		teaWriter.UpdateTokenBalance(available)
	}

	return nil
}

// UpdateBalanceInUI refreshes the token balance and updates the TUI
func (m *Manager) UpdateBalanceInUI(ctx context.Context) {
	summary, err := m.client.GetSummary(ctx)
	if err != nil {
		slog.Warn("Failed to get token balance", "error", err)
		return
	}

	available := summary.PlanTokens + summary.BonusTokens - summary.UsedTokens

	if m.uiOutput != nil {
		if teaWriter, ok := m.uiOutput.(interface{ UpdateTokenBalance(int) }); ok {
			teaWriter.UpdateTokenBalance(available)
		}
	}
}

// StartPurchaseFlow initiates the token purchase flow
func (m *Manager) StartPurchaseFlow(ctx context.Context, out io.Writer) (*PurchaseSession, error) {
	return NewPurchaseSession(m, ctx, out)
}
