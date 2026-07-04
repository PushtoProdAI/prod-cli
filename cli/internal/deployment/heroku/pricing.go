package heroku

import (
	"strings"

	"github.com/go-errors/errors"
	"golang.org/x/net/html"

	"github.com/pushtoprodai/prod-cli/internal/cache"
)

type PricingProvider struct{}

func NewPricingProvider() *PricingProvider {
	return &PricingProvider{}
}

func (h *PricingProvider) FetchContent() (string, error) {
	content, err := cache.FetchURLAsMarkdownWithFilter("https://www.heroku.com/pricing", true, shouldKeepPricingElement)
	if err != nil {
		return "", errors.Errorf("failed to fetch Heroku pricing page: %w", err)
	}
	return content, nil
}

func (h *PricingProvider) GetFallbackContent() string {
	return ""
}

func shouldKeepPricingElement(n *html.Node) bool {
	return containsPricingContent(n)
}

func containsPricingContent(n *html.Node) bool {
	if n.Type == html.ElementNode {
		if n.Data == "table" || n.Data == "tbody" || n.Data == "thead" || n.Data == "tr" || n.Data == "td" || n.Data == "th" {
			return true
		}

		for _, attr := range n.Attr {
			lowerVal := strings.ToLower(attr.Val)
			if strings.Contains(lowerVal, "pricing") ||
				strings.Contains(lowerVal, "table") ||
				strings.Contains(lowerVal, "price") ||
				strings.Contains(lowerVal, "dyno") ||
				strings.Contains(lowerVal, "postgres") ||
				strings.Contains(lowerVal, "redis") ||
				strings.Contains(lowerVal, "kafka") {
				return true
			}
		}
	}

	if n.Type == html.TextNode {
		lowerText := strings.ToLower(n.Data)
		if strings.Contains(lowerText, "$") ||
			strings.Contains(lowerText, "/month") ||
			strings.Contains(lowerText, "pricing") ||
			strings.Contains(lowerText, "dyno type") ||
			strings.Contains(lowerText, "plan name") {
			return true
		}
	}

	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if containsPricingContent(child) {
			return true
		}
	}

	return false
}
