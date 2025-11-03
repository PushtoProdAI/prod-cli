package aws

import (
	"github.com/go-errors/errors"

	"github.com/meroxa/prod/cli/internal/cache"
)

// PricingProvider fetches AWS pricing content
type PricingProvider struct{}

// NewPricingProvider creates a new AWS pricing provider
func NewPricingProvider() *PricingProvider {
	return &PricingProvider{}
}

// FetchContent fetches AWS pricing content
// For now, we'll use the App Runner pricing page as the primary source
func (p *PricingProvider) FetchContent() (string, error) {
	// TODO: Fetch pricing from multiple AWS pricing pages
	// For the initial implementation, we'll use App Runner as the primary service
	content, err := cache.FetchURLAsMarkdownWithOptions("https://aws.amazon.com/apprunner/pricing/", true)
	if err != nil {
		return "", errors.Errorf("failed to fetch AWS App Runner pricing page: %w", err)
	}
	return content, nil
}

// GetFallbackContent returns static fallback content for AWS
// This provides hardcoded pricing information when live fetching fails
func (p *PricingProvider) GetFallbackContent() string {
	return `
# AWS Pricing (Fallback - as of January 2025)

## App Runner Pricing
- Compute: $0.007 per vCPU-hour
- Memory: $0.003 per GB-hour
- Example: 1 vCPU + 2 GB = (1 × $0.007 + 2 × $0.003) × 730 hours = $51.84/month

## RDS PostgreSQL Pricing (US East)
- db.t3.micro: $0.017 per hour (~$12.41/month)
- db.t3.small: $0.034 per hour (~$24.82/month)
- db.t3.medium: $0.068 per hour (~$49.64/month)
- db.t4g.micro (ARM): $0.014 per hour (~$10.22/month)
- gp3 Storage: $0.115 per GB-month

## ElastiCache Redis Pricing (US East)
- cache.t3.micro: $0.016 per hour (~$11.68/month)
- cache.t3.small: $0.032 per hour (~$23.36/month)
- cache.t4g.micro (ARM): $0.013 per hour (~$9.49/month)
`
}
