package auth

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/go-errors/errors"
)

// PasswordService handles password security operations
type PasswordService struct {
	db *sql.DB
}

// NewPasswordService creates a new password service
func NewPasswordService(db *sql.DB) *PasswordService {
	return &PasswordService{db: db}
}

// CheckAccountLockout checks if a user account is currently locked out
func (ps *PasswordService) CheckAccountLockout(ctx context.Context, userID string) (bool, error) {
	query := `SELECT is_user_locked_out($1)`
	var isLocked bool
	err := ps.db.QueryRowContext(ctx, query, userID).Scan(&isLocked)
	if err != nil {
		return false, errors.Errorf("failed to check account lockout: %w", err)
	}
	return isLocked, nil
}

// RecordFailedLogin records a failed login attempt for a user
func (ps *PasswordService) RecordFailedLogin(ctx context.Context, userID string) error {
	query := `SELECT record_failed_login($1)`
	_, err := ps.db.ExecContext(ctx, query, userID)
	if err != nil {
		return errors.Errorf("failed to record failed login: %w", err)
	}
	return nil
}

// ResetFailedLogins resets failed login attempts for a user after successful login
func (ps *PasswordService) ResetFailedLogins(ctx context.Context, userID string) error {
	query := `SELECT reset_failed_logins($1)`
	_, err := ps.db.ExecContext(ctx, query, userID)
	if err != nil {
		return errors.Errorf("failed to reset failed logins: %w", err)
	}
	return nil
}

// CheckPasswordHistory checks if a password has been used recently
func (ps *PasswordService) CheckPasswordHistory(ctx context.Context, userID, passwordHash string) (bool, error) {
	query := `SELECT check_password_history($1, $2)`
	var inHistory bool
	err := ps.db.QueryRowContext(ctx, query, userID, passwordHash).Scan(&inHistory)
	if err != nil {
		return false, errors.Errorf("failed to check password history: %w", err)
	}
	return inHistory, nil
}

// AddPasswordToHistory adds a password hash to the user's password history
func (ps *PasswordService) AddPasswordToHistory(ctx context.Context, userID, passwordHash string) error {
	query := `SELECT add_password_to_history($1, $2)`
	_, err := ps.db.ExecContext(ctx, query, userID, passwordHash)
	if err != nil {
		return errors.Errorf("failed to add password to history: %w", err)
	}
	return nil
}

// CheckPasswordExpiration checks if a user's password has expired
func (ps *PasswordService) CheckPasswordExpiration(ctx context.Context, userID string) (bool, error) {
	query := `SELECT is_password_expired($1)`
	var isExpired bool
	err := ps.db.QueryRowContext(ctx, query, userID).Scan(&isExpired)
	if err != nil {
		return false, errors.Errorf("failed to check password expiration: %w", err)
	}
	return isExpired, nil
}

// GetPasswordPolicy retrieves the current password policy settings
func (ps *PasswordService) GetPasswordPolicy(ctx context.Context) (*PasswordSecurityConfig, error) {
	query := `
		SELECT 
			min_length,
			require_uppercase,
			require_lowercase,
			require_numbers,
			require_symbols,
			max_age_days,
			history_count,
			max_failed_attempts,
			lockout_duration_minutes
		FROM password_policies 
		LIMIT 1
	`

	var policy PasswordSecurityConfig
	err := ps.db.QueryRowContext(ctx, query).Scan(
		&policy.MinLength,
		&policy.RequireUpper,
		&policy.RequireLower,
		&policy.RequireNumbers,
		&policy.RequireSymbols,
		&policy.MaxAge,
		&policy.HistoryCount,
		&policy.MaxFailedAttempts,
		&policy.LockoutDuration,
	)
	if err != nil {
		return nil, errors.Errorf("failed to get password policy: %w", err)
	}

	// Convert days to duration
	policy.MaxAge = time.Duration(policy.MaxAge) * 24 * time.Hour
	policy.LockoutDuration = time.Duration(policy.LockoutDuration) * time.Minute

	return &policy, nil
}

