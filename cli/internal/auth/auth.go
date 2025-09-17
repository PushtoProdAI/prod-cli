package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/meroxa/prod/cli/internal/config"
)

type AuthProvider interface {
	CheckAuthentication(ctx context.Context) (bool, error)
	ValidateAPIKey(ctx context.Context, token string) (bool, error)
	PerformOAuthLogin(ctx context.Context) error
	APIKeyPrompt() string
}

// SupabaseAuth handles authentication with Supabase
type SupabaseAuth struct {
	config *Config
	store  *TokenStore
	out    io.Writer
}

// NewSupabaseAuth creates a new Supabase auth client
func NewSupabaseAuth(out io.Writer) (*SupabaseAuth, error) {
	// Get Supabase configuration from environment
	supabaseURL := config.GetSupabaseURL()
	if supabaseURL == "" {
		return nil, fmt.Errorf("SUPABASE_URL environment variable not set")
	}

	supabaseAnonKey := config.GetSupabaseAnonKey()
	if supabaseAnonKey == "" {
		return nil, fmt.Errorf("SUPABASE_ANON_KEY environment variable not set")
	}

	store, err := NewTokenStore()
	if err != nil {
		return nil, fmt.Errorf("failed to create token store: %w", err)
	}

	return &SupabaseAuth{
		config: &Config{
			SupabaseURL:     supabaseURL,
			SupabaseAnonKey: supabaseAnonKey,
		},
		store: store,
		out:   out,
	}, nil
}

// IsAuthenticated checks if the user is currently authenticated
func (sa *SupabaseAuth) IsAuthenticated() bool {
	session, err := sa.GetSession()
	if err != nil || session == nil {
		return false
	}

	if session.ExpiresAt.Before(time.Now()) {
		return false
	}

	return true
}

// GetSession returns the current session if valid
func (sa *SupabaseAuth) GetSession() (*Session, error) {
	session, err := sa.store.LoadSession()
	if err != nil {
		return nil, fmt.Errorf("failed to load session: %w", err)
	}

	if session == nil {
		return nil, nil
	}

	if session.ExpiresAt.Before(time.Now()) {
		// TODO: Implement refresh token logic
		return nil, fmt.Errorf("session expired")
	}

	return session, nil
}

// Logout removes the stored session
func (sa *SupabaseAuth) Logout(ctx context.Context) error {
	if err := sa.store.DeleteSession(); err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}

	fmt.Fprintln(sa.out, "✅ Successfully logged out")
	return nil
}

// GetAuthHeader returns the authorization header value for API requests
func (sa *SupabaseAuth) GetAuthHeader() (string, error) {
	session, err := sa.GetSession()
	if err != nil {
		return "", err
	}

	if session == nil {
		return "", fmt.Errorf("not authenticated")
	}

	return fmt.Sprintf("Bearer %s", session.AccessToken), nil
}

