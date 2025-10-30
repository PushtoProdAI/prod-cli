package render

import (
	"strings"

	"github.com/go-errors/errors"
	"golang.org/x/net/html"

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
	content, err := cache.FetchURLAsMarkdownWithFilter("https://render.com/pricing", true, shouldKeepPricingElement)
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

// shouldKeepPricingElement determines if an element should be kept even if it appears hidden
func shouldKeepPricingElement(n *html.Node) bool {
	return containsPricingContent(n)
}

// containsPricingContent checks if a node contains pricing-related tables or content
func containsPricingContent(n *html.Node) bool {
	// Check current node
	if n.Type == html.ElementNode {
		if n.Data == "table" || n.Data == "tbody" || n.Data == "thead" || n.Data == "tr" || n.Data == "td" || n.Data == "th" {
			return true
		}

		// Check for pricing-related classes or IDs
		for _, attr := range n.Attr {
			lowerVal := strings.ToLower(attr.Val)
			if strings.Contains(lowerVal, "pricing") ||
				strings.Contains(lowerVal, "table") ||
				strings.Contains(lowerVal, "price") {
				return true
			}
		}
	}

	// Check text content for pricing indicators
	if n.Type == html.TextNode {
		lowerText := strings.ToLower(n.Data)
		if strings.Contains(lowerText, "$") ||
			strings.Contains(lowerText, "/month") ||
			strings.Contains(lowerText, "pricing") {
			return true
		}
	}

	// Check children recursively
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if containsPricingContent(child) {
			return true
		}
	}

	return false
}
