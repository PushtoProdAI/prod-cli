package netlify

import (
	"time"
)

// Default configuration constants for Netlify deployments
const (
	// API configuration
	defaultAPIURL = "https://api.netlify.com/api/v1"
	
	// Deployment timeouts
	deployTimeout       = 10 * time.Minute
	deployPollInterval  = 5 * time.Second
	deployMaxAttempts   = 60 // 5 minutes with 5-second intervals
	
	// Build configuration
	defaultBuildTimeout = 15 * time.Minute
	
	// Retry configuration
	maxRetries   = 3
	retryDelay   = 2 * time.Second
	retryBackoff = 1.5
	
	// File upload configuration
	maxConcurrentUploads = 10
	uploadTimeout        = 30 * time.Second
	
	// Function configuration
	functionRuntime = "js"
)

// Common build output directories
var commonBuildDirs = []string{
	"dist",
	"build",
	"out",
	"public",
	"_site",
	".next",
	"output",
}

// Common function directories
var commonFunctionDirs = []string{
	"netlify/functions",
	"functions",
	".netlify/functions",
	"api",
}

// Framework-specific configuration
var frameworkConfig = map[string]FrameworkConfig{
	"react": {
		BuildCommand: "npm run build",
		PublishDir:   "build",
	},
	"vue": {
		BuildCommand: "npm run build",
		PublishDir:   "dist",
	},
	"angular": {
		BuildCommand: "ng build",
		PublishDir:   "dist",
	},
	"next": {
		BuildCommand: "next build && next export",
		PublishDir:   "out",
	},
	"gatsby": {
		BuildCommand: "gatsby build",
		PublishDir:   "public",
	},
	"hugo": {
		BuildCommand: "hugo",
		PublishDir:   "public",
	},
	"jekyll": {
		BuildCommand: "jekyll build",
		PublishDir:   "_site",
	},
	"eleventy": {
		BuildCommand: "eleventy",
		PublishDir:   "_site",
	},
}

// FrameworkConfig holds framework-specific deployment configuration
type FrameworkConfig struct {
	BuildCommand string
	PublishDir   string
	Environment  map[string]string
}

// GetFrameworkConfig returns configuration for a specific framework
func GetFrameworkConfig(framework string) (FrameworkConfig, bool) {
	config, ok := frameworkConfig[framework]
	return config, ok
}

// GetCommonBuildDirs returns the list of common build output directories
func GetCommonBuildDirs() []string {
	return commonBuildDirs
}

// GetCommonFunctionDirs returns the list of common function directories
func GetCommonFunctionDirs() []string {
	return commonFunctionDirs
}