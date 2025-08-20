package auth

import (
	"github.com/conduitio/ecdysis"
)

var (
	_ ecdysis.CommandWithSubCommands = (*AuthCommand)(nil)
	_ ecdysis.CommandWithDocs        = (*AuthCommand)(nil)
)

// AuthCommand handles authentication operations
type AuthCommand struct {
	Login  LoginCommand  `cmd:"" help:"Authenticate with Prod"`
	Logout LogoutCommand `cmd:"" help:"Sign out of Prod"`
	Status StatusCommand `cmd:"" help:"Show authentication status"`
}

func (c *AuthCommand) SubCommands() []ecdysis.Command {
	return []ecdysis.Command{
		&c.Login,
		&c.Logout,
		&c.Status,
	}
}

func (c *AuthCommand) Usage() string { return "auth" }

func (c *AuthCommand) Docs() ecdysis.Docs {
	return ecdysis.Docs{
		Short: "Manage authentication",
		Long:  `Authenticate with Prod to access deployment features.`,
	}
}