package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/meroxa/prod/cli/internal/deployment/netlify"
)

// NetlifyAuth handles authentication checking and login flow for Netlify
type NetlifyAuth struct {
	client netlify.NetlifyClient
	output io.Writer
}

// NewNetlifyAuth creates a new Netlify authentication handler
func NewNetlifyAuth(client netlify.NetlifyClient, writer io.Writer) *NetlifyAuth {
	// Use provided writer or fallback to stdout
	if writer == nil {
		writer = os.Stdout
	}

	return &NetlifyAuth{
		client: client,
		output: writer,
	}
}


// println writes a line to the configured writer
func (na *NetlifyAuth) println(args ...any) {
	fmt.Fprintln(na.output, args...)
}

// CheckAuthentication verifies if the user is authenticated with Netlify
// Returns true if authenticated, false otherwise
func (na *NetlifyAuth) CheckAuthentication(ctx context.Context) (bool, error) {
	// First check if Netlify CLI is installed
	if err := na.ensureNetlifyCLI(); err != nil {
		return false, err
	}

	// Check if NETLIFY_AUTH_TOKEN environment variable is set
	authToken := os.Getenv("NETLIFY_AUTH_TOKEN")
	if authToken != "" {
		// Validate the auth token by making a test API call
		return na.ValidateAPIKey(ctx, authToken)
	}

	// Try to get token from Netlify CLI config
	token, err := na.getTokenFromNetlifyCLI()
	if err != nil {
		return false, nil // Not authenticated
	}

	if token == "" {
		return false, nil
	}

	// Validate the token
	return na.ValidateAPIKey(ctx, token)
}

// getNetlifyConfigPath returns the OS-specific path to Netlify CLI config
func (na *NetlifyAuth) getNetlifyConfigPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	var configPath string
	switch runtime.GOOS {
	case "darwin":
		// macOS: Library/Preferences/netlify/config.json
		configPath = filepath.Join(homeDir, "Library", "Preferences", "netlify", "config.json")
	case "windows":
		// Windows: AppData\Roaming\netlify\Config\config.json
		configPath = filepath.Join(homeDir, "AppData", "Roaming", "netlify", "Config", "config.json")
	default:
		// Linux and others: .config/netlify/config.json
		configPath = filepath.Join(homeDir, ".config", "netlify", "config.json")
	}

	return configPath, nil
}

// getTokenFromNetlifyCLI attempts to get the auth token from Netlify CLI config
func (na *NetlifyAuth) getTokenFromNetlifyCLI() (string, error) {
	configPath, err := na.getNetlifyConfigPath()
	if err != nil {
		return "", err
	}
	
	data, err := os.ReadFile(configPath)
	if err != nil {
		// Config file doesn't exist - not authenticated
		return "", nil
	}
	
	// Netlify CLI config structure
	var config struct {
		TelemetryDisabled bool `json:"telemetryDisabled"`
		CliID            string `json:"cliId"`
		UserID           string `json:"userId"`
		Users            map[string]struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Email string `json:"email"`
			Auth  struct {
				Token string `json:"token"`
			} `json:"auth"`
		} `json:"users"`
	}
	
	if err := json.Unmarshal(data, &config); err != nil {
		return "", err
	}
	
	// Get token from the first user (usually there's only one)
	for _, user := range config.Users {
		if user.Auth.Token != "" {
			return user.Auth.Token, nil
		}
	}
	
	return "", nil
}

// ValidateAPIKey validates the API key by making a test API call
func (na *NetlifyAuth) ValidateAPIKey(ctx context.Context, token string) (bool, error) {
	// Validate token format
	if len(token) == 0 {
		return false, fmt.Errorf("API token cannot be empty")
	}

	// Try to list sites with the token - this requires authentication
	cmd := exec.CommandContext(ctx, "netlify", "api", "listSites", "--data", "{}")
	cmd.Env = append(os.Environ(), fmt.Sprintf("NETLIFY_AUTH_TOKEN=%s", token))
	
	output, err := cmd.Output()
	if err != nil {
		// Check if it's an auth error
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr := string(exitErr.Stderr)
			if strings.Contains(stderr, "401") || strings.Contains(stderr, "unauthorized") || 
			   strings.Contains(stderr, "authentication") || strings.Contains(stderr, "logged in") {
				os.Unsetenv("NETLIFY_AUTH_TOKEN")
				return false, nil
			}
		}
		return false, nil
	}
	
	// Check if response is valid JSON (successful API call)
	var result any
	if err := json.Unmarshal(output, &result); err != nil {
		return false, nil
	}
	
	// Token is valid
	os.Setenv("NETLIFY_AUTH_TOKEN", token)
	return true, nil
}

// APIKeyPrompt returns the prompt message for API key input
// Since we don't support manual token entry, this returns an error message
func (na *NetlifyAuth) APIKeyPrompt() string {
	return "❌ Manual token entry is not supported for Netlify. Please use 'netlify login' instead."
}

// PerformOAuthLogin performs browser-based authentication using Netlify CLI
func (na *NetlifyAuth) PerformOAuthLogin(ctx context.Context) error {
	// Ensure Netlify CLI is installed
	if err := na.ensureNetlifyCLI(); err != nil {
		return err
	}

	na.println("🚀 Starting Netlify authentication...")
	na.println("🌐 Opening browser for authentication...")
	na.println("💡 Complete the authentication in your browser, then return here.")
	na.println()

	// Run netlify login interactively
	// Netlify CLI handles all the token storage automatically
	cmd := exec.CommandContext(ctx, "netlify", "login")
	cmd.Stdin = os.Stdin
	cmd.Stdout = na.output
	cmd.Stderr = na.output

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("authentication failed: %w", err)
	}

	// Verify that authentication succeeded by getting the token
	token, err := na.getTokenFromNetlifyCLI()
	if err != nil {
		return fmt.Errorf("failed to verify authentication: %w", err)
	}

	if token == "" {
		return fmt.Errorf("authentication succeeded but no token found")
	}

	// Set the token for immediate use in this session
	os.Setenv("NETLIFY_AUTH_TOKEN", token)

	na.println()
	na.println("✅ Authentication successful!")
	na.println("💡 Netlify CLI has saved your credentials")
	na.println("📍 Token location: " + na.getConfigLocation())
	
	return nil
}

// getConfigLocation returns a user-friendly description of where the config is stored
func (na *NetlifyAuth) getConfigLocation() string {
	switch runtime.GOOS {
	case "darwin":
		return "~/Library/Preferences/netlify/config.json"
	case "windows":
		return "%AppData%\\Roaming\\netlify\\Config\\config.json"
	default:
		return "~/.config/netlify/config.json"
	}
}

// ensureNetlifyCLI checks if netlify CLI is installed
func (na *NetlifyAuth) ensureNetlifyCLI() error {
	cmd := exec.Command("netlify", "--version")
	if err := cmd.Run(); err != nil {
		if strings.Contains(err.Error(), "executable file not found") {
			na.println("❌ Netlify CLI is not installed")
			na.println()
			na.println("📦 To install Netlify CLI:")
			na.println()
			na.println("  With npm (recommended):")
			na.println("    npm install -g netlify-cli")
			na.println()
			na.println("  With Homebrew (macOS/Linux):")
			na.println("    brew install netlify-cli")
			na.println()
			na.println("  With yarn:")
			na.println("    yarn global add netlify-cli")
			na.println()
			na.println("After installation, run your command again.")
			return fmt.Errorf("netlify CLI is required but not installed")
		}
		return fmt.Errorf("failed to check netlify version: %w", err)
	}
	return nil
}