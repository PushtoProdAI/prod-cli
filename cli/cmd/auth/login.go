package auth

import (
	"context"
	"fmt"
	"os"

	"github.com/conduitio/ecdysis"
	"github.com/go-errors/errors"
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
		return errors.Errorf("failed to initialize auth: %w", err)
	}

	if authClient.IsAuthenticated() {
		session, _ := authClient.GetSession()
		if session != nil && session.User != nil {
			fmt.Printf("✅ Already authenticated as: %s\n", session.User.Email)
			fmt.Println("Use 'prod auth logout' to sign out")
			return nil
		}
	}

	// Perform browser-based login using static assets
	fmt.Println("🚀 Starting authentication...")
	if err := authClient.LoginWithBrowser(ctx); err != nil {
		return errors.Errorf("authentication failed: %w", err)
	}

	return nil
}

func (c *LoginCommand) Usage() string { return "login" }

func (c *LoginCommand) Docs() ecdysis.Docs {
	return ecdysis.Docs{
		Short: "Authenticate with PushToProd",
		Long: `Opens your browser to authenticate with PushToProd using OAuth providers or email/password.
		
This command will:
1. Open your browser to the PushToProd authentication page
2. Allow you to sign in with GitHub, Google, or email/password
3. Prompt you to enter the token you receive after authentication
4. Automatically save your session for future CLI use

The authentication session will be stored locally and used for all subsequent CLI commands.`,
	}
}
