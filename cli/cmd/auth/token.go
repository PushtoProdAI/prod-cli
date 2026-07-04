package auth

import (
	"context"
	"fmt"
	"os"

	"github.com/conduitio/ecdysis"
	"github.com/go-errors/errors"
	"github.com/pushtoprodai/prod-cli/internal/auth"
)

var (
	_ ecdysis.CommandWithExecute = (*TokenCommand)(nil)
	_ ecdysis.CommandWithDocs    = (*TokenCommand)(nil)
)

// TokenCommand handles token-based authentication
type TokenCommand struct {
	Token string `arg:"" help:"Authentication token from the web interface"`
}

func (c *TokenCommand) Execute(ctx context.Context) error {
	// Initialize auth client
	authClient, err := auth.NewSupabaseAuth(os.Stdout)
	if err != nil {
		return errors.Errorf("failed to initialize auth: %w", err)
	}

	// Check if already authenticated
	if authClient.IsAuthenticated() {
		session, _ := authClient.GetSession()
		if session != nil && session.User != nil {
			fmt.Printf("✅ Already authenticated as: %s\n", session.User.Email)
			fmt.Println("Use 'prod auth logout' to sign out first")
			return nil
		}
	}

	// Authenticate with token
	fmt.Println("🔐 Authenticating with token...")
	if err := authClient.LoginWithToken(ctx, c.Token); err != nil {
		return errors.Errorf("authentication failed: %w", err)
	}

	return nil
}

func (c *TokenCommand) Usage() string { return "token <token>" }

func (c *TokenCommand) Docs() ecdysis.Docs {
	return ecdysis.Docs{
		Short: "Authenticate with a token from the web interface",
		Long: `Authenticate using a token received from the PushToProd web interface.
		
After running 'prod auth login', you'll be redirected to a web page where you can
authenticate with GitHub or Google. Once authenticated, you'll receive a token
that you can use with this command.

Example:
  prod auth token eyJ1c2VyX2lkIjoiMTIzNDU2Nzg5MCIsImFjY2Vzc190b2tlbiI6Ii4uLiJ9`,
	}
}
