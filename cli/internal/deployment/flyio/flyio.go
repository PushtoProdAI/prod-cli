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
		InternalPort: 8000,
	},
	"node": {
		InternalPort: 3000,
	},
	"nodejs": {
		InternalPort: 3000,
	},
	"javascript": {
		InternalPort: 3000,
	},
	"go": {
		InternalPort: 8080,
	},
	"golang": {
		InternalPort: 8080,
	},
	"ruby": {
		InternalPort: 3000,
	},
	"php": {
		InternalPort: 8000,
	},
	"rust": {
		InternalPort: 8080,
	},
	"java": {
		InternalPort: 8080,
	},
}

// LanguageConfig holds language-specific deployment configuration
type LanguageConfig struct {
	InternalPort int
}

// GetLanguageConfig returns configuration for a specific language
func GetLanguageConfig(language string) LanguageConfig {
	if config, ok := languageConfig[language]; ok {
		return config
	}
	// Return default configuration for unknown languages
	return LanguageConfig{
		InternalPort: defaultHTTPPort,
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
			// Fallback pricing - actual pricing fetched dynamically from flyctl
			"pay-as-you-go": 0.0,   // Variable pricing based on usage
			"starter":       10.0,  // $10/month
			"standard":      50.0,  // $50/month
			"pro-2k":        280.0, // $280/month
			"pro-10k":       680.0, // $680/month
		},
		Storage: 0.15, // Per GB per month
	}
}
