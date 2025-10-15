package flyio

import (
	"context"
	"testing"

	"github.com/meroxa/prod/cli/internal/deployment"
	"github.com/meroxa/prod/cli/internal/deployment/pricing"
)

func TestFlyioDeploymentAdapter_EstimateCost(t *testing.T) {
	// Create a mock client
	mockClient := &MockFlyioClient{}

	// Create a mock pricing service to avoid LLM calls
	mockPricingService := pricing.NewMockServiceWithCostFunc(func(service deployment.CostService) float64 {
		return getFlyioFallbackServiceCost(service.Provider, service.Plan, service.Storage)
	})

	// Create the adapter with mock pricing service
	adapter := NewFlyioDeploymentAdapterWithPricing(mockClient, nil, mockPricingService)

	// Create a test deployment spec
	spec := &deployment.DeploymentSpec{
		Name:     "test-app",
		Language: "python",
		Services: []deployment.Service{
			{
				Name:     "db",
				Provider: "postgresql",
				Type:     "database",
			},
			{
				Name:     "cache",
				Provider: "redis",
				Type:     "cache",
			},
		},
	}

	// Estimate costs
	ctx := context.Background()
	costEstimate, err := adapter.EstimateCost(ctx, spec, deployment.StrategyFlyio)
	if err != nil {
		t.Fatalf("Failed to estimate costs: %v", err)
	}

	// Verify the cost estimate
	if costEstimate.Total <= 0 {
		t.Errorf("Expected total cost to be greater than 0, got: $%.2f", costEstimate.Total)
	}

	// Should have 3 services: web, postgresql, redis
	if len(costEstimate.Services) != 3 {
		t.Errorf("Expected 3 services, got %d", len(costEstimate.Services))
	}

	// Check that each service has a cost
	for i, service := range costEstimate.Services {
		if service.Cost <= 0 {
			t.Errorf("Service %d (%s) has no cost: $%.2f", i, service.Service.Name, service.Cost)
		}
	}

	t.Logf("Cost estimate: $%.2f/month", costEstimate.Total)
	for _, service := range costEstimate.Services {
		t.Logf("  - %s (%s): $%.2f", service.Service.Name, service.Plan, service.Cost)
	}
}

func TestGetFlyioFallbackServiceCost(t *testing.T) {
	tests := []struct {
		provider string
		plan     string
		storage  int
		expected float64
	}{
		{"web", "shared-cpu-1x", 0, 5.70},
		{"web", "shared-cpu-2x", 0, 11.40},
		{"postgresql", "basic", 10, 39.50}, // 38.00 + (10 * 0.15)
		{"redis", "redis-shared", 0, 5.00},
		{"unknown", "unknown", 0, 0.0},
	}

	for _, test := range tests {
		cost := getFlyioFallbackServiceCost(test.provider, test.plan, test.storage)
		if cost != test.expected {
			t.Errorf("For %s/%s/%dGB, expected $%.2f, got $%.2f",
				test.provider, test.plan, test.storage, test.expected, cost)
		}
	}
}

// MockFlyioClient implements FlyioClient for testing
type MockFlyioClient struct{}

func (m *MockFlyioClient) CreateApp(ctx context.Context, req CreateAppRequest) (*FlyioApp, error) {
	return &FlyioApp{ID: "test-app-id", Name: req.Name}, nil
}

func (m *MockFlyioClient) GetApp(ctx context.Context, appID string) (*FlyioApp, error) {
	return &FlyioApp{ID: appID, Name: appID}, nil
}

func (m *MockFlyioClient) DeployApp(ctx context.Context, appID string, config *FlyioConfig) error {
	return nil
}

func (m *MockFlyioClient) DestroyApp(ctx context.Context, appID string) error {
	return nil
}

func (m *MockFlyioClient) CreatePostgres(ctx context.Context, req CreatePostgresRequest) (*FlyioPostgresCluster, error) {
	return &FlyioPostgresCluster{ID: "test-db-id", Name: req.Name}, nil
}

func (m *MockFlyioClient) CreateRedis(ctx context.Context, req CreateRedisRequest) (*FlyioRedis, error) {
	return &FlyioRedis{ID: "test-redis-id", Name: req.Name}, nil
}

func (m *MockFlyioClient) GetPostgresConnectionInfo(ctx context.Context, appID string) (*PostgresConnectionInfo, error) {
	return &PostgresConnectionInfo{}, nil
}

func (m *MockFlyioClient) GetRedisConnectionInfo(ctx context.Context, appID string) (*RedisConnectionInfo, error) {
	return &RedisConnectionInfo{}, nil
}

func (m *MockFlyioClient) AttachPostgres(ctx context.Context, req AttachPostgresRequest) error {
	return nil
}

func (m *MockFlyioClient) AttachRedis(ctx context.Context, req AttachRedisRequest) error {
	return nil
}

func (m *MockFlyioClient) CreateVolume(ctx context.Context, req CreateVolumeRequest) (*FlyioVolume, error) {
	return &FlyioVolume{ID: "test-volume-id", Name: req.Name}, nil
}

func (m *MockFlyioClient) GetAppLogs(ctx context.Context, appID string) ([]LogEntry, error) {
	return []LogEntry{}, nil
}

func (m *MockFlyioClient) GetAppMetrics(ctx context.Context, appID string) (*AppMetrics, error) {
	return &AppMetrics{}, nil
}

func (m *MockFlyioClient) ListPostgres(ctx context.Context) ([]FlyioPostgresCluster, error) {
	return []FlyioPostgresCluster{}, nil
}
