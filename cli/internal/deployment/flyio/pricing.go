package flyio

import (
	"fmt"

	"github.com/go-errors/errors"

	"github.com/pushtoprodai/prod-cli/internal/cache"
)

// PricingProvider fetches Fly.io pricing content
type PricingProvider struct{}

// NewPricingProvider creates a new Fly.io pricing provider
func NewPricingProvider() *PricingProvider {
	return &PricingProvider{}
}

// FetchContent fetches and combines content from both Fly.io pricing pages
func (f *PricingProvider) FetchContent() (string, error) {
	// Fetch main pricing page
	pricingContent, err := cache.FetchURLAsMarkdownWithOptions("https://fly.io/docs/pricing/", true)
	if err != nil {
		return "", errors.Errorf("failed to fetch main pricing page: %w", err)
	}

	// Fetch MPG (Machines Per GB) pricing page
	mpgContent, err := cache.FetchURLAsMarkdownWithOptions("https://fly.io/docs/mpg/", true)
	if err != nil {
		return "", errors.Errorf("failed to fetch MPG pricing page: %w", err)
	}

	// Combine both contents with clear separation
	combinedContent := fmt.Sprintf("# Fly.io Main Pricing\n\n%s\n\n# Fly.io MPG (Machines Per GB) Pricing\n\n%s", pricingContent, mpgContent)
	return combinedContent, nil
}

// GetFallbackContent returns static fallback content
func (f *PricingProvider) GetFallbackContent() string {
	// Could provide static fallback content if needed
	return ""
}
