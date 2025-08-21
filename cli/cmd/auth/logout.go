package auth

import (
	"context"
	"fmt"
	"os"

	"github.com/conduitio/ecdysis"
	"github.com/meroxa/prod/cli/internal/auth"
)

var (
	_ ecdysis.CommandWithExecute = (*LogoutCommand)(nil)
	_ ecdysis.CommandWithDocs    = (*LogoutCommand)(nil)
)

// LogoutCommand handles user logout
type LogoutCommand struct{}

func (c *LogoutCommand) Execute(ctx context.Context) error {
	authClient, err := auth.NewSupabaseAuth(os.Stdout)
	if err != nil {
		return fmt.Errorf("failed to initialize auth: %w", err)
	}

	if !authClient.IsAuthenticated() {
		fmt.Println("ℹ️  Not currently authenticated")
		return nil
	}

	return authClient.Logout(ctx)
}

func (c *LogoutCommand) Usage() string { return "logout" }

func (c *LogoutCommand) Docs() ecdysis.Docs {
	return ecdysis.Docs{
		Short: "Sign out of Prod",
		Long:  `Removes your stored authentication credentials.`,
	}
}