// LoginWithSupabaseFunction performs authentication using the Supabase Edge Function
func (sa *SupabaseAuth) LoginWithSupabaseFunction(ctx context.Context) error {
	// Generate a random state parameter for security
	state := fmt.Sprintf("cli_auth_%d", time.Now().UnixNano())

	// Start a local server to receive the callback
	server := &http.Server{
		Addr: ":8081", // Use different port to avoid conflicts
	}

	// Channel to receive the token
	tokenChan := make(chan string, 1)
	errorChan := make(chan error, 1)

	// Set up the callback handler
	http.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(sa.out, "🔍 Callback received: %s\n", r.URL.String())

		// Extract token from URL parameters
		token := r.URL.Query().Get("token")
		errorParam := r.URL.Query().Get("error")
		errorDesc := r.URL.Query().Get("error_description")

		if errorParam != "" {
			fmt.Fprintf(sa.out, "❌ Authentication error: %s - %s\n", errorParam, errorDesc)
			errorChan <- fmt.Errorf("authentication error: %s - %s", errorParam, errorDesc)
			return
		}

		if token == "" {
			fmt.Fprintf(sa.out, "❌ No token received from authentication\n")
			errorChan <- fmt.Errorf("no token received from authentication")
			return
		}

		fmt.Fprintf(sa.out, "✅ Token received, processing...\n")

		// Send success response to browser
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <title>Authentication Successful</title>
    <style>
        body { font-family: Arial, sans-serif; text-align: center; padding: 50px; background: #1a1a1a; color: white; }
        .success { color: #05B55E; font-size: 24px; margin-bottom: 20px; }
        .message { color: #ccc; }
    </style>
</head>
<body>
    <div class="success">✅ Authentication Successful!</div>
    <div class="message">You can now close this window and return to the CLI.</div>
</body>
</html>`)

		// Send token to channel
		tokenChan <- token
	})

	// Start the server in a goroutine
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errorChan <- fmt.Errorf("failed to start callback server: %w", err)
		}
	}()

	// Update the Supabase function to redirect to our local callback
	callbackURL := "http://localhost:8081/callback"
	authURL := fmt.Sprintf("%s/functions/v1/cli-auth?state=%s&callback_url=%s",
		sa.config.SupabaseURL, state, url.QueryEscape(callbackURL))

	fmt.Fprintln(sa.out, "🔐 Starting CLI authentication...")
	fmt.Fprintf(sa.out, "🌐 Opening browser to: %s\n", authURL)

	// Open the browser
	if err := openBrowser(authURL); err != nil {
		fmt.Fprintf(sa.out, "❌ Failed to open browser: %v\n", err)
		fmt.Fprintf(sa.out, "Please manually open: %s\n", authURL)
	}

	fmt.Fprintln(sa.out, "⏳ Waiting for authentication...")
	fmt.Fprintln(sa.out, "   Complete the authentication in your browser, then return here.")

	// Wait for either token or error
	select {
	case token := <-tokenChan:
		// Shutdown the server
		server.Shutdown(context.Background())

		// Automatically authenticate with the received token
		if err := sa.LoginWithToken(ctx, token); err != nil {
			return fmt.Errorf("failed to authenticate with token: %w", err)
		}

	case err := <-errorChan:
		// Shutdown the server
		server.Shutdown(context.Background())
		return err

	case <-ctx.Done():
		// Shutdown the server
		server.Shutdown(context.Background())
		return ctx.Err()
	}

	return nil
}

// extractEmailFromJWT extracts the email from a JWT access token
func extractEmailFromJWT(accessToken string) (string, error) {
	// JWT tokens have 3 parts separated by dots: header.payload.signature
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid JWT token format")
	}

	// Decode the payload (second part)
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("failed to decode JWT payload: %w", err)
	}

	// Parse the JSON payload
	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("failed to parse JWT payload: %w", err)
	}

	// Extract the email field
	email, ok := claims["email"].(string)
	if !ok {
		return "", fmt.Errorf("email field not found in JWT token")
	}

	return email, nil
}

// LoginWithToken authenticates using a token from the Supabase function
func (sa *SupabaseAuth) LoginWithToken(ctx context.Context, token string) error {
	// Parse the token (it's base64 encoded JSON from our function)
	tokenData, err := sa.parseCLIToken(token)
	if err != nil {
		return fmt.Errorf("invalid token: %w", err)
	}

	// Check if token is expired
	if time.Now().Unix()*1000 > tokenData.ExpiresAt {
		return fmt.Errorf("token expired, please re-authenticate")
	}

	// Extract email from the JWT access token
	email, err := extractEmailFromJWT(tokenData.AccessToken)
	if err != nil {
		// Log the error but don't fail the authentication
		fmt.Fprintf(sa.out, "⚠️  Warning: Could not extract email from token: %v\n", err)
		email = "" // Set empty email as fallback
	}

	// Create session from token data
	session := &Session{
		AccessToken:  tokenData.AccessToken,
		RefreshToken: "", // CLI tokens don't have refresh tokens
		ExpiresAt:    time.Unix(tokenData.ExpiresAt/1000, 0),
		User: &User{
			ID:    tokenData.UserID,
			Email: email,
		},
	}

	// Save session
	if err := sa.store.SaveSession(session); err != nil {
		return fmt.Errorf("failed to save session: %w", err)
	}

	fmt.Fprintln(sa.out, "✅ Authentication successful!")
	fmt.Fprintf(sa.out, "👤 Logged in as user: %s\n", tokenData.UserID)

	return nil
}

// parseCLIToken parses the CLI token from the Supabase function
func (sa *SupabaseAuth) parseCLIToken(token string) (*CLITokenData, error) {
	// Decode base64
	decoded, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return nil, err
	}

	var tokenData CLITokenData
	if err := json.Unmarshal(decoded, &tokenData); err != nil {
		return nil, err
	}

	return &tokenData, nil
}
