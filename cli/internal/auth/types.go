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
	SupabaseURL   string
	SupabaseAnonKey string
}