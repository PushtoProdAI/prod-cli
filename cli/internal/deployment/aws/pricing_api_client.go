package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/pricing"
	"github.com/aws/aws-sdk-go-v2/service/pricing/types"
	"github.com/go-errors/errors"
)

// PricingAPIClient handles AWS Pricing API calls with caching
type PricingAPIClient struct {
	client *pricing.Client
	cache  *PricingCache
}

// PricingCache stores pricing data with TTL
type PricingCache struct {
	data map[string]*CachedPrice
	mu   sync.RWMutex
	ttl  time.Duration
}

// CachedPrice represents a cached pricing entry
type CachedPrice struct {
	Price     float64
	FetchedAt time.Time
}

// NewPricingAPIClient creates a new AWS Pricing API client
// Note: Pricing API is publicly accessible and doesn't require authentication
// However, we still use AWS SDK config to handle regional endpoints
func NewPricingAPIClient(ctx context.Context) (*PricingAPIClient, error) {
	// AWS Pricing API is only available in us-east-1
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion("us-east-1"))
	if err != nil {
		// If we can't load AWS config, create anonymous config
		// This allows pricing API to work without AWS credentials
		cfg = aws.Config{
			Region: "us-east-1",
		}
	}

	client := pricing.NewFromConfig(cfg)

	return &PricingAPIClient{
		client: client,
		cache: &PricingCache{
			data: make(map[string]*CachedPrice),
			ttl:  24 * time.Hour, // Pricing changes infrequently
		},
	}, nil
}

// Get retrieves a cached price if available and not expired
func (c *PricingCache) Get(key string) (float64, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	cached, exists := c.data[key]
	if !exists || time.Since(cached.FetchedAt) > c.ttl {
		return 0, false
	}
	return cached.Price, true
}

// Set stores a price in the cache
func (c *PricingCache) Set(key string, price float64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.data[key] = &CachedPrice{
		Price:     price,
		FetchedAt: time.Now(),
	}
}

// AppRunnerPricingResult contains App Runner pricing details
// Note: App Runner has two pricing modes:
// - Provisioned (idle): Only memory charged at $0.007/GB-hour
// - Active (processing): vCPU at $0.064/vCPU-hour + memory at $0.007/GB-hour
// This estimate assumes ~12 hours active per day (moderate production usage)
type AppRunnerPricingResult struct {
	ComputeHourlyCost float64 // Cost per vCPU-hour when active
	MemoryHourlyCost  float64 // Cost per GB-hour (always charged)
	MonthlyCost       float64 // Estimated monthly cost (assumes 12hrs active/day)
}

// RDSPricingResult contains RDS pricing details
type RDSPricingResult struct {
	ComputeHourlyCost    float64 // Cost per hour for instance
	StorageGBMonthlyCost float64 // Cost per GB per month
}

// ElastiCachePricingResult contains ElastiCache pricing details
type ElastiCachePricingResult struct {
	NodeHourlyCost float64 // Cost per hour per node
	MonthlyCost    float64 // Total monthly cost (730 hours)
}

// ServerlessElastiCachePricingResult contains Serverless ElastiCache pricing details
type ServerlessElastiCachePricingResult struct {
	DataStorageGBMonthlyCost float64 // Cost per GB per month for data storage
	ECPUHourlyCost           float64 // Cost per ECPU per hour
}

