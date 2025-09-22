package pricing

import (
	"context"

	"github.com/meroxa/prod/cli/internal/deployment"
)

// MockService implements Service interface for testing
type MockService struct {
	// CostFunc allows customizing the cost calculation logic for different test scenarios
	CostFunc func(service deployment.CostService) float64
}

// NewMockService creates a new mock pricing service with default cost calculation
func NewMockService() *MockService {
	return &MockService{
		CostFunc: defaultMockCostFunc,
	}
}

// NewMockServiceWithCostFunc creates a mock pricing service with custom cost calculation
func NewMockServiceWithCostFunc(costFunc func(service deployment.CostService) float64) *MockService {
	return &MockService{
		CostFunc: costFunc,
	}
}

// EstimateCost returns mock pricing without making external calls
func (m *MockService) EstimateCost(ctx context.Context, service deployment.CostService) (*PricingResult, error) {
	cost := m.CostFunc(service)
	return &PricingResult{
		Cost:       cost,
		UsageCosts: nil, // No usage costs in mock by default
	}, nil
}

// EstimateCosts estimates costs for multiple services
func (m *MockService) EstimateCosts(ctx context.Context, services []deployment.CostService) ([]float64, error) {
	costs := make([]float64, len(services))
	for i, service := range services {
		costs[i] = m.CostFunc(service)
	}
	return costs, nil
}

// defaultMockCostFunc provides reasonable default costs for common service types
func defaultMockCostFunc(service deployment.CostService) float64 {
	switch service.Provider {
	case "web", "vercel":
		return 20.0 // Default web service cost
	case "postgresql":
		baseCost := 38.0                               // Default PostgreSQL cost
		storageCost := float64(service.Storage) * 0.15 // Storage cost per GB
		return baseCost + storageCost
	case "redis":
		return 5.0 // Default Redis cost
	default:
		return 10.0 // Default fallback cost
	}
}
