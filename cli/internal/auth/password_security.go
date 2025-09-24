package auth

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/go-errors/errors"
	"github.com/pelletier/go-toml/v2"
)

// PasswordRequirements defines the password complexity requirements
type PasswordRequirements struct {
	MinLength      int  `json:"min_length"`
	RequireUpper   bool `json:"require_uppercase"`
	RequireLower   bool `json:"require_lowercase"`
	RequireNumbers bool `json:"require_numbers"`
	RequireSymbols bool `json:"require_symbols"`
}

// PasswordStrength represents the calculated password strength
type PasswordStrength struct {
	Score       int    `json:"score"`
	Level       string `json:"level"`
	IsValid     bool   `json:"is_valid"`
	BreachCheck bool   `json:"breach_check"`
}

// DefaultPasswordRequirements returns the default password requirements
func DefaultPasswordRequirements() PasswordRequirements {
	return PasswordRequirements{
		MinLength:      8,
		RequireUpper:   true,
		RequireLower:   true,
		RequireNumbers: true,
		RequireSymbols: true,
	}
}

// ValidatePassword checks if a password meets the specified requirements
func ValidatePassword(password string, requirements PasswordRequirements) (bool, []string) {
	var errors []string

	if len(password) < requirements.MinLength {
		errors = append(errors, fmt.Sprintf("Password must be at least %d characters long", requirements.MinLength))
	}

	if requirements.RequireUpper && !regexp.MustCompile(`[A-Z]`).MatchString(password) {
		errors = append(errors, "Password must contain at least one uppercase letter")
	}

	if requirements.RequireLower && !regexp.MustCompile(`[a-z]`).MatchString(password) {
		errors = append(errors, "Password must contain at least one lowercase letter")
	}

	if requirements.RequireNumbers && !regexp.MustCompile(`\d`).MatchString(password) {
		errors = append(errors, "Password must contain at least one number")
	}

	if requirements.RequireSymbols && !regexp.MustCompile(`[!@#$%^&*()_+\-=\[\]{};':"\\|,.<>\/?]`).MatchString(password) {
		errors = append(errors, "Password must contain at least one special character")
	}

	return len(errors) == 0, errors
}

// CalculatePasswordStrength calculates a password strength score and level
func CalculatePasswordStrength(password string, requirements PasswordRequirements) PasswordStrength {
	score := 0
	maxScore := 100

	// Check basic requirements (40 points)
	if len(password) >= requirements.MinLength {
		score += 8
	}
	if regexp.MustCompile(`[A-Z]`).MatchString(password) {
		score += 8
	}
	if regexp.MustCompile(`[a-z]`).MatchString(password) {
		score += 8
	}
	if regexp.MustCompile(`\d`).MatchString(password) {
		score += 8
	}
	if regexp.MustCompile(`[!@#$%^&*()_+\-=\[\]{};':"\\|,.<>\/?]`).MatchString(password) {
		score += 8
	}

	// Length bonus (30 points)
	if len(password) >= 12 {
		score += 15
	} else if len(password) >= 10 {
		score += 10
	} else if len(password) >= 8 {
		score += 5
	}

	// Complexity bonus (30 points)
	uniqueChars := countUniqueChars(password)
	if uniqueChars >= 12 {
		score += 15
	} else if uniqueChars >= 8 {
		score += 10
	} else if uniqueChars >= 6 {
		score += 5
	}

	// Pattern detection (penalties)
	if regexp.MustCompile(`(.)\1{2,}`).MatchString(password) {
		score -= 10 // Repeated characters
	}
	if regexp.MustCompile(`(?i)(123|abc|qwe|asd|zxc)`).MatchString(password) {
		score -= 15 // Common sequences
	}
	if regexp.MustCompile(`(?i)(password|admin|user|test)`).MatchString(password) {
		score -= 20 // Common words
	}

	// Ensure score is within bounds
	if score < 0 {
		score = 0
	}
	if score > maxScore {
		score = maxScore
	}

	// Determine strength level
	var level string
	if score < 30 {
		level = "Very Weak"
	} else if score < 50 {
		level = "Weak"
	} else if score < 70 {
		level = "Fair"
	} else if score < 85 {
		level = "Good"
	} else {
		level = "Strong"
	}

	// Check if password meets all requirements
	isValid, _ := ValidatePassword(password, requirements)

	return PasswordStrength{
		Score:   score,
		Level:   level,
		IsValid: isValid,
	}
}

