package config

import (
	"strings"
)

// Build-time variables set via ldflags in Makefile
var (
	SupabaseURL                  string
	SupabaseAnonKey              string
	ProdDebug                    string
	Environment                  string
	SentryDSN                    string
	ProdAWSAccountID             string
	AWSCloudFormationTemplateURL string
	Version                      string
)

// GetEnvironment returns the current environment (local, staging, production)
func GetEnvironment() string {
	// Check build-time variable first, then fall back to environment variable
	if Environment != "" {
		return Environment
	}
	return "staging" // Default to staging for local development
}

// GetSupabaseURL returns the Supabase URL
// Uses build-time variable set via ldflags in Makefile
func GetSupabaseURL() string {
	if SupabaseURL != "" {
		return SupabaseURL
	}

	// Fallback defaults based on environment
	env := GetEnvironment()
	switch env {
	case "local":
		return "http://localhost:54321"
	case "staging":
		return "https://PROJECT_REDACTED.supabase.co"
	case "production":
		return "https://PROJECT_REDACTED.supabase.co"
	default:
		return "https://PROJECT_REDACTED.supabase.co"
	}
}

// GetSupabaseAnonKey returns the Supabase anon key
// Uses build-time variable set via ldflags in Makefile
func GetSupabaseAnonKey() string {
	return SupabaseAnonKey
}

// GetProdDebug returns the debug setting
// Uses build-time variable set via ldflags in Makefile
func GetProdDebug() string {
	if ProdDebug != "" {
		return ProdDebug
	}
	return "false" // Default to false
}

func DebugMode() bool {
	return strings.ToLower(GetProdDebug()) == "true"
}

// GetProdAWSAccountID returns the Prod AWS account ID
// Uses build-time variable set via ldflags in Makefile
// Returns empty string if not set
func GetProdAWSAccountID() string {
	return ProdAWSAccountID
}

// GetAWSCloudFormationTemplateURL returns the S3 URL for the CloudFormation template
// Uses build-time variable set via ldflags in Makefile
// Returns empty string if not set
func GetAWSCloudFormationTemplateURL() string {
	return AWSCloudFormationTemplateURL
}
