package auth

import (
	"context"
	"fmt"
	"os"

	"github.com/conduitio/ecdysis"
	"github.com/meroxa/prod/cli/internal/auth"
)

var (
	_ ecdysis.CommandWithExecute = (*LoginCommand)(nil)
	_ ecdysis.CommandWithDocs    = (*LoginCommand)(nil)
)

// LoginCommand handles user login
type LoginCommand struct{}

func (c *LoginCommand) Execute(ctx context.Context) error {
	// Check if already authenticated
	authClient, err := auth.NewSupabaseAuth(os.Stdout)
	if err != nil {
		return fmt.Errorf("failed to initialize auth: %w", err)
	}

	if authClient.IsAuthenticated() {
		session, _ := authClient.GetSession()
		if session != nil && session.User != nil {
			fmt.Printf("✅ Already authenticated as: %s\n", session.User.Email)
			fmt.Println("Use 'prod auth logout' to sign out")
			return nil
		}
	}

	// Perform browser-based login
	fmt.Println("🚀 Starting authentication...")
	if err := authClient.LoginWithBrowser(ctx); err != nil {
		return fmt.Errorf("authentication failed: %w", err)
	}

	return nil
}

func (c *LoginCommand) Usage() string { return "login" }

func (c *LoginCommand) Docs() ecdysis.Docs {
	return ecdysis.Docs{
		Short: "Authenticate with Prod",
		Long:  `Opens your browser to authenticate with Prod using Supabase authentication.`,
	}
}

