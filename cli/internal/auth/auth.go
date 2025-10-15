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

	"github.com/go-errors/errors"

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
		return nil, errors.Errorf("SUPABASE_URL environment variable not set")
	}

	supabaseAnonKey := config.GetSupabaseAnonKey()
	if supabaseAnonKey == "" {
		return nil, errors.Errorf("SUPABASE_ANON_KEY environment variable not set")
	}

	store, err := NewTokenStore()
	if err != nil {
		return nil, errors.Errorf("failed to create token store: %w", err)
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

	// Check file-based expiry
	if session.ExpiresAt.Before(time.Now()) {
		return false
	}

	// Also validate the JWT token itself to ensure it hasn't expired
	jwtExpiry, err := extractExpiryFromJWT(session.AccessToken)
	if err != nil {
		// If we can't parse the JWT, consider it invalid
		return false
	}

	// Add a small buffer (5 minutes) to account for clock skew
	if jwtExpiry.Before(time.Now().Add(5 * time.Minute)) {
		return false
	}

	return true
}

// GetSession returns the current session if valid
func (sa *SupabaseAuth) GetSession() (*Session, error) {
	session, err := sa.store.LoadSession()
	if err != nil {
		return nil, errors.Errorf("failed to load session: %w", err)
	}

	if session == nil {
		return nil, nil
	}

	// Check file-based expiry first
	if session.ExpiresAt.Before(time.Now()) {
		return nil, errors.Errorf("session expired (file timestamp)")
	}

	// Validate the JWT token itself
	jwtExpiry, err := extractExpiryFromJWT(session.AccessToken)
	if err != nil {
		return nil, errors.Errorf("failed to validate JWT token: %w", err)
	}

	if jwtExpiry.Before(time.Now()) {
		return nil, errors.Errorf("session expired (JWT token)")
	}

	return session, nil
}

// Logout removes the stored session
func (sa *SupabaseAuth) Logout(ctx context.Context) error {
	if err := sa.store.DeleteSession(); err != nil {
		return errors.Errorf("failed to delete session: %w", err)
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
		return "", errors.Errorf("not authenticated")
	}

	return fmt.Sprintf("Bearer %s", session.AccessToken), nil
}

// LoginWithSupabaseFunction performs authentication using the Supabase Edge Function
func (sa *SupabaseAuth) LoginWithSupabaseFunction(ctx context.Context) error {
	// Generate a random state parameter for security
	state := fmt.Sprintf("cli_auth_%d", time.Now().UnixNano())

	// Channel to receive the token
	tokenChan := make(chan string, 1)
	errorChan := make(chan error, 1)

	// Set up the callback handler
	callback := func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(sa.out, "🔍 Callback received: %s\n", r.URL.String())

		// Extract token from URL parameters
		token := r.URL.Query().Get("token")
		errorParam := r.URL.Query().Get("error")
		errorDesc := r.URL.Query().Get("error_description")

		if errorParam != "" {
			fmt.Fprintf(sa.out, "❌ Authentication error: %s - %s\n", errorParam, errorDesc)
			errorChan <- errors.Errorf("authentication error: %s - %s", errorParam, errorDesc)
			return
		}

		if token == "" {
			fmt.Fprintf(sa.out, "❌ No token received from authentication\n")
			errorChan <- errors.Errorf("no token received from authentication")
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
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", callback)

	// Start a local server to receive the callback
	server := &http.Server{
		Addr:    ":8081", // Use different port to avoid conflicts
		Handler: mux,
	}

	// Start the server in a goroutine
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errorChan <- errors.Errorf("failed to start callback server: %w", err)
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
			return errors.Errorf("failed to authenticate with token: %w", err)
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

// extractJWTClaims extracts and parses the claims from a JWT access token
func extractJWTClaims(accessToken string) (map[string]interface{}, error) {
	// JWT tokens have 3 parts separated by dots: header.payload.signature
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		return nil, errors.Errorf("invalid JWT token format")
	}

	// Decode the payload (second part)
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errors.Errorf("failed to decode JWT payload: %w", err)
	}

	// Parse the JSON payload
	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, errors.Errorf("failed to parse JWT payload: %w", err)
	}

	return claims, nil
}

// extractEmailFromJWT extracts the email from a JWT access token
func extractEmailFromJWT(accessToken string) (string, error) {
	claims, err := extractJWTClaims(accessToken)
	if err != nil {
		return "", err
	}

	// Extract the email field
	email, ok := claims["email"].(string)
	if !ok {
		return "", errors.Errorf("email field not found in JWT token")
	}

	return email, nil
}

// extractExpiryFromJWT extracts the expiry time from a JWT access token
func extractExpiryFromJWT(accessToken string) (time.Time, error) {
	claims, err := extractJWTClaims(accessToken)
	if err != nil {
		return time.Time{}, err
	}

	// Extract the exp field (Unix timestamp in seconds)
	exp, ok := claims["exp"].(float64)
	if !ok {
		return time.Time{}, errors.Errorf("exp field not found in JWT token")
	}

	return time.Unix(int64(exp), 0), nil
}

// LoginWithToken authenticates using a token from the Supabase function
func (sa *SupabaseAuth) LoginWithToken(ctx context.Context, token string) error {
	// Parse the token (it's base64 encoded JSON from our function)
	tokenData, err := sa.parseCLIToken(token)
	if err != nil {
		return errors.Errorf("invalid token: %w", err)
	}

	// Extract and validate expiry directly from JWT
	jwtExpiry, err := extractExpiryFromJWT(tokenData.AccessToken)
	if err != nil {
		return errors.Errorf("failed to extract expiry from JWT: %w", err)
	}

	// Check if token is expired
	if jwtExpiry.Before(time.Now()) {
		return errors.Errorf("token expired, please re-authenticate")
	}

	// Extract email from the JWT access token
	email, err := extractEmailFromJWT(tokenData.AccessToken)
	if err != nil {
		// Log the error but don't fail the authentication
		fmt.Fprintf(sa.out, "⚠️  Warning: Could not extract email from token: %v\n", err)
		email = "" // Set empty email as fallback
	}

	// Create session from token data, using JWT expiry as source of truth
	session := &Session{
		AccessToken:  tokenData.AccessToken,
		RefreshToken: "", // CLI tokens don't have refresh tokens
		ExpiresAt:    jwtExpiry,
		User: &User{
			ID:    tokenData.UserID,
			Email: email,
		},
	}

	// Save session
	if err := sa.store.SaveSession(session); err != nil {
		return errors.Errorf("failed to save session: %w", err)
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
