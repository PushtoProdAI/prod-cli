package vercel

import (
	"fmt"

	"github.com/meroxa/prod/cli/internal/deployment"
)

// PricingProvider fetches Vercel pricing content
type PricingProvider struct{}

// NewPricingProvider creates a new Vercel pricing provider
func NewPricingProvider() *PricingProvider {
	return &PricingProvider{}
}

// FetchContent fetches Vercel pricing content
func (v *PricingProvider) FetchContent() (string, error) {
	content, err := deployment.FetchURLAsMarkdown("https://vercel.com/pricing")
	if err != nil {
		return "", fmt.Errorf("failed to fetch Vercel pricing page: %w", err)
	}
	return content, nil
}

// GetFallbackContent returns static fallback content for Vercel
func (v *PricingProvider) GetFallbackContent() string {
	// Could provide static fallback content if needed
	return ""
}
