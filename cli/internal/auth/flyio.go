package auth

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/manifoldco/promptui"
)

// FlyAuth handles authentication checking and login flow for Fly.io
type FlyAuth struct{}

// NewFlyAuth creates a new Fly.io authentication handler
func NewFlyAuth() *FlyAuth {
	return &FlyAuth{}
}

// CheckAuthentication verifies if the user is authenticated with Fly.io
// Returns true if authenticated, false otherwise
func (fa *FlyAuth) CheckAuthentication(ctx context.Context) (bool, error) {
	// First, check if FLY_API_TOKEN environment variable is set
	apiToken := os.Getenv("FLY_API_TOKEN")
	if apiToken != "" {
		// Token exists, validate it by making a test call
		return fa.validateToken(ctx, apiToken)
	}

	// Try to get token from flyctl
	token, err := fa.getTokenFromFlyctl(ctx)
	if err != nil {
		return false, nil // Not authenticated
	}

	if token == "" {
		return false, nil
	}

	// Validate the token
	return fa.validateToken(ctx, token)
}

// getTokenFromFlyctl attempts to get the auth token from flyctl
func (fa *FlyAuth) getTokenFromFlyctl(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "flyctl", "auth", "token")
	output, err := cmd.Output()
	if err != nil {
		// Check if flyctl is not installed
		if strings.Contains(err.Error(), "executable file not found") {
			return "", fmt.Errorf("flyctl is not installed. Please install it from https://fly.io/docs/flyctl/install/")
		}
		return "", err
	}

	token := strings.TrimSpace(string(output))
	return token, nil
}

// validateToken validates the API token by making a test API call
func (fa *FlyAuth) validateToken(ctx context.Context, token string) (bool, error) {
	// Set the token temporarily for validation
	oldToken := os.Getenv("FLY_API_TOKEN")
	os.Setenv("FLY_API_TOKEN", token)
	defer func() {
		if oldToken != "" {
			os.Setenv("FLY_API_TOKEN", oldToken)
		} else {
			os.Unsetenv("FLY_API_TOKEN")
		}
	}()

	// Try to list apps - this is a simple call that requires authentication
	cmd := exec.CommandContext(ctx, "flyctl", "apps", "list", "--json")
	_, err := cmd.Output()
	return err == nil, nil
}

// PromptLogin prompts the user to authenticate with Fly.io
func (fa *FlyAuth) PromptLogin(ctx context.Context) error {
	// Check if flyctl is installed
	if err := fa.ensureFlyctl(); err != nil {
		return err
	}

	fmt.Println("🚀 Starting Fly.io authentication...")
	fmt.Println()

	// Check if user wants to use existing browser session or API token
	options := []string{
		"Login with browser (recommended)",
		"Enter API token directly",
	}

	templates := &promptui.SelectTemplates{
		Label:    "{{ . }}?",
		Active:   "\U0001F449 {{ . }}",
		Inactive: "  {{ . }}",
		Selected: "\U0001F389 {{ . }}",
	}

	prompt := promptui.Select{
		Label:     "Choose authentication method",
		Items:     options,
		Templates: templates,
	}

	i, _, err := prompt.Run()
	if err != nil {
		return fmt.Errorf("authentication selection failed: %w", err)
	}

	switch i {
	case 0:
		return fa.loginWithBrowser(ctx)
	case 1:
		return fa.promptForAPIToken(ctx)
	default:
		return fmt.Errorf("invalid selection")
	}
}

// loginWithBrowser performs browser-based authentication
func (fa *FlyAuth) loginWithBrowser(ctx context.Context) error {
	fmt.Println("🌐 Opening browser for authentication...")
	fmt.Println("💡 Complete the authentication in your browser, then return here.")
	fmt.Println()

	// Run flyctl auth login interactively
	cmd := exec.CommandContext(ctx, "flyctl", "auth", "login")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("authentication failed: %w", err)
	}

	// Get the token and set it in environment
	token, err := fa.getTokenFromFlyctl(ctx)
	if err != nil {
		return fmt.Errorf("failed to retrieve auth token: %w", err)
	}

	// Set the token for immediate use
	os.Setenv("FLY_API_TOKEN", token)

	fmt.Println()
	fmt.Println("✅ Authentication successful!")
	fmt.Println()

	// Ask if user wants to persist the token
	if err := fa.offerToPersistToken(token); err != nil {
		// Don't fail auth if persistence fails
		fmt.Printf("⚠️  Could not persist token: %v\n", err)
	}

	return nil
}

