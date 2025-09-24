package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/go-errors/errors"

	"github.com/meroxa/prod/cli/internal/deployment/heroku"
)

const (
	herokuAPIHost    = "api.heroku.com"
	herokuLoginHost  = "id.heroku.com"
	herokuAPIURL     = "https://api.heroku.com"
	herokuCLIAuthURL = "https://cli-auth.heroku.com"
)

// HerokuAuth handles authentication checking and login flow for Heroku
type HerokuAuth struct {
	client *heroku.HerokuClient
	output io.Writer
	netrc  *NetrcConfig
}

// NetrcConfig represents the .netrc file configuration
type NetrcConfig struct {
	path     string
	machines map[string]*NetrcEntry
}

// NetrcEntry represents a single machine entry in .netrc
type NetrcEntry struct {
	Login    string
	Password string
}

// NewHerokuAuth creates a new Heroku authentication handler
func NewHerokuAuth(client *heroku.HerokuClient, writer io.Writer) *HerokuAuth {
	if writer == nil {
		writer = os.Stdout
	}

	return &HerokuAuth{
		client: client,
		output: writer,
		netrc:  loadNetrc(),
	}
}

// loadNetrc loads the .netrc file configuration
func loadNetrc() *NetrcConfig {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return &NetrcConfig{machines: make(map[string]*NetrcEntry)}
	}

	netrcPath := filepath.Join(homeDir, ".netrc")
	// For Windows, the file is named _netrc
	if runtime.GOOS == "windows" {
		netrcPath = filepath.Join(homeDir, "_netrc")
	}

	nc := &NetrcConfig{
		path:     netrcPath,
		machines: make(map[string]*NetrcEntry),
	}

	// Try to read and parse the netrc file
	content, err := os.ReadFile(netrcPath)
	if err != nil {
		return nc
	}

	// Simple parser for netrc format
	lines := strings.Split(string(content), "\n")
	var currentMachine string
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		switch fields[0] {
		case "machine":
			currentMachine = fields[1]
			if _, ok := nc.machines[currentMachine]; !ok {
				nc.machines[currentMachine] = &NetrcEntry{}
			}
		case "login":
			if currentMachine != "" && nc.machines[currentMachine] != nil {
				nc.machines[currentMachine].Login = fields[1]
			}
		case "password":
			if currentMachine != "" && nc.machines[currentMachine] != nil {
				nc.machines[currentMachine].Password = fields[1]
			}
		}
	}

	return nc
}

// printf writes formatted output to the configured writer
func (ha *HerokuAuth) printf(format string, args ...any) {
	fmt.Fprintf(ha.output, format, args...)
}

// println writes a line to the configured writer
func (ha *HerokuAuth) println(args ...any) {
	fmt.Fprintln(ha.output, args...)
}

// CheckAuthentication verifies if the user is authenticated with Heroku
func (ha *HerokuAuth) CheckAuthentication(ctx context.Context) (bool, error) {
	// First, check if HEROKU_API_KEY environment variable is set
	apiKey := os.Getenv("HEROKU_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("HEROKU_AUTH_TOKEN")
	}

	if apiKey != "" {
		// Validate the API key by making a test API call
		return ha.ValidateAPIKey(ctx, apiKey)
	}

	// Check if we have valid stored credentials in .netrc
	if entry, ok := ha.netrc.machines[herokuAPIHost]; ok && entry != nil {
		if entry.Password != "" {
			// Set the API key from netrc and validate
			os.Setenv("HEROKU_API_KEY", entry.Password)
			return ha.ValidateAPIKey(ctx, entry.Password)
		}
	}

	// Try to use Heroku CLI if installed
	if ha.isHerokuCLIAvailable() {
		apiKey, err := ha.getAPIKeyFromCLI()
		if err == nil && apiKey != "" {
			os.Setenv("HEROKU_API_KEY", apiKey)
			return ha.ValidateAPIKey(ctx, apiKey)
		}
	}

	return false, nil
}

// ValidateAPIKey validates the API key by making a test API call
func (ha *HerokuAuth) ValidateAPIKey(ctx context.Context, apiKey string) (bool, error) {
	// Validate API key format
	if len(apiKey) == 0 {
		return false, errors.Errorf("API key cannot be empty")
	}

	// Create a test client with the API key
	testClient := heroku.NewHerokuClient(apiKey, ha.output)

	// Try to list apps - this is a simple call that requires authentication
	_, err := testClient.ListApps(ctx)
	if err != nil {
		// Check if it's an authentication error
		if isHerokuAuthError(err) {
			os.Unsetenv("HEROKU_API_KEY")
			return false, nil
		}
		// Other error, return it
		return false, errors.Errorf("failed to validate API key: %w", err)
	}

	return true, nil
}

