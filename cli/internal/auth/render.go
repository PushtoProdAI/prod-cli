package auth

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/go-errors/errors"
	"github.com/meroxa/prod/cli/internal/deployment/render"
	"github.com/render-oss/cli/pkg/client/oauth"
	"github.com/render-oss/cli/pkg/config"
	"github.com/render-oss/cli/pkg/dashboard"
)

// customTransport wraps an HTTP transport to add custom user-agent headers
// and fix OAuth library Content-Type bug
type customTransport struct {
	transport           http.RoundTripper
	userAgent           string
	fixOAuthContentType bool
}

func (t *customTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Set user-agent header if not already set
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", t.userAgent)
	}

	// Fix the Content-Type header bug in the OAuth library if needed
	// The library sets "application/x-www-form-urlencoded" but sends JSON
	if t.fixOAuthContentType && req.Header.Get("Content-Type") == "application/x-www-form-urlencoded" {
		req.Header.Set("Content-Type", "application/json")
	}

	return t.transport.RoundTrip(req)
}

// RenderAuth handles authentication checking and login flow for Render
type RenderAuth struct {
	client render.RenderClient
	config *config.Config
	output io.Writer
}

// NewRenderAuth creates a new Render authentication handler
func NewRenderAuth(client render.RenderClient, writer io.Writer) *RenderAuth {
	// Load existing config if available
	cfg, _ := config.Load()

	// Use provided writer or fallback to stdout
	if writer == nil {
		writer = os.Stdout
	}

	return &RenderAuth{
		client: client,
		config: cfg,
		output: writer,
	}
}

// printf writes formatted output to the configured writer
func (ra *RenderAuth) printf(format string, args ...any) {
	fmt.Fprintf(ra.output, format, args...)
}

// println writes a line to the configured writer
func (ra *RenderAuth) println(args ...any) {
	fmt.Fprintln(ra.output, args...)
}

// CheckAuthentication verifies if the user is authenticated with Render
// Returns true if authenticated, false otherwise
func (ra *RenderAuth) CheckAuthentication(ctx context.Context) (bool, error) {
	// First, check if RENDER_API_KEY environment variable is set
	apiKey := os.Getenv("RENDER_API_KEY")
	if apiKey != "" {
		// Validate the API key by making a test API call
		return ra.ValidateAPIKey(ctx, apiKey)
	}

	// Check if we have valid stored credentials from Render CLI
	if ra.config != nil && ra.config.Key != "" {
		// Check if the stored key is still valid (check if not expired)
		if ra.config.ExpiresAt == 0 || time.Now().Unix() < ra.config.ExpiresAt {
			// Set the API key from stored config and validate
			os.Setenv("RENDER_API_KEY", ra.config.Key)
			return ra.ValidateAPIKey(ctx, ra.config.Key)
		}
	}

	return false, nil
}

// ValidateAPIKey validates the API key by making a test API call
func (ra *RenderAuth) ValidateAPIKey(ctx context.Context, apiKey string) (bool, error) {
	// Validate API key format
	if len(apiKey) == 0 {
		return false, errors.Errorf("API key cannot be empty")
	}

	if len(apiKey) < 20 {
		return false, errors.Errorf("API key seems too short (should be at least 20 characters)")
	}

	if !strings.HasPrefix(apiKey, "rnd_") {
		return false, errors.New("invalid API key format - Render API keys typically start with 'rnd_'")
	}

	// Try to list workspaces - this is a simple call that requires authenticatio
	os.Setenv("RENDER_API_KEY", apiKey)
	_, err := ra.client.ListWorkspaces(ctx)
	if err != nil {
		// Check if it's an authentication error
		if isAuthError(err) {
			os.Unsetenv("RENDER_API_KEY")
			return false, nil
		}
		// Other error, return it
		return false, errors.Errorf("failed to validate API key: %w", err)
	}

	return true, nil
}

func (ra *RenderAuth) APIKeyPrompt() string {
	return "🔑 Enter your Render API key (get it from https://dashboard.render.com/account/settings):"
}

