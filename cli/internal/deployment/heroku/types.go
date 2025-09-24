package heroku

import (
	"time"
)

// HerokuPricing represents pricing information for Heroku services
type HerokuPricing struct {
	Dynos       map[string]float64 `json:"dynos"`
	Databases   map[string]float64 `json:"databases"`
	Redis       map[string]float64 `json:"redis"`
	LastFetched time.Time          `json:"last_fetched"`
}

// Default fallback pricing for Heroku services (monthly costs in USD)
var FallbackPricing = HerokuPricing{
	Dynos: map[string]float64{
		"eco":           5.0,   // Eco dyno
		"basic":         7.0,   // Basic dyno
		"standard-1x":   25.0,  // Standard-1X dyno
		"standard-2x":   50.0,  // Standard-2X dyno
		"performance-m": 250.0, // Performance-M dyno
		"performance-l": 500.0, // Performance-L dyno
	},
	Databases: map[string]float64{
		"essential-0": 5.0,
		"essential-1": 12.0,
		"essential-2": 18.0,
		"standard-0":  50.0,
		"standard-2":  200.0,
		"standard-4":  400.0,
		"standard-6":  600.0,
		"premium-0":   200.0,
		"premium-2":   350.0,
		"premium-4":   600.0,
	},
	Redis: map[string]float64{
		"mini":       3.0,
		"premium-0":  15.0,
		"premium-1":  30.0,
		"premium-2":  60.0,
		"premium-3":  120.0,
		"premium-5":  200.0,
		"premium-7":  750.0,
		"premium-9":  1450.0,
		"premium-10": 3500.0,
		"premium-12": 6500.0,
		"premium-14": 12500.0,
	},
	LastFetched: time.Date(2025, 1, 30, 0, 0, 0, 0, time.UTC),
}
