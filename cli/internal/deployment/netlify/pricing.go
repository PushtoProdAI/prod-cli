package netlify

import (
	"github.com/go-errors/errors"

	"github.com/pushtoprodai/prod-cli/internal/cache"
)

// PricingProvider fetches Netlify pricing content
type PricingProvider struct{}

// NewPricingProvider creates a new Netlify pricing provider
func NewPricingProvider() *PricingProvider {
	return &PricingProvider{}
}

// FetchContent fetches Netlify pricing content
func (n *PricingProvider) FetchContent() (string, error) {
	content, err := cache.FetchURLAsMarkdownWithOptions("https://www.netlify.com/pricing/", true)
	if err != nil {
		return "", errors.Errorf("failed to fetch Netlify pricing page: %w", err)
	}
	return content, nil
}

// GetFallbackContent returns static fallback content for Netlify
func (n *PricingProvider) GetFallbackContent() string {
	return `
# Netlify Pricing

## Credit-Based Pricing (New System - September 2025)

### Plans:
- **Free**: $0/month (300 credits/month included)
- **Personal**: $9/month (1,000 credits/month included)  
- **Pro**: $20 per seat/month (5,000 credits/month included)
- **Enterprise**: Custom pricing

### Credit Usage:
- Production deploys: 15 credits each
- Compute: 5 credits per GB-hour
- Form submissions: 1 credit each
- Bandwidth: 10 credits per GB
- Web requests: 3 credits per 10k requests

### Legacy Plans (still available for existing customers):
- **Static Sites**: Free ($0), Pro ($19), Business ($99), Enterprise (custom)
- **Functions**: Free tier (125K invocations), Pro ($19 includes 500K invocations), Business ($99 includes 2M invocations)
- **Forms**: Free tier (100 submissions), Pro ($19 includes 1K submissions), Business ($99 includes 10K submissions)
- **Background Functions**: Pro ($19 includes 500K invocations), Business ($99 includes 2M invocations)
- **Large Media**: Pro ($19 includes 1TB), Business ($99 includes 5TB)

For static sites on the Free plan, the cost is $0.
`
}