// UpdatePasswordPolicy updates the password policy settings
func (ps *PasswordService) UpdatePasswordPolicy(ctx context.Context, policy *PasswordSecurityConfig) error {
	query := `
		UPDATE password_policies SET
			min_length = $1,
			require_uppercase = $2,
			require_lowercase = $3,
			require_numbers = $4,
			require_symbols = $5,
			max_age_days = $6,
			history_count = $7,
			max_failed_attempts = $8,
			lockout_duration_minutes = $9,
			updated_at = NOW()
		WHERE id = (SELECT id FROM password_policies LIMIT 1)
	`

	_, err := ps.db.ExecContext(ctx, query,
		policy.MinLength,
		policy.RequireUpper,
		policy.RequireLower,
		policy.RequireNumbers,
		policy.RequireSymbols,
		int(policy.MaxAge.Hours()/24), // Convert duration to days
		policy.HistoryCount,
		policy.MaxFailedAttempts,
		int(policy.LockoutDuration.Minutes()),
	)
	if err != nil {
		return errors.Errorf("failed to update password policy: %w", err)
	}

	return nil
}

// GetAccountLockoutInfo retrieves lockout information for a user
func (ps *PasswordService) GetAccountLockoutInfo(ctx context.Context, userID string) (*AccountLockoutInfo, error) {
	query := `
		SELECT 
			failed_attempts,
			locked_until,
			last_failed_attempt
		FROM account_lockout 
		WHERE user_id = $1
	`

	var info AccountLockoutInfo
	var lockedUntil sql.NullTime
	var lastFailedAttempt sql.NullTime

	err := ps.db.QueryRowContext(ctx, query, userID).Scan(
		&info.FailedAttempts,
		&lockedUntil,
		&lastFailedAttempt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			// No lockout record exists
			return &AccountLockoutInfo{
				FailedAttempts: 0,
				IsLocked:       false,
			}, nil
		}
		return nil, errors.Errorf("failed to get account lockout info: %w", err)
	}

	info.IsLocked = lockedUntil.Valid && lockedUntil.Time.After(time.Now())
	if lastFailedAttempt.Valid {
		info.LastFailedAttempt = &lastFailedAttempt.Time
	}
	if lockedUntil.Valid {
		info.LockedUntil = &lockedUntil.Time
	}

	return &info, nil
}

// AccountLockoutInfo contains information about account lockout status
type AccountLockoutInfo struct {
	FailedAttempts    int        `json:"failed_attempts"`
	IsLocked          bool       `json:"is_locked"`
	LastFailedAttempt *time.Time `json:"last_failed_attempt,omitempty"`
	LockedUntil       *time.Time `json:"locked_until,omitempty"`
}

// ValidatePasswordWithPolicy validates a password against the current policy
func (ps *PasswordService) ValidatePasswordWithPolicy(ctx context.Context, password string) (*PasswordValidationResult, error) {
	// Get current policy
	policy, err := ps.GetPasswordPolicy(ctx)
	if err != nil {
		return nil, errors.Errorf("failed to get password policy: %w", err)
	}

	// Convert to requirements
	requirements := PasswordRequirements{
		MinLength:      policy.MinLength,
		RequireUpper:   policy.RequireUpper,
		RequireLower:   policy.RequireLower,
		RequireNumbers: policy.RequireNumbers,
		RequireSymbols: policy.RequireSymbols,
	}

	// Validate password
	isValid, errors := ValidatePassword(password, requirements)

	// Calculate strength
	strength := CalculatePasswordStrength(password, requirements)

	// Check for breaches if enabled
	var breachCheck bool
	if policy.EnableBreachCheck {
		breachCheck, err = CheckPasswordBreach(password)
		if err != nil {
			// Log error but don't fail validation
			fmt.Printf("Warning: Password breach check failed: %v\n", err)
		}
	}

	return &PasswordValidationResult{
		IsValid:     isValid && !breachCheck,
		Errors:      errors,
		Strength:    strength,
		BreachCheck: breachCheck,
		Policy:      policy,
	}, nil
}

// PasswordValidationResult contains the result of password validation
type PasswordValidationResult struct {
	IsValid     bool                    `json:"is_valid"`
	Errors      []string                `json:"errors,omitempty"`
	Strength    PasswordStrength        `json:"strength"`
	BreachCheck bool                    `json:"breach_check"`
	Policy      *PasswordSecurityConfig `json:"policy"`
}