// PerformOAuthLogin executes the OAuth device authorization flow using Render CLI components
func (ra *RenderAuth) PerformOAuthLogin(ctx context.Context) error {
	ra.println("🚀 Starting authentication flow...")

	// Load Render CLI config to get proper host configuration
	// The render CLI uses config to determine the correct host
	var host string
	if ra.config != nil {
		// Try to get host from loaded config
		if ra.config.Host != "" {
			host = ra.config.Host
			slog.Info("Host from config", "host", host)
			// Ensure the host has a protocol scheme
			if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
				host = "https://" + host + "/v1"
			}
		}
	}

	// Fallback to default if no config or host found
	if host == "" {
		// Use the API endpoint for OAuth - this is where the OAuth endpoints are actually located
		// The default host from Render CLI is https://api.render.com/v1/
		host = "https://api.render.com/v1" // Use the API domain with v1 path for OAuth
	}

	ra.printf("🔧 Using OAuth host: %s\n", host)
	slog.Info("OAuth host being used", "host", host)

	// Set up custom HTTP client with proper user-agent like Render CLI and OAuth Content-Type fix
	customClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &customTransport{
			transport:           http.DefaultTransport,
			userAgent:           fmt.Sprintf("prod-cli/1.0 (%s)", runtime.GOOS),
			fixOAuthContentType: true,
		},
	}

	// Unfortunately, oauth.NewClient doesn't accept a custom HTTP client
	// But we can try to override the default client temporarily
	originalClient := http.DefaultClient
	http.DefaultClient = customClient
	defer func() {
		http.DefaultClient = originalClient
	}()

	oauthClient := oauth.NewClient(host)

	// Try to create device grant with automatic fallback
	fmt.Fprintf(ra.output, "🔗 Connecting to Render authentication server...\n")

	deviceGrant, err := oauthClient.CreateGrant(ctx)
	if err != nil {
		// Log error to log file
		slog.Info("Failed to create device grant", "error", err)

		ra.printf("❌ Failed to connect to Render authentication server\n")
		ra.printf("Error details: %v\n", err)
		ra.println("\n💡 This might be due to:")
		ra.println("   • Network connectivity issues")
		ra.println("   • Render's OAuth service being temporarily unavailable")
		ra.println("   • Firewall or proxy blocking the connection")
		ra.println("\n🔧 You can try option 2 (Manual API key setup) instead")
		return errors.Errorf("failed to create device grant: %w", err)
	}

	// Step 2: Generate dashboard authentication URL and open browser
	ra.printf("\n📱 Please visit: %s\n", deviceGrant.VerificationUri)
	ra.printf("🔑 Enter code: %s\n\n", deviceGrant.UserCode)

	// Use the complete verification URI from the API response
	// This includes the user code in the path: /device-authorization/{user_code}
	authURL := deviceGrant.VerificationUriComplete
	if authURL == "" {
		// Fallback to manual construction if VerificationUriComplete is empty
		authURL = fmt.Sprintf("%s/%s", deviceGrant.VerificationUri, deviceGrant.UserCode)
	}

	// Open browser automatically
	ra.println("🌐 Opening browser automatically...")
	if err := dashboard.Open(authURL); err != nil {
		ra.printf("Failed to open browser automatically: %v\n", err)
		ra.println("Please visit the URL manually.")
	}

	ra.println("⏳ Waiting for authentication...")
	ra.printf("💡 Complete authentication in your browser, then return here.\n")
	ra.printf("⏰ You have %d minutes to complete authentication.\n", deviceGrant.ExpiresIn/60)
	ra.printf("🔗 Browser URL: %s\n\n", authURL)

	// Step 3: Poll for token using render CLI components
	deviceToken, err := ra.pollForToken(ctx, oauthClient, deviceGrant)
	if err != nil {
		return errors.Errorf("authentication failed: %w", err)
	}

	// Step 4: Create and save configuration using render CLI format
	apiConfig := config.APIConfig{
		Host:         "api.render.com",
		Key:          deviceToken.AccessToken,
		ExpiresAt:    time.Now().Add(time.Duration(deviceToken.ExpiresIn) * time.Second).Unix(),
		RefreshToken: deviceToken.RefreshToken,
	}
	cfg := &config.Config{
		Version:   2,
		APIConfig: apiConfig,
	}

	if err := cfg.Persist(); err != nil {
		return errors.Errorf("failed to save authentication: %w", err)
	}

	// Update our local config reference
	ra.config = cfg

	// Set the API key for immediate use
	os.Setenv("RENDER_API_KEY", deviceToken.AccessToken)

	ra.println("✅ Authentication successful!")
	return nil
}

// pollForToken polls for the device token using render CLI components
func (ra *RenderAuth) pollForToken(ctx context.Context, oauthClient *oauth.Client, deviceGrant *oauth.DeviceGrant) (*oauth.DeviceToken, error) {
	timeout := time.NewTimer(time.Duration(deviceGrant.ExpiresIn) * time.Second)
	interval := time.Duration(deviceGrant.Interval) * time.Second
	if interval == 0 {
		interval = 5 * time.Second // Default interval
	}
	ticker := time.NewTicker(interval)

	defer timeout.Stop()
	defer ticker.Stop()

	for {
		select {
		case <-timeout.C:
			return nil, errors.Errorf("authentication timed out")
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			token, err := oauthClient.GetDeviceTokenResponse(ctx, deviceGrant)
			if err != nil {
				// Check for the specific ErrAuthorizationPending error type first
				if errors.Is(err, oauth.ErrAuthorizationPending) {
					// This is expected - user hasn't authenticated yet
					continue // Keep polling
				}

				// Check for specific OAuth error messages as fallback
				errMsg := err.Error()
				if strings.Contains(errMsg, "authorization_pending") {
					// This is expected - user hasn't authenticated yet
					continue // Keep polling
				}
				if strings.Contains(errMsg, "slow_down") {
					// Server is asking us to slow down polling
					ticker.Reset(interval * 2)
					continue
				}
				if strings.Contains(errMsg, "access_denied") {
					return nil, errors.Errorf("user denied authorization")
				}
				if strings.Contains(errMsg, "expired_token") {
					return nil, errors.Errorf("device code expired - please try again")
				}
				// Other error
				return nil, errors.Errorf("authentication error: %w", err)
			}

			if token != nil {
				return token, nil
			}
		}
	}
}

