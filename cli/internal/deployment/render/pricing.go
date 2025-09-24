package render

import (
	"github.com/go-errors/errors"

	"github.com/meroxa/prod/cli/internal/cache"
)

// PricingProvider fetches Render pricing content
type PricingProvider struct{}

// NewPricingProvider creates a new Render pricing provider
func NewPricingProvider() *PricingProvider {
	return &PricingProvider{}
}

// FetchContent fetches Render pricing content
func (r *PricingProvider) FetchContent() (string, error) {
	content, err := cache.FetchURLAsMarkdownWithOptions("https://render.com/pricing", true)
	if err != nil {
		return "", errors.Errorf("failed to fetch Render pricing page: %w", err)
	}
	return content, nil
}

// GetFallbackContent returns static fallback content for Render
func (r *PricingProvider) GetFallbackContent() string {
	// Could provide static fallback content if needed
	return ""
}
