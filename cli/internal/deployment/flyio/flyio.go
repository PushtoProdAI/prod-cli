package flyio

import (
	"time"
)

// Default configuration constants for Fly.io deployments
const (
	// Region configuration
	defaultRegion = "iad" // Northern Virginia (US East)

	// Organization configuration
	defaultOrg = "personal"

	// PostgreSQL configuration
	postgresInitialClusterSize = 1
	postgresVolumeSizeGB       = 10
	postgresImageRef           = "flyio/postgres:16" // Latest stable PostgreSQL

	// Redis configuration
	redisImageRef = "flyio/redis:7" // Latest stable Redis

	// Machine sizing
	defaultMachineSize = "shared-cpu-1x" // 1 shared CPU, 256MB RAM

	// Deployment timeouts
	deployTimeout        = 10 * time.Minute
	serviceReadyTimeout  = 5 * time.Minute
	serviceReadyInterval = 10 * time.Second

	// Retry configuration
	maxRetries   = 3
	retryDelay   = 2 * time.Second
	retryBackoff = 1.5

	// Port configuration
	defaultHTTPPort     = 8080
	defaultHTTPSPort    = 443
	postgresDefaultPort = 5432
	redisDefaultPort    = 6379
)

// Language-specific configuration
var languageConfig = map[string]LanguageConfig{
	"python": {
		Builder:      "paketobuildpacks/builder:base",
		InternalPort: 8000,
		BuildArgs:    []string{},
	},
	"node": {
		Builder:      "paketobuildpacks/builder:base",
		InternalPort: 3000,
		BuildArgs:    []string{},
	},
	"nodejs": {
		Builder:      "paketobuildpacks/builder:base",
		InternalPort: 3000,
		BuildArgs:    []string{},
	},
	"javascript": {
		Builder:      "paketobuildpacks/builder:base",
		InternalPort: 3000,
		BuildArgs:    []string{},
	},
	"go": {
		Builder:      "paketobuildpacks/builder:base",
		InternalPort: 8080,
		BuildArgs:    []string{},
	},
	"golang": {
		Builder:      "paketobuildpacks/builder:base",
		InternalPort: 8080,
		BuildArgs:    []string{},
	},
	"ruby": {
		Builder:      "paketobuildpacks/builder:base",
		InternalPort: 3000,
		BuildArgs:    []string{},
	},
	"php": {
		Builder:      "paketobuildpacks/builder:base",
		InternalPort: 8000,
		BuildArgs:    []string{},
	},
	"rust": {
		Builder:      "paketobuildpacks/builder:base",
		InternalPort: 8080,
		BuildArgs:    []string{},
	},
	"java": {
		Builder:      "paketobuildpacks/builder:base",
		InternalPort: 8080,
		BuildArgs:    []string{},
	},
}

// LanguageConfig holds language-specific deployment configuration
type LanguageConfig struct {
	Builder      string
	InternalPort int
	BuildArgs    []string
}

// GetLanguageConfig returns configuration for a specific language
func GetLanguageConfig(language string) LanguageConfig {
	if config, ok := languageConfig[language]; ok {
		return config
	}
	// Return default configuration for unknown languages
	return LanguageConfig{
		Builder:      "paketobuildpacks/builder:base",
		InternalPort: defaultHTTPPort,
		BuildArgs:    []string{},
	}
}

// FlyioPricing contains pricing information for Fly.io services
// Note: These are approximate values for estimation purposes
type FlyioPricing struct {
	Machines  map[string]float64 `json:"machines"`
	Databases map[string]float64 `json:"databases"`
	Redis     map[string]float64 `json:"redis"`
	Storage   float64            `json:"storage_per_gb"`
}

// GetEstimatedPricing returns estimated pricing for Fly.io services
func GetEstimatedPricing() FlyioPricing {
	return FlyioPricing{
		Machines: map[string]float64{
			"shared-cpu-1x":  5.70,   // 1 shared CPU, 256MB RAM
			"shared-cpu-2x":  11.40,  // 2 shared CPUs, 512MB RAM
			"shared-cpu-4x":  22.80,  // 4 shared CPUs, 1GB RAM
			"shared-cpu-8x":  45.60,  // 8 shared CPUs, 2GB RAM
			"performance-1x": 62.00,  // 1 dedicated CPU, 2GB RAM
			"performance-2x": 124.00, // 2 dedicated CPUs, 4GB RAM
			"performance-4x": 248.00, // 4 dedicated CPUs, 8GB RAM
			"performance-8x": 496.00, // 8 dedicated CPUs, 16GB RAM
		},
		Databases: map[string]float64{
			"basic":   38.00,
			"starter": 72.00,
			"launch":  282.00,
			"scale":   962.00,
		},
		Redis: map[string]float64{
			"redis-shared":    5.00,  // Shared Redis instance (Upstash)
			"redis-dedicated": 15.00, // Dedicated Redis instance
		},
		Storage: 0.15, // Per GB per month
	}
}