// GetAppRunnerPricing retrieves App Runner pricing for specified CPU and memory
func (c *PricingAPIClient) GetAppRunnerPricing(ctx context.Context, vCPU, memoryGB, region string) (*AppRunnerPricingResult, error) {
	cacheKey := fmt.Sprintf("apprunner:%s:%s:%s", region, vCPU, memoryGB)

	// Check cache first
	if cached, ok := c.cache.Get(cacheKey); ok {
		slog.Debug("Using cached App Runner pricing", "key", cacheKey, "price", cached)
		return &AppRunnerPricingResult{
			MonthlyCost: cached,
		}, nil
	}

	// AWS Pricing API filters for App Runner
	// Note: App Runner pricing is per vCPU-hour and per GB-hour
	filters := []types.Filter{
		{
			Type:  types.FilterTypeTermMatch,
			Field: aws.String("ServiceCode"),
			Value: aws.String("AWSAppRunner"),
		},
		{
			Type:  types.FilterTypeTermMatch,
			Field: aws.String("regionCode"),
			Value: aws.String(region),
		},
	}

	result, err := c.client.GetProducts(ctx, &pricing.GetProductsInput{
		ServiceCode: aws.String("AWSAppRunner"),
		Filters:     filters,
		MaxResults:  aws.Int32(10),
	})
	if err != nil {
		return nil, errors.Errorf("failed to get App Runner pricing: %w", err)
	}

	// Parse pricing data from results
	var vCPUCostPerHour, memoryCostPerHour float64

	for _, priceItem := range result.PriceList {
		var product map[string]any
		if err := json.Unmarshal([]byte(priceItem), &product); err != nil {
			continue
		}

		// Extract pricing dimensions
		terms, ok := product["terms"].(map[string]any)
		if !ok {
			continue
		}

		onDemand, ok := terms["OnDemand"].(map[string]any)
		if !ok {
			continue
		}

		// Navigate through the nested structure to find the price
		for _, termData := range onDemand {
			termMap, ok := termData.(map[string]any)
			if !ok {
				continue
			}

			priceDimensions, ok := termMap["priceDimensions"].(map[string]any)
			if !ok {
				continue
			}

			for _, dimension := range priceDimensions {
				dimMap, ok := dimension.(map[string]any)
				if !ok {
					continue
				}

				pricePerUnit, ok := dimMap["pricePerUnit"].(map[string]any)
				if !ok {
					continue
				}

				usdPrice, ok := pricePerUnit["USD"].(string)
				if !ok {
					continue
				}

				price, err := strconv.ParseFloat(usdPrice, 64)
				if err != nil {
					continue
				}

				// Determine if this is vCPU or memory pricing
				description, _ := dimMap["description"].(string)
				unit, _ := dimMap["unit"].(string)

				if unit == "vCPU-Hours" {
					vCPUCostPerHour = price
				} else if unit == "GB-Hours" {
					memoryCostPerHour = price
				}

				slog.Debug("Found App Runner pricing", "description", description, "unit", unit, "price", price)
			}
		}
	}

	// Calculate estimated monthly cost
	// Assumes moderate production usage: ~12 hours active per day
	// Active hours: (vCPU + memory) charged
	// Provisioned hours: only memory charged
	vCPUCount, _ := strconv.ParseFloat(vCPU, 64)
	memoryCount, _ := strconv.ParseFloat(memoryGB, 64)

	activeHoursPerDay := 12.0
	activeHoursPerMonth := activeHoursPerDay * 30
	provisionedHoursPerMonth := 24.0 * 30 // Always provisioned

	// Active compute cost (only during active hours)
	activeComputeCost := vCPUCount * vCPUCostPerHour * activeHoursPerMonth
	// Memory cost (24/7 provisioned)
	memoryCost := memoryCount * memoryCostPerHour * provisionedHoursPerMonth

	monthlyCost := activeComputeCost + memoryCost

	slog.Debug("App Runner pricing calculation",
		"vCPU", vCPUCount,
		"memory_gb", memoryCount,
		"vcpu_hourly", vCPUCostPerHour,
		"memory_hourly", memoryCostPerHour,
		"active_compute_monthly", activeComputeCost,
		"memory_monthly", memoryCost,
		"total_monthly", monthlyCost,
	)

	// Cache the result
	c.cache.Set(cacheKey, monthlyCost)

	return &AppRunnerPricingResult{
		ComputeHourlyCost: vCPUCostPerHour,
		MemoryHourlyCost:  memoryCostPerHour,
		MonthlyCost:       monthlyCost,
	}, nil
}

