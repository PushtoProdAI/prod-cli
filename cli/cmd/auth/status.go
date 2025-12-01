package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

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

type authStatusJSON struct {
	Type          string `json:"type"`
	Authenticated bool   `json:"authenticated"`
	User          string `json:"user,omitempty"`
	ExpiresAt     string `json:"expires_at,omitempty"`
}

func (c *StatusCommand) Execute(ctx context.Context) error {
	// TODO: Future improvement - use StatusWriter for consistency with rest of app
	// For now, auth commands check PROD_JSON_MODE directly to keep them standalone
	authClient, err := auth.NewSupabaseAuth(os.Stdout)
	if err != nil {
		return errors.Errorf("failed to initialize auth: %w", err)
	}

	jsonMode := os.Getenv("PROD_JSON_MODE") == "true"

	if !authClient.IsAuthenticated() {
		if jsonMode {
			status := authStatusJSON{
				Type:          "auth_status",
				Authenticated: false,
			}
			json.NewEncoder(os.Stdout).Encode(status)
		} else {
			fmt.Println("❌ Not authenticated")
			fmt.Println("\nRun 'prod auth login' to authenticate")
		}
		return nil
	}

	session, err := authClient.GetSession()
	if err != nil {
		return errors.Errorf("failed to get session: %w", err)
	}

	if jsonMode {
		status := authStatusJSON{
			Type:          "auth_status",
			Authenticated: true,
		}
		if session.User != nil && session.User.Email != "" {
			status.User = session.User.Email
		}
		status.ExpiresAt = session.ExpiresAt.Format(time.RFC3339)
		json.NewEncoder(os.Stdout).Encode(status)
	} else {
		fmt.Println("✅ Authenticated")
		if session.User != nil && session.User.Email != "" {
			fmt.Printf("👤 User: %s\n", session.User.Email)
		}
		fmt.Printf("⏰ Expires: %s\n", session.ExpiresAt.Format("2006-01-02 15:04:05"))
	}

	return nil
}

func (c *StatusCommand) Usage() string { return "status" }

func (c *StatusCommand) Docs() ecdysis.Docs {
	return ecdysis.Docs{
		Short: "Show authentication status",
		Long:  `Displays your current authentication status and session information.`,
	}
}
