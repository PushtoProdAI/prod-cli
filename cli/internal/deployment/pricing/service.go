package pricing

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"strings"

	"github.com/go-errors/errors"
	"github.com/meroxa/prod/cli/baml_client/types"
	"github.com/meroxa/prod/cli/internal/deployment"
	"github.com/meroxa/prod/cli/internal/llm"
)

const (
	// DefaultRetries is the default number of retries for pricing extraction
	DefaultRetries = 3
)

// PricingProvider defines how to fetch pricing content for a specific provider
type PricingProvider interface {
	FetchContent() (string, error)
	GetFallbackContent() string
}

// Service defines the interface for pricing estimation services
type Service interface {
	EstimateCost(ctx context.Context, service deployment.CostService) (*PricingResult, error)
	EstimateCosts(ctx context.Context, services []deployment.CostService) ([]float64, error)
}

// PricingService handles common pricing extraction logic
type PricingService struct {
	provider  PricingProvider
	retries   int
	llmClient llm.Client
}

// NewPricingService creates a new pricing service with the given pricing provider and LLM client
func NewPricingService(provider PricingProvider, retries int, client llm.Client) *PricingService {
	if retries <= 0 {
		retries = 3 // default retries
	}
	return &PricingService{
		provider:  provider,
		retries:   retries,
		llmClient: client,
	}
}

// PricingResult contains the result of pricing extraction
type PricingResult struct {
	Cost       float64
	UsageCosts []UsageCost
}

// UsageCost represents a usage-based cost
type UsageCost struct {
	Unit        string
	CostPerUnit float64
}

// EstimateCosts estimates costs for multiple services using the pricing service
func (ps *PricingService) EstimateCosts(ctx context.Context, services []deployment.CostService) ([]float64, error) {
	costs := make([]float64, len(services))

	// Fetch content with retries
	content, err := ps.fetchContentWithRetries()
	if err != nil {
		return nil, errors.Errorf("failed to fetch pricing content: %w", err)
	}

	for i, service := range services {
		result, err := ps.extractPricingForService(ctx, service, content)
		if err != nil {
			return nil, errors.Errorf("failed to extract pricing for service %s: %w", service.Name, err)
		}
		costs[i] = result.Cost
	}

	return costs, nil
}

// EstimateCost estimates cost for a single service
func (ps *PricingService) EstimateCost(ctx context.Context, service deployment.CostService) (*PricingResult, error) {
	// Fetch content with retries
	content, err := ps.fetchContentWithRetries()
	if err != nil {
		return nil, errors.Errorf("failed to fetch pricing content: %w", err)
	}

	return ps.extractPricingForService(ctx, service, content)
}

// fetchContentWithRetries fetches content with retry logic
func (ps *PricingService) fetchContentWithRetries() (string, error) {
	var content string
	var err error

	for attempt := 1; attempt <= ps.retries; attempt++ {
		content, err = ps.provider.FetchContent()
		if err == nil {
			return content, nil
		}
		slog.Info("Failed to fetch pricing content", "attempt", attempt, "error", err)
	}

	// Use fallback content if all retries failed
	slog.Warn("All attempts to fetch live pricing failed, using fallback content", "error", err)
	return ps.provider.GetFallbackContent(), nil
}

// extractPricingForService extracts pricing for a single service
func (ps *PricingService) extractPricingForService(ctx context.Context, service deployment.CostService, content string) (*PricingResult, error) {
	// Convert to BAML service type
	s := types.Service{
		Name: service.Name,
		Type: service.Provider,
		Plan: service.Plan,
	}
	if service.Storage > 0 {
		s.Storage = fmt.Sprintf("%dGB", service.Storage)
	}

	slog.Info("Fetching pricing for service", "service", s)

	// Call BAML pricing function through our LLM client
	resp, err := ps.llmClient.FetchPricing(ctx, s, content)
	if err != nil {
		log.Println(err)
		return nil, errors.Errorf("BAML pricing extraction failed: %w", err)
	}

	// Handle "NOT_FOUND" gracefully
	baseCost := 0.0
	if resp.Monthly_cost < 0 {
		slog.Info("Pricing not found for service, defaulting to 0.0", "name", s.Name, "type", s.Type)
	} else {
		baseCost = resp.Monthly_cost
	}

	result := &PricingResult{
		Cost: baseCost,
	}

	// Handle usage-based pricing
	if resp.Usage_cost != nil && len(*resp.Usage_cost) > 0 {
		result.UsageCosts = make([]UsageCost, len(*resp.Usage_cost))
		for i, usageCost := range *resp.Usage_cost {
			result.UsageCosts[i] = UsageCost{
				Unit:        usageCost.Unit,
				CostPerUnit: usageCost.Cost_per_unit,
			}
		}
	}

	return result, nil
}

// ApplyUsageCosts applies usage-based costs to the base cost
func ApplyUsageCosts(baseCost float64, usageCosts []UsageCost, storageGB float64, unitFilter string) float64 {
	totalCost := baseCost

	for _, usageCost := range usageCosts {
		if usageCost.CostPerUnit > 0 && strings.EqualFold(usageCost.Unit, unitFilter) {
			usageAmount := storageGB
			additionalCost := usageCost.CostPerUnit * usageAmount

			slog.Info("Applying usage-based cost",
				"unit", usageCost.Unit,
				"cost_per_unit", usageCost.CostPerUnit,
				"usage_amount", usageAmount,
				"additional_cost", additionalCost,
			)

			totalCost += additionalCost
		}
	}

	return totalCost
}