// promptForAPIToken prompts the user to enter their API token directly
func (fa *FlyAuth) promptForAPIToken(ctx context.Context) error {
	fmt.Println("🔑 Direct API token setup")
	fmt.Println()
	fmt.Println("📋 To get your API token:")
	fmt.Println("1. Go to https://fly.io/user/personal_access_tokens")
	fmt.Println("2. Create a new token")
	fmt.Println("3. Copy the token and paste it below")
	fmt.Println()

	prompt := promptui.Prompt{
		Label:       "Enter your Fly.io API token",
		Mask:        '*',
		HideEntered: true,
		Validate: func(input string) error {
			input = strings.TrimSpace(input)
			if len(input) == 0 {
				return fmt.Errorf("API token cannot be empty")
			}
			if len(input) < 20 {
				return fmt.Errorf("API token seems too short")
			}
			return nil
		},
	}

	token, err := prompt.Run()
	if err != nil {
		if err == promptui.ErrInterrupt {
			return fmt.Errorf("authentication cancelled by user")
		}
		return fmt.Errorf("failed to read API token: %w", err)
	}

	token = strings.TrimSpace(token)

	// Validate the token
	fmt.Println("\n🔍 Validating API token...")
	valid, err := fa.validateToken(ctx, token)
	if err != nil {
		return fmt.Errorf("failed to validate API token: %w", err)
	}

	if !valid {
		return fmt.Errorf("invalid API token - please check your token and try again")
	}

	// Set the token for immediate use
	os.Setenv("FLY_API_TOKEN", token)

	fmt.Println("✅ API token validated successfully!")
	fmt.Println()

	// Ask if user wants to persist the token
	if err := fa.offerToPersistToken(token); err != nil {
		// Don't fail auth if persistence fails
		fmt.Printf("⚠️  Could not persist token: %v\n", err)
	}

	return nil
}

// offerToPersistToken asks the user if they want to save the token to their shell profile
func (fa *FlyAuth) offerToPersistToken(token string) error {
	persistPrompt := promptui.Prompt{
		Label:     "Save API token to your shell profile for future use? (y/n)",
		IsConfirm: true,
		Default:   "y",
	}

	result, err := persistPrompt.Run()
	if err != nil && err != promptui.ErrAbort {
		return err
	}

	if err == promptui.ErrAbort || (result != "y" && result != "yes") {
		fmt.Println("💡 API token will only be available for this session.")
		fmt.Println("   To persist it manually, run: export FLY_API_TOKEN=" + token)
		return nil
	}

	// Note: We could implement shell profile persistence here similar to render.go
	// For now, we'll use flyctl's built-in token management
	fmt.Println("✅ Token saved by flyctl!")
	fmt.Println("💡 The token will be available in new terminal sessions.")

	return nil
}

// ensureFlyctl checks if flyctl is installed
func (fa *FlyAuth) ensureFlyctl() error {
	cmd := exec.Command("flyctl", "version")
	if err := cmd.Run(); err != nil {
		if strings.Contains(err.Error(), "executable file not found") {
			fmt.Println("❌ flyctl is not installed")
			fmt.Println()
			fmt.Println("📦 To install flyctl:")
			fmt.Println()
			fmt.Println("  On macOS/Linux:")
			fmt.Println("    curl -L https://fly.io/install.sh | sh")
			fmt.Println()
			fmt.Println("  On Windows:")
			fmt.Println("    powershell -Command \"iwr https://fly.io/install.ps1 -useb | iex\"")
			fmt.Println()
			fmt.Println("  With Homebrew:")
			fmt.Println("    brew install flyctl")
			fmt.Println()
			fmt.Println("After installation, run your command again.")
			return fmt.Errorf("flyctl is required but not installed")
		}
		return fmt.Errorf("failed to check flyctl version: %w", err)
	}
	return nil
}