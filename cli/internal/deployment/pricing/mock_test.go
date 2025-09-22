package pricing

import (
	"context"
	"testing"

	"github.com/meroxa/prod/cli/internal/deployment"
)

func TestMockService_EstimateCost(t *testing.T) {
	mock := NewMockService()

	tests := []struct {
		name     string
		service  deployment.CostService
		expected float64
	}{
		{
			name: "PostgreSQL service",
			service: deployment.CostService{
				Service: deployment.Service{Provider: "postgresql"},
				Storage: 10,
			},
			expected: 39.5, // 38.0 + (10 * 0.15)
		},
		{
			name: "Redis service",
			service: deployment.CostService{
				Service: deployment.Service{Provider: "redis"},
			},
			expected: 5.0,
		},
		{
			name: "Web service",
			service: deployment.CostService{
				Service: deployment.Service{Provider: "web"},
			},
			expected: 20.0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := mock.EstimateCost(context.Background(), test.service)
			if err != nil {
				t.Fatalf("EstimateCost failed: %v", err)
			}

			if result.Cost != test.expected {
				t.Errorf("Expected cost %.2f, got %.2f", test.expected, result.Cost)
			}

			if result.UsageCosts != nil {
				t.Errorf("Expected no usage costs, got %v", result.UsageCosts)
			}
		})
	}
}

func TestMockService_EstimateCosts(t *testing.T) {
	mock := NewMockService()

	services := []deployment.CostService{
		{Service: deployment.Service{Provider: "web"}},
		{Service: deployment.Service{Provider: "redis"}},
	}

	costs, err := mock.EstimateCosts(context.Background(), services)
	if err != nil {
		t.Fatalf("EstimateCosts failed: %v", err)
	}

	expected := []float64{20.0, 5.0}
	if len(costs) != len(expected) {
		t.Fatalf("Expected %d costs, got %d", len(expected), len(costs))
	}

	for i, expectedCost := range expected {
		if costs[i] != expectedCost {
			t.Errorf("Service %d: expected cost %.2f, got %.2f", i, expectedCost, costs[i])
		}
	}
}

func TestMockServiceWithCustomCostFunc(t *testing.T) {
	// Custom cost function that always returns 100.0
	customCostFunc := func(service deployment.CostService) float64 {
		return 100.0
	}

	mock := NewMockServiceWithCostFunc(customCostFunc)

	service := deployment.CostService{
		Service: deployment.Service{Provider: "postgresql"},
		Storage: 10,
	}

	result, err := mock.EstimateCost(context.Background(), service)
	if err != nil {
		t.Fatalf("EstimateCost failed: %v", err)
	}

	if result.Cost != 100.0 {
		t.Errorf("Expected cost 100.0, got %.2f", result.Cost)
	}
}