// isAuthError checks if an error is an authentication-related error
func isAuthError(err error) bool {
	if err == nil {
		return false
	}

	errMsg := err.Error()
	return strings.Contains(errMsg, "401") ||
		strings.Contains(errMsg, "unauthorized") ||
		strings.Contains(errMsg, "authentication") ||
		strings.Contains(errMsg, "invalid api key") ||
		strings.Contains(errMsg, "access denied")
}

// persistAPIKeyToShellProfile saves the API key to the user's shell profile
func (ra *RenderAuth) persistAPIKeyToShellProfile(apiKey string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return errors.Errorf("could not get home directory: %w", err)
	}

	// Determine which shell profile to use based on the current shell or common defaults
	shell := os.Getenv("SHELL")
	var profilePath string

	switch {
	case strings.Contains(shell, "zsh"):
		profilePath = filepath.Join(homeDir, ".zshrc")
	case strings.Contains(shell, "bash"):
		// Try .bashrc first, then .bash_profile
		bashrc := filepath.Join(homeDir, ".bashrc")
		bashProfile := filepath.Join(homeDir, ".bash_profile")
		if _, err := os.Stat(bashrc); err == nil {
			profilePath = bashrc
		} else {
			profilePath = bashProfile
		}
	case strings.Contains(shell, "fish"):
		profilePath = filepath.Join(homeDir, ".config", "fish", "config.fish")
	default:
		// Default to .zshrc on macOS (since that's the default shell)
		if runtime.GOOS == "darwin" {
			profilePath = filepath.Join(homeDir, ".zshrc")
		} else {
			profilePath = filepath.Join(homeDir, ".bashrc")
		}
	}

	// Check if the API key already exists in the profile
	existingContent := ""
	if content, err := os.ReadFile(profilePath); err == nil {
		existingContent = string(content)
	}

	// Check if RENDER_API_KEY is already set in the profile
	if strings.Contains(existingContent, "RENDER_API_KEY=") {
		// Update existing entry
		return ra.updateExistingAPIKeyInProfile(profilePath, apiKey, existingContent)
	}

	// Append new entry
	return ra.appendAPIKeyToProfile(profilePath, apiKey)
}

// updateExistingAPIKeyInProfile updates an existing RENDER_API_KEY entry in the shell profile
func (ra *RenderAuth) updateExistingAPIKeyInProfile(profilePath, apiKey, existingContent string) error {
	lines := strings.Split(existingContent, "\n")
	updated := false

	for i, line := range lines {
		if strings.Contains(line, "RENDER_API_KEY=") && !strings.HasPrefix(strings.TrimSpace(line), "#") {
			// Replace the existing line
			lines[i] = fmt.Sprintf("export RENDER_API_KEY=%s", apiKey)
			updated = true
			break
		}
	}

	if !updated {
		// If we couldn't find an uncommented line, append a new one
		return ra.appendAPIKeyToProfile(profilePath, apiKey)
	}

	// Write the updated content back to the file
	updatedContent := strings.Join(lines, "\n")
	return os.WriteFile(profilePath, []byte(updatedContent), 0o644)
}

// appendAPIKeyToProfile appends a new RENDER_API_KEY entry to the shell profile
func (ra *RenderAuth) appendAPIKeyToProfile(profilePath, apiKey string) error {
	// Create the directory if it doesn't exist (for fish config)
	dir := filepath.Dir(profilePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return errors.Errorf("could not create directory %s: %w", dir, err)
	}

	// Open file in append mode, create if it doesn't exist
	file, err := os.OpenFile(profilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return errors.Errorf("could not open profile file %s: %w", profilePath, err)
	}
	defer file.Close()

	// Check if file is empty or doesn't end with a newline
	fileInfo, err := file.Stat()
	if err != nil {
		return errors.Errorf("could not stat profile file: %w", err)
	}

	// If file has content, ensure we start on a new line
	if fileInfo.Size() > 0 {
		// Read the last byte to see if we need a newline
		file.Seek(-1, io.SeekEnd)
		lastByte := make([]byte, 1)
		file.Read(lastByte)
		if lastByte[0] != '\n' {
			if _, err := file.WriteString("\n"); err != nil {
				return errors.Errorf("could not write newline: %w", err)
			}
		}
		file.Seek(0, io.SeekEnd) // Go back to end for appending
	}

	comment := "# Added by prod-cli\n"
	exportStatement := fmt.Sprintf("export RENDER_API_KEY=%s\n", apiKey)

	if _, err := file.WriteString(comment + exportStatement); err != nil {
		return errors.Errorf("could not write to profile file: %w", err)
	}

	return nil
}
