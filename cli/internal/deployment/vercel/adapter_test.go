package vercel

import (
	"context"
	"testing"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
	"github.com/pushtoprodai/prod-cli/internal/deployment/pricing"
)

func TestVercelDeploymentAdapter_EstimateCost(t *testing.T) {
	// Create a mock pricing service to avoid BAML calls
	mockPricingService := pricing.NewMockServiceWithCostFunc(func(service deployment.CostService) float64 {
		// Return fallback costs for Vercel services
		switch service.Plan {
		case "hobby":
			return 0.0 // Hobby plan is free
		case "pro":
			return 20.0 // Pro plan is $20/month
		default:
			return 0.0
		}
	})

	// Create the adapter with mock pricing service
	adapter := NewVercelDeploymentAdapterWithPricing(NewCLIVercelClient(), nil, mockPricingService)

	spec := &deployment.DeploymentSpec{
		Name:     "test-app",
		Language: "javascript",
		Services: []deployment.Service{
			{
				Type:     "web",
				Name:     "frontend",
				Provider: "vercel",
			},
		},
	}

	ctx := context.Background()
	estimate, err := adapter.EstimateCost(ctx, spec, deployment.StrategyVercel)
	if err != nil {
		t.Fatalf("EstimateCost failed: %v", err)
	}

	// Should have at least one service (the web-app service is automatically added)
	if len(estimate.Services) == 0 {
		t.Error("Expected at least one service in cost estimate")
	}

	// Check that we have the web-app service
	found := false
	for _, service := range estimate.Services {
		if service.Service.Name == "web-app" && service.Service.Provider == "vercel" {
			found = true
			if service.Plan != "hobby" {
				t.Errorf("Expected hobby plan, got %s", service.Plan)
			}
			break
		}
	}

	if !found {
		t.Error("Expected web-app service with vercel provider not found")
	}

	t.Logf("Cost estimate: %+v", estimate)
}

func TestVercelPricingContent(t *testing.T) {
	// Test that pricing provider can fetch fallback content
	provider := NewPricingProvider()
	content := provider.GetFallbackContent()

	// Since our Vercel provider doesn't have meaningful fallback content,
	// this test mainly verifies that the interface works without panicking
	t.Logf("Pricing fallback content length: %d characters", len(content))

	// The interface works - that's what matters for this test
}

func TestVercelFallbackPricing(t *testing.T) {
	cr := deployment.CostRequest{
		Services: []deployment.CostService{
			{
				Service: deployment.Service{
					Name:     "web-app",
					Provider: "vercel",
				},
				Plan: "hobby",
			},
			{
				Service: deployment.Service{
					Name:     "api",
					Provider: "vercel",
				},
				Plan: "pro",
			},
		},
	}

	estimate, err := estimateVercelCostFallback(cr)
	if err != nil {
		t.Fatalf("Fallback pricing failed: %v", err)
	}

	if len(estimate.Services) != 2 {
		t.Errorf("Expected 2 services, got %d", len(estimate.Services))
	}

	// Check hobby plan cost (should be free)
	hobbyService := estimate.Services[0]
	if hobbyService.Cost != 0.0 {
		t.Errorf("Expected hobby plan to cost $0, got $%.2f", hobbyService.Cost)
	}

	// Check pro plan cost
	proService := estimate.Services[1]
	if proService.Cost != 20.0 {
		t.Errorf("Expected pro plan to cost $20, got $%.2f", proService.Cost)
	}

	expectedTotal := 0.0 + 20.0
	if estimate.Total != expectedTotal {
		t.Errorf("Expected total cost $%.2f, got $%.2f", expectedTotal, estimate.Total)
	}

	t.Logf("Fallback cost estimate: %+v", estimate)
}
