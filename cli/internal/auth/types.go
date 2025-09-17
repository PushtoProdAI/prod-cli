package auth

import "time"

// Session represents an authenticated session
type Session struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	User         *User     `json:"user,omitempty"`
}

// User represents the authenticated user
type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

// Config holds auth configuration
type Config struct {
	SupabaseURL     string
	SupabaseAnonKey string
}

// CLITokenData represents the token data from the Supabase function
type CLITokenData struct {
	UserID      string `json:"user_id"`
	AccessToken string `json:"access_token"`
	IssuedAt    int64  `json:"issued_at"`
	ExpiresAt   int64  `json:"expires_at"`
}
