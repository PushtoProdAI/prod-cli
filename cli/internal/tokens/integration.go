package tokens

import (
	"context"
	"fmt"
	"io"

	"github.com/go-errors/errors"
)

// CheckAndConsumeTokens is a helper that:
// 1. Checks if user has enough tokens
// 2. Shows warnings if usage is high
// 3. Consumes tokens if sufficient
// 4. Returns helpful error if insufficient
func CheckAndConsumeTokens(
	ctx context.Context,
	client *Client,
	amount int,
	operation string,
	metadata Metadata,
	out io.Writer,
) (*ConsumeResult, error) {
	// Get current balance
	balance, err := getCurrentBalance(ctx, client)
	if err != nil {
		// If we can't get balance, log warning but don't block deployment
		fmt.Fprintf(out, "⚠️  Warning: Could not check token balance: %v\n", err)
		fmt.Fprintf(out, "   Proceeding with deployment...\n")
		return nil, nil
	}

	// Show usage warnings
	DisplayTokenWarning(balance, out)

	// Check if sufficient tokens
	available := balance.Available()
	if available < amount {
		return nil, errors.Errorf(
			"Insufficient tokens: need %d, have %d. Purchase more tokens to continue.",
			amount,
			available,
		)
	}

	// Consume tokens
	result, err := client.ConsumeTokens(ctx, amount, operation, metadata)
	if err != nil {
		return nil, errors.WrapPrefix(err, "failed to consume tokens", 0)
	}

	if !result.Success {
		errorMsg := "Unknown error"
		if result.ErrorMessage != nil {
			errorMsg = *result.ErrorMessage
		}
		return nil, errors.Errorf("token consumption failed: %s", errorMsg)
	}

	// Show success message
	fmt.Fprintf(out, "✓ Consumed %d tokens (%d remaining)\n", amount, result.TokensRemaining)

	return result, nil
}

// ShowEstimatedCost shows the user what a deployment will cost before proceeding
func ShowEstimatedCost(operation string, metadata Metadata, out io.Writer) int {
	engine := NewRulesEngine()

	// Get cost with breakdown
	cost, breakdown, err := engine.CalculateCostWithBreakdown(operation, metadata)
	if err != nil {
		// Fallback to basic estimation
		fmt.Fprintf(out, "\n💰 Estimated Cost: Unable to calculate\n\n")
		return 1 // Default to 1 token
	}

	fmt.Fprintf(out, "\n💰 Estimated Cost:\n")
	for _, line := range breakdown {
		fmt.Fprintf(out, "   %s\n", line)
	}
	fmt.Fprintf(out, "\n")

	return cost
}

// RefundOnFailure refunds tokens if a deployment fails
// This should be called in a defer or error handler
func RefundOnFailure(
	ctx context.Context,
	client *Client,
	consumeResult *ConsumeResult,
	deploymentFailed bool,
	reason string,
	out io.Writer,
) {
	if !deploymentFailed || consumeResult == nil || consumeResult.TransactionID == nil {
		return
	}

	fmt.Fprintf(out, "🔄 Refunding tokens due to deployment failure...\n")

	// Note: Refund requires service role key, so it would need to be called via Edge Function
	// For now, we'll log it and handle refunds via admin tool or support
	fmt.Fprintf(out, "   Transaction ID: %s\n", consumeResult.TransactionID.String())
	fmt.Fprintf(out, "   Reason: %s\n", reason)
	fmt.Fprintf(out, "   Please contact support for a refund if needed.\n")

	// TODO: Implement refund Edge Function for automatic refunds
}

// getCurrentBalance is a helper to get balance with proper error handling
func getCurrentBalance(ctx context.Context, client *Client) (*TokenBalance, error) {
	summary, err := client.GetSummary(ctx)
	if err != nil {
		return nil, err
	}

	return &TokenBalance{
		PlanTokens:  summary.PlanTokens,
		BonusTokens: summary.BonusTokens,
		UsedTokens:  summary.UsedTokens,
		// ResetDate and other fields would come from summary as well
	}, nil
}

// pluralize returns "s" if count != 1, empty string otherwise
func pluralize(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

// SuggestPurchaseOptions displays available token packages when user runs out
func SuggestPurchaseOptions(ctx context.Context, client *Client, out io.Writer) error {
	packages, err := client.GetPackages(ctx)
	if err != nil {
		return errors.WrapPrefix(err, "failed to get token packages", 0)
	}

	fmt.Fprintf(out, "\n💳 Purchase Additional Tokens:\n")
	fmt.Fprintf(out, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")

	for _, pkg := range packages {
		fmt.Fprintf(
			out, "  %s - %d tokens for $%.2f\n",
			pkg.Name,
			pkg.TokenCount,
			pkg.PriceDollars(),
		)
		if pkg.Description != "" {
			fmt.Fprintf(out, "    %s\n", pkg.Description)
		}
	}

	fmt.Fprintf(out, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Fprintf(out, "\nVisit https://prod.cli/tokens to purchase\n\n")

	return nil
}
