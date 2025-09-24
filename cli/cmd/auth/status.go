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
	_ ecdysis.CommandWithExecute = (*StatusCommand)(nil)
	_ ecdysis.CommandWithDocs    = (*StatusCommand)(nil)
)

// StatusCommand shows authentication status
type StatusCommand struct{}

func (c *StatusCommand) Execute(ctx context.Context) error {
	authClient, err := auth.NewSupabaseAuth(os.Stdout)
	if err != nil {
		return errors.Errorf("failed to initialize auth: %w", err)
	}

	if !authClient.IsAuthenticated() {
		fmt.Println("❌ Not authenticated")
		fmt.Println("\nRun 'prod auth login' to authenticate")
		return nil
	}

	session, err := authClient.GetSession()
	if err != nil {
		return errors.Errorf("failed to get session: %w", err)
	}

	fmt.Println("✅ Authenticated")
	if session.User != nil && session.User.Email != "" {
		fmt.Printf("👤 User: %s\n", session.User.Email)
	}
	fmt.Printf("⏰ Expires: %s\n", session.ExpiresAt.Format("2006-01-02 15:04:05"))

	return nil
}

func (c *StatusCommand) Usage() string { return "status" }

func (c *StatusCommand) Docs() ecdysis.Docs {
	return ecdysis.Docs{
		Short: "Show authentication status",
		Long:  `Displays your current authentication status and session information.`,
	}
}
