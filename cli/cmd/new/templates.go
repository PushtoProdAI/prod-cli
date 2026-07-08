package newcmd

import "embed"

// templatesFS holds the starter templates, embedded so the binary stays self-contained.
// The `all:` prefix includes dotfiles (e.g. .env.example) that a plain //go:embed would skip.
//
//go:embed all:templates
var templatesFS embed.FS