// APIKeyPrompt returns the prompt message for manual API key entry
func (ha *HerokuAuth) APIKeyPrompt() string {
	return "🔑 Enter your Heroku API key (get it from https://dashboard.heroku.com/account):"
}

// PerformOAuthLogin executes the OAuth device authorization flow for Heroku
func (ha *HerokuAuth) PerformOAuthLogin(ctx context.Context) error {
	ha.println("🚀 Starting Heroku authentication flow...")

	// Try browser-based authentication using Heroku's CLI auth service
	if err := ha.browserLogin(ctx); err == nil {
		return nil
	}

	// Fall back to manual API key entry
	ha.println("\n⚠️  Browser authentication unavailable.")
	ha.println("📝 Please follow these steps to authenticate manually:")
	ha.println("")
	ha.println("1. Go to: https://dashboard.heroku.com/account")
	ha.println("2. Scroll to 'API Key' section")
	ha.println("3. Click 'Reveal' to show your API key")
	ha.println("4. Copy the API key")
	ha.println("5. Paste it when prompted")
	ha.println("")

	return errors.Errorf("manual API key entry required")
}

// browserLogin implements Heroku's browser-based login flow
func (ha *HerokuAuth) browserLogin(ctx context.Context) error {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "unknown"
	}

	// Step 1: Request authentication URLs from Heroku's CLI auth service
	authRequest := map[string]interface{}{
		"description": fmt.Sprintf("Heroku CLI login from %s", hostname),
	}

	jsonData, err := json.Marshal(authRequest)
	if err != nil {
		return errors.Errorf("failed to create auth request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", herokuCLIAuthURL+"/auth", bytes.NewBuffer(jsonData))
	if err != nil {
		return errors.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", fmt.Sprintf("prod-cli/1.0 (%s)", runtime.GOOS))

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return errors.Errorf("failed to request auth URLs: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return errors.Errorf("auth request failed (status %d): %s", resp.StatusCode, string(body))
	}

	// Parse the response to get browser_url, cli_url, and token
	var authURLs struct {
		BrowserURL string `json:"browser_url"`
		CLIURL     string `json:"cli_url"`
		Token      string `json:"token"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&authURLs); err != nil {
		return errors.Errorf("failed to parse auth response: %w", err)
	}

	// Step 2: Open browser for authentication
	browserURL := herokuCLIAuthURL + authURLs.BrowserURL
	ha.printf("\n📱 Opening browser for authentication...\n")
	ha.printf("🔗 Login URL: %s\n\n", browserURL)

	if err := openBrowser(browserURL); err != nil {
		ha.printf("⚠️  Could not open browser automatically: %v\n", err)
		ha.printf("Please visit the URL manually.\n")
	}

	ha.println("⏳ Waiting for authentication...")
	ha.println("💡 Complete authentication in your browser, then return here.")

	// Step 3: Poll for authentication result
	return ha.pollForAuth(ctx, authURLs.CLIURL, authURLs.Token)
}

// pollForAuth polls the CLI URL for authentication result
func (ha *HerokuAuth) pollForAuth(ctx context.Context, cliURL, token string) error {
	pollURL := herokuCLIAuthURL + cliURL
	timeout := time.After(10 * time.Minute)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	retries := 0

	for {
		select {
		case <-timeout:
			ha.println("\n⏰ Authentication timed out.")
			return errors.Errorf("authentication timed out")

		case <-ctx.Done():
			return ctx.Err()

		case <-ticker.C:
			// Make polling request
			req, err := http.NewRequestWithContext(ctx, "GET", pollURL, nil)
			if err != nil {
				continue
			}

			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Accept", "application/json")
			req.Header.Set("User-Agent", fmt.Sprintf("prod-cli/1.0 (%s)", runtime.GOOS))

			client := &http.Client{Timeout: 10 * time.Second}
			resp, err := client.Do(req)
			if err != nil {
				continue
			}

			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			// Handle server errors with retry (matching TypeScript's fetchAuth)
			if resp.StatusCode >= 500 {
				retries++
				if retries > 3 {
					// After 3 retries, continue polling rather than failing completely
					retries = 0
					continue
				}
				continue
			}

			// Reset retries on non-500 response
			retries = 0

			if resp.StatusCode == 200 {
				// Parse the authentication result
				var authResult struct {
					AccessToken string `json:"access_token"`
					Error       string `json:"error"`
				}

				if err := json.Unmarshal(body, &authResult); err != nil {
					continue
				}

				if authResult.Error != "" {
					return errors.Errorf("authentication error: %s", authResult.Error)
				}

				if authResult.AccessToken != "" {
					// Authentication successful!
					ha.println("\n✅ Authentication successful!")

					// Set the API key in environment (matching Heroku CLI behavior)
					os.Setenv("HEROKU_API_KEY", authResult.AccessToken)

					// Fetch account email
					email := "user@heroku"
					if accountEmail, err := ha.fetchAccountEmail(ctx, authResult.AccessToken); err == nil {
						email = accountEmail
					}

					// Save the token to .netrc
					return ha.saveToken(authResult.AccessToken, email)
				}
			}

			// Continue polling if we haven't got a result yet
		}
	}
}

// fetchAccountEmail fetches the email associated with the API token
func (ha *HerokuAuth) fetchAccountEmail(ctx context.Context, token string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", herokuAPIURL+"/account", nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("Accept", "application/vnd.heroku+json; version=3")
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", errors.Errorf("failed to fetch account info: status %d", resp.StatusCode)
	}

	var account struct {
		Email string `json:"email"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&account); err != nil {
		return "", err
	}

	return account.Email, nil
}

// saveToken saves the authentication token to .netrc
func (ha *HerokuAuth) saveToken(token, email string) error {
	// Update netrc entries
	hosts := []string{herokuAPIHost, "git.heroku.com"}
	for _, host := range hosts {
		if ha.netrc.machines[host] == nil {
			ha.netrc.machines[host] = &NetrcEntry{}
		}
		ha.netrc.machines[host].Login = email
		ha.netrc.machines[host].Password = token
	}

	// Save to file
	return ha.saveNetrc()
}

// saveNetrc saves the netrc configuration to file
func (ha *HerokuAuth) saveNetrc() error {
	var content strings.Builder

	for machine, entry := range ha.netrc.machines {
		if entry.Login == "" && entry.Password == "" {
			continue
		}
		content.WriteString(fmt.Sprintf("machine %s\n", machine))
		if entry.Login != "" {
			content.WriteString(fmt.Sprintf("  login %s\n", entry.Login))
		}
		if entry.Password != "" {
			content.WriteString(fmt.Sprintf("  password %s\n", entry.Password))
		}
		content.WriteString("\n")
	}

	// Set proper permissions (0600 for Unix-like systems)
	perm := os.FileMode(0600)
	if runtime.GOOS == "windows" {
		perm = 0644
	}

	return os.WriteFile(ha.netrc.path, []byte(content.String()), perm)
}

// isHerokuCLIAvailable checks if Heroku CLI is installed
func (ha *HerokuAuth) isHerokuCLIAvailable() bool {
	cmd := exec.Command("heroku", "version")
	err := cmd.Run()
	return err == nil
}

// getAPIKeyFromCLI attempts to get the API key from Heroku CLI
func (ha *HerokuAuth) getAPIKeyFromCLI() (string, error) {
	cmd := exec.Command("heroku", "auth:token")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	token := strings.TrimSpace(string(output))
	if token == "" {
		return "", errors.New("no token from Heroku CLI")
	}

	return token, nil
}

// LoginWithCLI uses Heroku CLI for authentication
func (ha *HerokuAuth) LoginWithCLI(ctx context.Context) error {
	if !ha.isHerokuCLIAvailable() {
		return errors.Errorf("Heroku CLI is not installed. Please install it from https://devcenter.heroku.com/articles/heroku-cli")
	}

	ha.println("🔐 Using Heroku CLI for authentication...")

	// Run heroku login command
	cmd := exec.CommandContext(ctx, "heroku", "login")
	cmd.Stdin = os.Stdin
	cmd.Stdout = ha.output
	cmd.Stderr = ha.output

	if err := cmd.Run(); err != nil {
		return errors.Errorf("Heroku CLI login failed: %w", err)
	}

	// Get the token from CLI
	token, err := ha.getAPIKeyFromCLI()
	if err != nil {
		return errors.Errorf("failed to retrieve token after login: %w", err)
	}

	// Set the environment variable
	os.Setenv("HEROKU_API_KEY", token)

	ha.println("✅ Authentication successful via Heroku CLI!")
	return nil
}

// isHerokuAuthError checks if an error is an authentication-related error
func isHerokuAuthError(err error) bool {
	if err == nil {
		return false
	}

	errMsg := strings.ToLower(err.Error())
	return strings.Contains(errMsg, "401") ||
		strings.Contains(errMsg, "unauthorized") ||
		strings.Contains(errMsg, "authentication") ||
		strings.Contains(errMsg, "invalid credentials") ||
		strings.Contains(errMsg, "forbidden")
}

// SaveAPIKey saves an API key to the environment and optionally to .netrc
func (ha *HerokuAuth) SaveAPIKey(apiKey string, persist bool) error {
	// Set environment variable
	os.Setenv("HEROKU_API_KEY", apiKey)

	if persist {
		// For now, use a generic email placeholder
		// In a real implementation, we would fetch the account info
		email := "user@heroku"

		if err := ha.saveToken(apiKey, email); err != nil {
			slog.Warn("Failed to save token to .netrc", "error", err)
			// Don't fail - the env var is set
		}
	}

	return nil
}
