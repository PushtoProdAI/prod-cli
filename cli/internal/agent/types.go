package agent

// Core types for the agent package.
// These types are used across multiple files and represent the fundamental
// data structures for deployment planning and execution.

//go:generate stringer -type=Platform,Action -output=types_string.go

// Platform represents a deployment target platform
type Platform int

const (
	Render Platform = iota
	FlyIO
	Netlify
	Vercel
	Heroku
	AWS
	GoogleCloudRun
	Azure
	UnknownPlatform
)

// Action represents the type of deployment action
type Action int

const (
	Deploy Action = iota
	Rollback
	UnknownAction
	// Destroy is appended last so the serialized (int) values of the existing
	// actions stay stable for durable workflows in flight across an upgrade.
	Destroy
)

// DiffLine represents a single line in a diff
type DiffLine struct {
	Type    string `json:"type"`    // "context", "added", "removed", "header", "fileheader"
	Content string `json:"content"` // the actual line content
}

// Remediation provides a suggested fix for deployment errors
type Remediation struct {
	Description string
	CliCommand  string
}

// deployError represents an error during deployment with optional remediations
type deployError struct {
	Summary      string
	Remediations []Remediation
	IsWarning    bool
}

// deployResult represents the result of a deployment operation
type deployResult struct {
	Url   string
	Error deployError
}