// GetRDSPricing retrieves RDS pricing for specified instance class and region
func (c *PricingAPIClient) GetRDSPricing(ctx context.Context, instanceClass, engine, region string) (*RDSPricingResult, error) {
	cacheKey := fmt.Sprintf("rds:%s:%s:%s", region, instanceClass, engine)

	// Check cache first
	if cached, ok := c.cache.Get(cacheKey); ok {
		slog.Debug("Using cached RDS pricing", "key", cacheKey, "price", cached)
		// For cached RDS, we return monthly compute cost
		// Storage is priced separately
		return &RDSPricingResult{
			ComputeHourlyCost: cached / 730,
		}, nil
	}

	// Get RDS instance pricing
	filters := []types.Filter{
		{
			Type:  types.FilterTypeTermMatch,
			Field: aws.String("ServiceCode"),
			Value: aws.String("AmazonRDS"),
		},
		{
			Type:  types.FilterTypeTermMatch,
			Field: aws.String("regionCode"),
			Value: aws.String(region),
		},
		{
			Type:  types.FilterTypeTermMatch,
			Field: aws.String("instanceType"),
			Value: aws.String(instanceClass),
		},
		{
			Type:  types.FilterTypeTermMatch,
			Field: aws.String("databaseEngine"),
			Value: aws.String(engine),
		},
		{
			Type:  types.FilterTypeTermMatch,
			Field: aws.String("deploymentOption"),
			Value: aws.String("Single-AZ"),
		},
	}

	result, err := c.client.GetProducts(ctx, &pricing.GetProductsInput{
		ServiceCode: aws.String("AmazonRDS"),
		Filters:     filters,
		MaxResults:  aws.Int32(10),
	})
	if err != nil {
		return nil, errors.Errorf("failed to get RDS pricing: %w", err)
	}

	var computeHourlyCost float64

	// Parse the first matching price
	if len(result.PriceList) > 0 {
		computeHourlyCost = c.extractOnDemandPrice(result.PriceList[0])
	}

	// Get storage pricing separately
	storageFilters := []types.Filter{
		{
			Type:  types.FilterTypeTermMatch,
			Field: aws.String("ServiceCode"),
			Value: aws.String("AmazonRDS"),
		},
		{
			Type:  types.FilterTypeTermMatch,
			Field: aws.String("regionCode"),
			Value: aws.String(region),
		},
		{
			Type:  types.FilterTypeTermMatch,
			Field: aws.String("volumeType"),
			Value: aws.String("General Purpose-GP3"),
		},
	}

	storageResult, err := c.client.GetProducts(ctx, &pricing.GetProductsInput{
		ServiceCode: aws.String("AmazonRDS"),
		Filters:     storageFilters,
		MaxResults:  aws.Int32(5),
	})

	var storageGBMonthlyCost float64
	if err == nil && len(storageResult.PriceList) > 0 {
		storageGBMonthlyCost = c.extractOnDemandPrice(storageResult.PriceList[0])
	}

	// Cache compute cost
	monthlyCost := computeHourlyCost * 730
	c.cache.Set(cacheKey, monthlyCost)

	return &RDSPricingResult{
		ComputeHourlyCost:    computeHourlyCost,
		StorageGBMonthlyCost: storageGBMonthlyCost,
	}, nil
}

// GetElastiCachePricing retrieves ElastiCache pricing for specified node type and region
func (c *PricingAPIClient) GetElastiCachePricing(ctx context.Context, nodeType, region string) (*ElastiCachePricingResult, error) {
	cacheKey := fmt.Sprintf("elasticache:%s:%s", region, nodeType)

	// Check cache first
	if cached, ok := c.cache.Get(cacheKey); ok {
		slog.Debug("Using cached ElastiCache pricing", "key", cacheKey, "price", cached)
		return &ElastiCachePricingResult{
			MonthlyCost: cached,
		}, nil
	}

	filters := []types.Filter{
		{
			Type:  types.FilterTypeTermMatch,
			Field: aws.String("ServiceCode"),
			Value: aws.String("AmazonElastiCache"),
		},
		{
			Type:  types.FilterTypeTermMatch,
			Field: aws.String("regionCode"),
			Value: aws.String(region),
		},
		{
			Type:  types.FilterTypeTermMatch,
			Field: aws.String("instanceType"),
			Value: aws.String(nodeType),
		},
		{
			Type:  types.FilterTypeTermMatch,
			Field: aws.String("cacheEngine"),
			Value: aws.String("Redis"),
		},
	}

	result, err := c.client.GetProducts(ctx, &pricing.GetProductsInput{
		ServiceCode: aws.String("AmazonElastiCache"),
		Filters:     filters,
		MaxResults:  aws.Int32(10),
	})
	if err != nil {
		return nil, errors.Errorf("failed to get ElastiCache pricing: %w", err)
	}

	var nodeHourlyCost float64

	if len(result.PriceList) > 0 {
		nodeHourlyCost = c.extractOnDemandPrice(result.PriceList[0])
	}

	monthlyCost := nodeHourlyCost * 730

	// Cache the result
	c.cache.Set(cacheKey, monthlyCost)

	return &ElastiCachePricingResult{
		NodeHourlyCost: nodeHourlyCost,
		MonthlyCost:    monthlyCost,
	}, nil
}

