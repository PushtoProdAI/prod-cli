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
		// Cannot verify token balance - block deployment for safety
		return nil, errors.WrapPrefix(err, "failed to check token balance", 0)
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

	// Call refund Edge Function
	result, err := client.RefundTokens(ctx, consumeResult.TransactionID.String(), reason)
	if err != nil {
		// Log error but don't fail - user can contact support
		fmt.Fprintf(out, "   ⚠️  Automatic refund failed: %v\n", err)
		fmt.Fprintf(out, "   Transaction ID: %s\n", consumeResult.TransactionID.String())
		fmt.Fprintf(out, "   Please contact support for a manual refund.\n")
		return
	}

	if !result.Success {
		errorMsg := "Unknown error"
		if result.ErrorMessage != nil {
			errorMsg = *result.ErrorMessage
		}
		fmt.Fprintf(out, "   ⚠️  Refund failed: %s\n", errorMsg)
		fmt.Fprintf(out, "   Transaction ID: %s\n", consumeResult.TransactionID.String())
		fmt.Fprintf(out, "   Please contact support for a manual refund.\n")
		return
	}

	// Success!
	fmt.Fprintf(out, "   ✅ Refunded %d token%s\n", result.TokensRefunded, pluralize(result.TokensRefunded))
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
		fmt.Fprintf(out, "  %s - %d tokens for $%.2f\n",
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
