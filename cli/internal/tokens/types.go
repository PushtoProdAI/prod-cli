package tokens

import (
	"time"

	"github.com/google/uuid"
)

// TokenBalance represents a user's current token allocation and usage
type TokenBalance struct {
	UserID      uuid.UUID `json:"user_id" db:"user_id"`
	PlanTokens  int       `json:"plan_tokens" db:"plan_tokens"`
	BonusTokens int       `json:"bonus_tokens" db:"bonus_tokens"`
	UsedTokens  int       `json:"used_tokens" db:"used_tokens"`
	ResetDate   time.Time `json:"reset_date" db:"reset_date"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" db:"updated_at"`
}

// Available returns the total available tokens (plan + bonus - used)
func (tb *TokenBalance) Available() int {
	planAvailable := max(0, tb.PlanTokens-tb.UsedTokens)
	return planAvailable + tb.BonusTokens
}

// UsagePercentage returns the percentage of plan tokens used (0-100)
func (tb *TokenBalance) UsagePercentage() float64 {
	if tb.PlanTokens == 0 {
		return 0
	}
	return (float64(tb.UsedTokens) / float64(tb.PlanTokens)) * 100
}

// IsLowBalance returns true if usage exceeds the given threshold (e.g., 80.0 for 80%)
func (tb *TokenBalance) IsLowBalance(thresholdPercent float64) bool {
	return tb.UsagePercentage() >= thresholdPercent
}

// TokenTransaction represents an immutable record of a token operation
type TokenTransaction struct {
	ID             uuid.UUID `json:"id" db:"id"`
	UserID         uuid.UUID `json:"user_id" db:"user_id"`
	Operation      string    `json:"operation" db:"operation"`
	TokensConsumed int       `json:"tokens_consumed" db:"tokens_consumed"`
	TokensBefore   int       `json:"tokens_before" db:"tokens_before"`
	TokensAfter    int       `json:"tokens_after" db:"tokens_after"`
	Metadata       Metadata  `json:"metadata" db:"metadata"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
}

// Metadata holds flexible key-value pairs for transaction context
type Metadata map[string]interface{}

// TokenPackage represents a purchasable token bundle
type TokenPackage struct {
	ID          uuid.UUID `json:"id" db:"id"`
	Name        string    `json:"name" db:"name"`
	Description string    `json:"description" db:"description"`
	TokenCount  int       `json:"token_count" db:"token_count"`
	PriceCents  int       `json:"price_cents" db:"price_cents"`
	Active      bool      `json:"active" db:"active"`
	SortOrder   int       `json:"sort_order" db:"sort_order"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" db:"updated_at"`
}

// PriceDollars returns the price in dollars (for display)
func (tp *TokenPackage) PriceDollars() float64 {
	return float64(tp.PriceCents) / 100.0
}

// TokenPurchase represents a record of a token purchase
type TokenPurchase struct {
	ID              uuid.UUID `json:"id" db:"id"`
	UserID          uuid.UUID `json:"user_id" db:"user_id"`
	PackageID       uuid.UUID `json:"package_id" db:"package_id"`
	TokensPurchased int       `json:"tokens_purchased" db:"tokens_purchased"`
	PricePaidCents  int       `json:"price_paid_cents" db:"price_paid_cents"`
	PaymentProvider string    `json:"payment_provider" db:"payment_provider"`
	PaymentID       *string   `json:"payment_id,omitempty" db:"payment_id"`
	PaymentStatus   string    `json:"payment_status" db:"payment_status"`
	Metadata        Metadata  `json:"metadata" db:"metadata"`
	CreatedAt       time.Time `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time `json:"updated_at" db:"updated_at"`
}

// TokenRule represents a token cost calculation rule
// Currently defined in Go code, could be moved to database in the future
type TokenRule struct {
	Operation   string
	BaseCost    float64
	Multipliers []CostMultiplier
	Priority    int
	Active      bool
}

// CostMultiplier represents a conditional cost adjustment
type CostMultiplier struct {
	Condition   string  // Go template condition, e.g., "{{ gt .services_count 3 }}"
	Factor      float64 // Multiplier factor, e.g., 1.5
	Description string  // Human-readable explanation
}

// ConsumeResult represents the result of a token consumption operation
type ConsumeResult struct {
	Success          bool
	TransactionID    *uuid.UUID
	TokensRemaining  int
	ErrorMessage     *string
}

// RefundResult represents the result of a token refund operation
type RefundResult struct {
	Success        bool
	TransactionID  *uuid.UUID
	TokensRefunded int
	ErrorMessage   *string
}

// TokenSummary provides comprehensive token information for display
type TokenSummary struct {
	PlanTokens         int           `json:"plan_tokens"`
	BonusTokens        int           `json:"bonus_tokens"`
	UsedTokens         int           `json:"used_tokens"`
	AvailableTokens    int           `json:"available_tokens"`
	ResetDate          time.Time     `json:"reset_date"`
	UsagePercentage    float64       `json:"usage_percentage"`
	RecentTransactions []interface{} `json:"recent_transactions"` // JSON array from database
}

// Operation type constants for type safety
const (
	OperationDeploy       = "deploy"
	OperationDryRun       = "dry_run"
	OperationRollback     = "rollback"
	OperationStatus       = "status"
	OperationRefund       = "refund"
	OperationPurchase     = "purchase"
	OperationBonus        = "bonus"
	OperationMonthlyReset = "monthly_reset"
)

// PaymentStatus constants
const (
	PaymentStatusPending   = "pending"
	PaymentStatusCompleted = "completed"
	PaymentStatusFailed    = "failed"
	PaymentStatusRefunded  = "refunded"
)

// Helper function for max (Go 1.21+)
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
