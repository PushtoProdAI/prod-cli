package config

import (
	"os"
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

// GetSupabaseURL returns the configured backend URL, or "" in local mode.
//
// Precedence: PROD_BACKEND_URL env → SUPABASE_URL env → build-time ldflags → "".
// There is no hard-coded backend fallback: the OSS default is local mode (no
// backend), and a managed/self-hosted backend is opted into via env or ldflags.
func GetSupabaseURL() string {
	if v := os.Getenv("PROD_BACKEND_URL"); v != "" {
		return v
	}
	if v := os.Getenv("SUPABASE_URL"); v != "" {
		return v
	}
	return SupabaseURL // ldflags; empty in a plain OSS build
}

// GetSupabaseAnonKey returns the backend anon key.
// Precedence: SUPABASE_ANON_KEY env → build-time ldflags → "".
func GetSupabaseAnonKey() string {
	if v := os.Getenv("SUPABASE_ANON_KEY"); v != "" {
		return v
	}
	return SupabaseAnonKey
}

// BackendConfigured reports whether a managed/self-hosted backend is available.
// When false, prod runs in local mode: no backend, local state, BYO LLM keys.
func BackendConfigured() bool {
	return GetSupabaseURL() != "" && GetSupabaseAnonKey() != ""
}

// Mode returns the run mode: "managed" when a backend is configured, else "local".
func Mode() string {
	if BackendConfigured() {
		return "managed"
	}
	return "local"
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
