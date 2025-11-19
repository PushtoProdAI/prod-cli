package tokens

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/go-errors/errors"
	"github.com/meroxa/prod/cli/internal/config"
)

// Client is a Go client for the Supabase Edge Function token API
type Client struct {
	baseURL    string
	httpClient *http.Client
	getToken   func() (string, error) // Function to get current user's JWT
}

// NewClient creates a new token client
// getToken should return the user's JWT from the current session
func NewClient(getToken func() (string, error)) *Client {
	return &Client{
		baseURL: config.GetSupabaseURL() + "/functions/v1/tokens",
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		getToken: getToken,
	}
}

// ConsumeTokens atomically deducts tokens from the user's balance
func (c *Client) ConsumeTokens(ctx context.Context, amount int, operation string, metadata Metadata) (*ConsumeResult, error) {
	reqBody := map[string]interface{}{
		"amount":    amount,
		"operation": operation,
		"metadata":  metadata,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, errors.WrapPrefix(err, "failed to marshal request", 0)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/consume", bytes.NewReader(jsonData))
	if err != nil {
		return nil, errors.WrapPrefix(err, "failed to create request", 0)
	}

	// Add auth header
	token, err := c.getToken()
	if err != nil {
		return nil, errors.WrapPrefix(err, "failed to get auth token", 0)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.WrapPrefix(err, "failed to execute request", 0)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.WrapPrefix(err, "failed to read response", 0)
	}

	var result ConsumeResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, errors.WrapPrefix(err, "failed to unmarshal response", 0)
	}

	return &result, nil
}

// GetSummary retrieves comprehensive token information for display
func (c *Client) GetSummary(ctx context.Context) (*TokenSummary, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL, nil)
	if err != nil {
		return nil, errors.WrapPrefix(err, "failed to create request", 0)
	}

	// Add auth header
	token, err := c.getToken()
	if err != nil {
		return nil, errors.WrapPrefix(err, "failed to get auth token", 0)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.WrapPrefix(err, "failed to execute request", 0)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, errors.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.WrapPrefix(err, "failed to read response", 0)
	}

	var summary TokenSummary
	if err := json.Unmarshal(body, &summary); err != nil {
		return nil, errors.WrapPrefix(err, "failed to unmarshal response", 0)
	}

	return &summary, nil
}

// GetAvailableTokens returns just the available token count (quick check)
func (c *Client) GetAvailableTokens(ctx context.Context) (int, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/balance", nil)
	if err != nil {
		return 0, errors.WrapPrefix(err, "failed to create request", 0)
	}

	// Add auth header
	token, err := c.getToken()
	if err != nil {
		return 0, errors.WrapPrefix(err, "failed to get auth token", 0)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, errors.WrapPrefix(err, "failed to execute request", 0)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, errors.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, errors.WrapPrefix(err, "failed to read response", 0)
	}

	var result struct {
		AvailableTokens int `json:"available_tokens"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, errors.WrapPrefix(err, "failed to unmarshal response", 0)
	}

	return result.AvailableTokens, nil
}

// GetPackages returns available token packages for purchase
func (c *Client) GetPackages(ctx context.Context) ([]TokenPackage, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/packages", nil)
	if err != nil {
		return nil, errors.WrapPrefix(err, "failed to create request", 0)
	}

	// Add auth header
	token, err := c.getToken()
	if err != nil {
		return nil, errors.WrapPrefix(err, "failed to get auth token", 0)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.WrapPrefix(err, "failed to execute request", 0)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, errors.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.WrapPrefix(err, "failed to read response", 0)
	}

	var result struct {
		Packages []TokenPackage `json:"packages"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, errors.WrapPrefix(err, "failed to unmarshal response", 0)
	}

	return result.Packages, nil
}

// EstimateCost calculates the token cost for an operation based on rules
// This is client-side estimation - actual cost is enforced server-side
func EstimateCost(operation string, metadata Metadata) int {
	// Base costs (should match server-side rules)
	baseCosts := map[string]float64{
		OperationDeploy:   1.0,
		OperationRollback: 0.25,
		OperationStatus:   0.1,
	}

	baseCost, ok := baseCosts[operation]
	if !ok {
		baseCost = 1.0 // Default
	}

	multiplier := 1.0

	// Apply multipliers based on metadata
	if servicesCount, ok := metadata["services_count"].(int); ok && servicesCount > 3 {
		multiplier *= 1.5
	}

	if llmModel, ok := metadata["llm_model"].(string); ok {
		if llmModel == "gpt-4" || llmModel == "claude-3-opus" {
			multiplier *= 1.3
		}
	}

	return int(baseCost * multiplier)
}

// DisplayTokenWarning shows warnings based on usage threshold
func DisplayTokenWarning(balance *TokenBalance, writer io.Writer) {
	usage := balance.UsagePercentage()

	if usage >= 95 {
		fmt.Fprintf(writer, "⚠️  Warning: Only %d tokens remaining (%.0f%% used)\n", balance.Available(), usage)
		fmt.Fprintf(writer, "   Purchase more tokens to continue deploying.\n")
	} else if usage >= 80 {
		fmt.Fprintf(writer, "📊 You've used %d of %d plan tokens (%.0f%%)\n", balance.UsedTokens, balance.PlanTokens, usage)
		fmt.Fprintf(writer, "   %d tokens remaining until reset on %s\n", balance.Available(), balance.ResetDate.Format("Jan 2, 2006"))
	}
}

// FormatTokenSummary formats a token summary for CLI display
func FormatTokenSummary(summary *TokenSummary, writer io.Writer) {
	fmt.Fprintf(writer, "\n📊 Token Usage Summary\n")
	fmt.Fprintf(writer, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Fprintf(writer, "Plan Tokens:      %d\n", summary.PlanTokens)
	fmt.Fprintf(writer, "Bonus Tokens:     %d\n", summary.BonusTokens)
	fmt.Fprintf(writer, "Used This Month:  %d\n", summary.UsedTokens)
	fmt.Fprintf(writer, "Available:        %d\n", summary.AvailableTokens)
	fmt.Fprintf(writer, "Usage:            %.1f%%\n", summary.UsagePercentage)

	resetDate, _ := time.Parse(time.RFC3339, summary.ResetDate.Format(time.RFC3339))
	fmt.Fprintf(writer, "Resets On:        %s\n", resetDate.Format("January 2, 2006"))

	if len(summary.RecentTransactions) > 0 {
		fmt.Fprintf(writer, "\n📜 Recent Transactions:\n")
		for i, txInterface := range summary.RecentTransactions {
			if i >= 5 { // Show max 5
				break
			}

			txMap, ok := txInterface.(map[string]interface{})
			if !ok {
				continue
			}

			operation, _ := txMap["operation"].(string)
			tokensConsumed, _ := txMap["tokens_consumed"].(float64)
			createdAt, _ := txMap["created_at"].(string)

			timestamp, _ := time.Parse(time.RFC3339, createdAt)

			fmt.Fprintf(writer, "  • %s - %s (%d tokens) - %s\n",
				timestamp.Format("Jan 2 15:04"),
				operation,
				int(tokensConsumed),
				relativeTime(timestamp),
			)
		}
	}

	fmt.Fprintf(writer, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")
}

// relativeTime returns a human-readable relative time string
func relativeTime(t time.Time) string {
	duration := time.Since(t)

	if duration < time.Minute {
		return "just now"
	} else if duration < time.Hour {
		minutes := int(duration.Minutes())
		if minutes == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", minutes)
	} else if duration < 24*time.Hour {
		hours := int(duration.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	} else {
		days := int(duration.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	}
}