// countUniqueChars counts the number of unique characters in a string
func countUniqueChars(s string) int {
	seen := make(map[rune]bool)
	for _, char := range s {
		seen[char] = true
	}
	return len(seen)
}

// CheckPasswordBreach checks if a password has been found in data breaches using HaveIBeenPwned API
func CheckPasswordBreach(password string) (bool, error) {
	// Hash the password with SHA-1
	hash := sha1.Sum([]byte(password))
	hashStr := strings.ToUpper(hex.EncodeToString(hash[:]))

	// Get first 5 characters for the API call
	prefix := hashStr[:5]
	suffix := hashStr[5:]

	// Make request to HaveIBeenPwned API
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(fmt.Sprintf("https://api.pwnedpasswords.com/range/%s", prefix))
	if err != nil {
		return false, errors.Errorf("failed to check password breach: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, errors.Errorf("breach check API returned status %d", resp.StatusCode)
	}

	// Read response body
	body := make([]byte, 1024*1024) // 1MB buffer
	n, err := resp.Body.Read(body)
	if err != nil && n == 0 {
		return false, errors.Errorf("failed to read breach check response: %w", err)
	}

	// Check if our hash suffix is in the response
	responseText := string(body[:n])
	lines := strings.Split(responseText, "\n")

	for _, line := range lines {
		if strings.HasPrefix(strings.ToUpper(line), suffix) {
			return true, nil // Password found in breach
		}
	}

	return false, nil // Password not found in breach
}

// HashPassword creates a secure hash of the password for storage
func HashPassword(password string) string {
	// In a real implementation, you would use bcrypt or Argon2
	// For now, we'll use SHA-1 for compatibility with HaveIBeenPwned
	hash := sha1.Sum([]byte(password))
	return hex.EncodeToString(hash[:])
}

// PasswordSecurityConfig holds configuration for password security features
type PasswordSecurityConfig struct {
	MinLength          int           `json:"min_length"`
	RequireUpper       bool          `json:"require_uppercase"`
	RequireLower       bool          `json:"require_lowercase"`
	RequireNumbers     bool          `json:"require_numbers"`
	RequireSymbols     bool          `json:"require_symbols"`
	MaxAge             time.Duration `json:"max_age"`
	HistoryCount       int           `json:"history_count"`
	MaxFailedAttempts  int           `json:"max_failed_attempts"`
	LockoutDuration    time.Duration `json:"lockout_duration"`
	EnableBreachCheck  bool          `json:"enable_breach_check"`
	RequireFirstChange bool          `json:"require_first_change"`
}

// DefaultPasswordSecurityConfig returns the default password security configuration
func DefaultPasswordSecurityConfig() PasswordSecurityConfig {
	return PasswordSecurityConfig{
		MinLength:          8,
		RequireUpper:       true,
		RequireLower:       true,
		RequireNumbers:     true,
		RequireSymbols:     true,
		MaxAge:             90 * 24 * time.Hour, // 90 days
		HistoryCount:       5,
		MaxFailedAttempts:  5,
		LockoutDuration:    30 * time.Minute,
		EnableBreachCheck:  true,
		RequireFirstChange: true,
	}
}

// LoadPasswordSecurityConfig loads password security configuration from file
func LoadPasswordSecurityConfig(configPath string) (*PasswordSecurityConfig, error) {
	// Try to load from file first
	if _, err := os.Stat(configPath); err == nil {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return nil, errors.Errorf("failed to read password config: %w", err)
		}

		var config PasswordSecurityConfig
		if err := toml.Unmarshal(data, &config); err != nil {
			return nil, errors.Errorf("failed to decode password config: %w", err)
		}

		// Convert days to duration
		config.MaxAge = time.Duration(config.MaxAge) * 24 * time.Hour
		config.LockoutDuration = time.Duration(config.LockoutDuration) * time.Minute

		return &config, nil
	}

	// Fall back to default config
	defaultConfig := DefaultPasswordSecurityConfig()
	return &defaultConfig, nil
}