// GetNATGatewayPricing retrieves NAT Gateway pricing for a region
func (c *PricingAPIClient) GetNATGatewayPricing(ctx context.Context, region string) (float64, error) {
	cacheKey := fmt.Sprintf("natgateway:%s", region)

	if cached, ok := c.cache.Get(cacheKey); ok {
		return cached, nil
	}

	// NAT Gateway pricing is straightforward - hourly charge
	filters := []types.Filter{
		{
			Type:  types.FilterTypeTermMatch,
			Field: aws.String("ServiceCode"),
			Value: aws.String("AmazonEC2"),
		},
		{
			Type:  types.FilterTypeTermMatch,
			Field: aws.String("regionCode"),
			Value: aws.String(region),
		},
		{
			Type:  types.FilterTypeTermMatch,
			Field: aws.String("productFamily"),
			Value: aws.String("NAT Gateway"),
		},
	}

	result, err := c.client.GetProducts(ctx, &pricing.GetProductsInput{
		ServiceCode: aws.String("AmazonEC2"),
		Filters:     filters,
		MaxResults:  aws.Int32(5),
	})
	if err != nil {
		return 0, errors.Errorf("failed to get NAT Gateway pricing: %w", err)
	}

	var hourlyCost float64
	if len(result.PriceList) > 0 {
		hourlyCost = c.extractOnDemandPrice(result.PriceList[0])
	}

	monthlyCost := hourlyCost * 730

	c.cache.Set(cacheKey, monthlyCost)
	return monthlyCost, nil
}

// GetServerlessElastiCachePricing retrieves Serverless ElastiCache pricing
func (c *PricingAPIClient) GetServerlessElastiCachePricing(ctx context.Context, region string) (*ServerlessElastiCachePricingResult, error) {
	cacheKey := fmt.Sprintf("serverless-elasticache:%s", region)

	// Try to get pricing from AWS Pricing API
	// Note: Serverless ElastiCache pricing may not be fully available in the API yet
	// ServiceCode would be "AmazonElastiCache" with specific filters for serverless

	// For now, use fallback pricing based on published AWS pricing
	// As of Jan 2025:
	// - Data storage: $0.125/GB-month
	// - ECPU: $0.0034/ECPU-hour

	result := &ServerlessElastiCachePricingResult{
		DataStorageGBMonthlyCost: 0.125,
		ECPUHourlyCost:           0.0034,
	}

	// Check cache first
	if cached, ok := c.cache.Get(cacheKey); ok {
		slog.Debug("Using cached Serverless ElastiCache pricing", "key", cacheKey, "cost", cached)
		return result, nil
	}

	// Cache the storage cost as a representative value
	c.cache.Set(cacheKey, result.DataStorageGBMonthlyCost)

	return result, nil
}

// GetSecretsManagerPricing retrieves Secrets Manager pricing
func (c *PricingAPIClient) GetSecretsManagerPricing(ctx context.Context, region string) (float64, error) {
	// Secrets Manager is $0.40 per secret per month (fairly consistent across regions)
	// Plus API call costs, but we'll ignore those for estimation
	return 0.40, nil
}

// extractOnDemandPrice is a helper to extract the USD price from a pricing API result
func (c *PricingAPIClient) extractOnDemandPrice(priceItem string) float64 {
	var product map[string]any
	if err := json.Unmarshal([]byte(priceItem), &product); err != nil {
		return 0
	}

	terms, ok := product["terms"].(map[string]any)
	if !ok {
		return 0
	}

	onDemand, ok := terms["OnDemand"].(map[string]any)
	if !ok {
		return 0
	}

	for _, termData := range onDemand {
		termMap, ok := termData.(map[string]any)
		if !ok {
			continue
		}

		priceDimensions, ok := termMap["priceDimensions"].(map[string]any)
		if !ok {
			continue
		}

		for _, dimension := range priceDimensions {
			dimMap, ok := dimension.(map[string]any)
			if !ok {
				continue
			}

			pricePerUnit, ok := dimMap["pricePerUnit"].(map[string]any)
			if !ok {
				continue
			}

			usdPrice, ok := pricePerUnit["USD"].(string)
			if !ok {
				continue
			}

			price, err := strconv.ParseFloat(usdPrice, 64)
			if err != nil {
				continue
			}

			return price
		}
	}

	return 0
}
