package auth

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/go-errors/errors"
)

// FlyAuth handles authentication checking and login flow for Fly.io
type FlyAuth struct {
	out io.Writer
}

// NewFlyAuth creates a new Fly.io authentication handler
func NewFlyAuth(out io.Writer) *FlyAuth {
	return &FlyAuth{
		out: out,
	}
}

// CheckAuthentication verifies if the user is authenticated with Fly.io
// Returns true if authenticated, false otherwise
func (fa *FlyAuth) CheckAuthentication(ctx context.Context) (bool, error) {
	// First, check if FLY_API_TOKEN environment variable is set
	err := fa.ensureFlyctl()
	if err != nil {
		return false, err
	}
	apiToken := os.Getenv("FLY_API_TOKEN")
	if apiToken != "" {
		// Token exists, validate it by making a test call
		return fa.ValidateAPIKey(ctx, apiToken)
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
	return fa.ValidateAPIKey(ctx, token)
}

// getTokenFromFlyctl attempts to get the auth token from flyctl
func (fa *FlyAuth) getTokenFromFlyctl(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "flyctl", "auth", "token")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(output))
	return token, nil
}

// validateToken validates the API token by making a test API call
func (fa *FlyAuth) ValidateAPIKey(ctx context.Context, token string) (bool, error) {
	// Try to list apps - this is a simple call that requires authentication
	cmd := exec.CommandContext(ctx, "flyctl", "apps", "list", "--json")
	cmd.Env = append(os.Environ(), fmt.Sprintf("FLY_API_TOKEN=%s", token))
	_, err := cmd.Output()
	return err == nil, nil
}

// loginWithBrowser performs browser-based authentication
func (fa *FlyAuth) PerformOAuthLogin(ctx context.Context) error {
	fmt.Fprintln(fa.out, "🌐 Opening browser for authentication...")
	fmt.Fprintln(fa.out, "💡 Complete the authentication in your browser, then return here.")
	fmt.Fprintln(fa.out)

	// Run flyctl auth login interactively
	cmd := exec.CommandContext(ctx, "flyctl", "auth", "login")
	cmd.Stdin = os.Stdin
	cmd.Stdout = fa.out
	cmd.Stderr = fa.out

	if err := cmd.Run(); err != nil {
		return errors.Errorf("authentication failed: %w", err)
	}

	// Get the token and set it in environment
	token, err := fa.getTokenFromFlyctl(ctx)
	if err != nil {
		return errors.Errorf("failed to retrieve auth token: %w", err)
	}

	// Set the token for immediate use
	os.Setenv("FLY_API_TOKEN", token)

	// Ask if user wants to persist the token
	// TODO: figure out a clean way of handling the prompt to save token here
	return nil
}

func (fa *FlyAuth) APIKeyPrompt() string {
	return "🔑 Enter your Fly.io API key (get it from https://fly.io/user/personal_access_tokens):"
}

// ensureFlyctl checks if flyctl is installed
func (fa *FlyAuth) ensureFlyctl() error {
	cmd := exec.Command("flyctl", "version")
	if err := cmd.Run(); err != nil {
		if strings.Contains(err.Error(), "executable file not found") {
			fmt.Fprintln(fa.out, "❌ flyctl is not installed")
			fmt.Fprintln(fa.out)
			fmt.Fprintln(fa.out, "📦 To install flyctl:")
			fmt.Fprintln(fa.out)
			fmt.Fprintln(fa.out, "  On macOS/Linux:")
			fmt.Fprintln(fa.out, "    curl -L https://fly.io/install.sh | sh")
			fmt.Fprintln(fa.out)
			fmt.Fprintln(fa.out, "  On Windows:")
			fmt.Fprintln(fa.out, "    powershell -Command \"iwr https://fly.io/install.ps1 -useb | iex\"")
			fmt.Fprintln(fa.out)
			fmt.Fprintln(fa.out, "  With Homebrew:")
			fmt.Fprintln(fa.out, "    brew install flyctl")
			fmt.Fprintln(fa.out)
			fmt.Fprintln(fa.out, "After installation, run your command again.")
			return errors.Errorf("flyctl is required but not installed")
		}
		return errors.Errorf("failed to check flyctl version: %w", err)
	}
	return nil
}
