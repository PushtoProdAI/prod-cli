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

// VercelAuth handles authentication checking and login flow for Vercel
type VercelAuth struct {
	output io.Writer
}

// NewVercelAuth creates a new Vercel authentication handler
func NewVercelAuth(writer io.Writer) *VercelAuth {
	// Use provided writer or fallback to stdout
	if writer == nil {
		writer = os.Stdout
	}

	return &VercelAuth{
		output: writer,
	}
}

// println writes a line to the configured writer
func (va *VercelAuth) println(args ...any) {
	fmt.Fprintln(va.output, args...)
}

// printf writes formatted output to the configured writer
func (va *VercelAuth) printf(format string, args ...any) {
	fmt.Fprintf(va.output, format, args...)
}

// CheckAuthentication verifies if the user is authenticated with Vercel
// Returns true if authenticated, false otherwise
func (va *VercelAuth) CheckAuthentication(ctx context.Context) (bool, error) {
	// First check if Vercel CLI is installed
	if err := va.ensureVercelCLI(); err != nil {
		return false, err
	}

	// Check if VERCEL_TOKEN environment variable is set
	authToken := os.Getenv("VERCEL_TOKEN")
	if authToken != "" {
		// Validate the auth token by making a test API call
		return va.ValidateAPIKey(ctx, authToken)
	}

	// Check if user is authenticated by running vercel whoami
	// This is more reliable than trying to extract tokens from config files
	return va.isAuthenticatedViaWhoami(ctx)
}

// isAuthenticatedViaWhoami checks authentication by running vercel whoami
func (va *VercelAuth) isAuthenticatedViaWhoami(ctx context.Context) (bool, error) {
	cmd := exec.CommandContext(ctx, "vercel", "whoami")
	output, err := cmd.Output()
	if err != nil {
		// Command failed, likely not authenticated
		return false, nil
	}

	username := strings.TrimSpace(string(output))
	// If we get a valid username (not empty, not error messages), user is authenticated
	if username != "" &&
		!strings.Contains(username, "Not authenticated") &&
		!strings.Contains(username, "Error") &&
		!strings.Contains(username, "Login required") &&
		!strings.Contains(username, "error") {
		return true, nil
	}

	return false, nil
}

// ValidateAPIKey validates the API key by making a test API call
func (va *VercelAuth) ValidateAPIKey(ctx context.Context, token string) (bool, error) {
	// Validate token format
	if len(token) == 0 {
		return false, errors.Errorf("API token cannot be empty")
	}

	// Try to get user info - this requires authentication
	cmd := exec.CommandContext(ctx, "vercel", "whoami")
	cmd.Env = append(os.Environ(), fmt.Sprintf("VERCEL_TOKEN=%s", token))

	output, err := cmd.Output()
	if err != nil {
		// Check if it's an auth error
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr := string(exitErr.Stderr)
			if strings.Contains(stderr, "401") || strings.Contains(stderr, "unauthorized") ||
				strings.Contains(stderr, "authentication") || strings.Contains(stderr, "not authenticated") ||
				strings.Contains(stderr, "login") {
				os.Unsetenv("VERCEL_TOKEN")
				return false, nil
			}
		}
		return false, nil
	}

	// Check if we got a valid username response
	username := strings.TrimSpace(string(output))
	if username == "" || strings.Contains(username, "error") || strings.Contains(username, "Error") {
		return false, nil
	}

	// Token is valid
	os.Setenv("VERCEL_TOKEN", token)
	return true, nil
}

// APIKeyPrompt returns the prompt message for API key input
func (va *VercelAuth) APIKeyPrompt() string {
	return "🔑 Enter your Vercel token (get it from https://vercel.com/account/tokens):"
}

// PerformOAuthLogin performs browser-based authentication using Vercel CLI
func (va *VercelAuth) PerformOAuthLogin(ctx context.Context) error {
	// Ensure Vercel CLI is installed
	if err := va.ensureVercelCLI(); err != nil {
		return err
	}

	va.println("🚀 Starting Vercel authentication...")
	va.println("🌐 Opening browser for authentication...")
	va.println("💡 Complete the authentication in your browser, then return here.")
	va.println()

	cmd := exec.CommandContext(ctx, "vercel", "login")
	cmd.Stdin = os.Stdin
	cmd.Stdout = va.output
	cmd.Stderr = va.output

	if err := cmd.Run(); err != nil {
		return errors.Errorf("authentication failed: %w", err)
	}

	// Verify that authentication succeeded by checking whoami
	va.println("🔍 Verifying authentication...")

	isAuth, err := va.isAuthenticatedViaWhoami(ctx)
	if err != nil {
		return errors.Errorf("failed to verify authentication: %w", err)
	}

	if !isAuth {
		return errors.Errorf("authentication completed but verification failed")
	}

	va.println()
	va.println("✅ Authentication successful!")
	va.println("💡 Vercel CLI is now authenticated and ready to use")

	return nil
}

// ensureVercelCLI checks if vercel CLI is installed
func (va *VercelAuth) ensureVercelCLI() error {
	cmd := exec.Command("vercel", "--version")
	if err := cmd.Run(); err != nil {
		if strings.Contains(err.Error(), "executable file not found") {
			va.println("❌ Vercel CLI is not installed")
			va.println()
			va.println("📦 To install Vercel CLI:")
			va.println()
			va.println("  With npm (recommended):")
			va.println("    npm install -g vercel")
			va.println()
			va.println("  With yarn:")
			va.println("    yarn global add vercel")
			va.println()
			va.println("  With pnpm:")
			va.println("    pnpm add -g vercel")
			va.println()
			va.println("  With Homebrew (macOS/Linux):")
			va.println("    brew install vercel-cli")
			va.println()
			va.println("After installation, run your command again.")
			return errors.Errorf("vercel CLI is required but not installed")
		}
		return errors.Errorf("failed to check vercel version: %w", err)
	}
	return nil
}
