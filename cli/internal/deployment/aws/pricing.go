package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/go-errors/errors"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
)

// CloudFormationTemplate represents a parsed CloudFormation template
type CloudFormationTemplate struct {
	AWSTemplateFormatVersion string                 `json:"AWSTemplateFormatVersion"`
	Description              string                 `json:"Description"`
	Resources                map[string]CFResource  `json:"Resources"`
	Parameters               map[string]interface{} `json:"Parameters,omitempty"`
	Outputs                  map[string]interface{} `json:"Outputs,omitempty"`
}

// CFResource represents a CloudFormation resource
type CFResource struct {
	Type       string                 `json:"Type"`
	Properties map[string]interface{} `json:"Properties"`
}

// TemplatePricer estimates costs from a CloudFormation template using AWS Pricing API
type TemplatePricer struct {
	pricingClient *PricingAPIClient
	region        string
}

// NewTemplatePricer creates a new template-based pricing estimator
func NewTemplatePricer(ctx context.Context, region string) (*TemplatePricer, error) {
	client, err := NewPricingAPIClient(ctx)
	if err != nil {
		return nil, errors.Errorf("failed to create pricing API client: %w", err)
	}

	return &TemplatePricer{
		pricingClient: client,
		region:        region,
	}, nil
}

// EstimateCostFromTemplate parses a CloudFormation template and estimates costs
func (tp *TemplatePricer) EstimateCostFromTemplate(ctx context.Context, templateJSON string) (deployment.CostEstimate, error) {
	// Parse template
	var template CloudFormationTemplate
	if err := json.Unmarshal([]byte(templateJSON), &template); err != nil {
		return deployment.CostEstimate{}, errors.Errorf("failed to parse CloudFormation template: %w", err)
	}

	slog.Info("Estimating costs from CloudFormation template",
		"resourceCount", len(template.Resources),
		"region", tp.region,
	)

	costs := make([]deployment.CostService, 0)
	var hasNATGateway bool

	// Iterate through resources and price each one
	for resourceName, resource := range template.Resources {
		slog.Debug("Processing resource", "name", resourceName, "type", resource.Type)

		switch resource.Type {
		case "AWS::AppRunner::Service":
			cost, err := tp.priceAppRunnerService(ctx, resourceName, resource.Properties)
			if err != nil {
				slog.Warn("Failed to price App Runner service", "error", err)
				continue
			}
			costs = append(costs, cost)

		case "AWS::RDS::DBInstance":
			cost, err := tp.priceRDSInstance(ctx, resourceName, resource.Properties)
			if err != nil {
				slog.Warn("Failed to price RDS instance", "error", err)
				continue
			}
			costs = append(costs, cost)

		case "AWS::ElastiCache::CacheCluster":
			cost, err := tp.priceElastiCacheCluster(ctx, resourceName, resource.Properties)
			if err != nil {
				slog.Warn("Failed to price ElastiCache cluster", "error", err)
				continue
			}
			costs = append(costs, cost)

		case "AWS::ElastiCache::ServerlessCache":
			cost, err := tp.priceServerlessElastiCache(ctx, resourceName, resource.Properties)
			if err != nil {
				slog.Warn("Failed to price Serverless ElastiCache", "error", err)
				continue
			}
			costs = append(costs, cost)

		case "AWS::EC2::NatGateway":
			hasNATGateway = true

		case "AWS::SecretsManager::Secret":
			cost := tp.priceSecretsManagerSecret(resourceName)
			costs = append(costs, cost)
		}
	}

	// Add NAT Gateway cost if present
	if hasNATGateway {
		cost, err := tp.priceNATGateway(ctx)
		if err != nil {
			slog.Warn("Failed to price NAT Gateway", "error", err)
		} else {
			costs = append(costs, cost)
		}
	}

	// Calculate total
	total := 0.0
	for _, c := range costs {
		total += c.Cost
	}

	slog.Debug("Cost estimation complete",
		"total", total,
		"itemCount", len(costs),
	)

	return deployment.CostEstimate{
		Total:    total,
		Services: costs,
	}, nil
}

// priceAppRunnerService estimates cost for an App Runner service
func (tp *TemplatePricer) priceAppRunnerService(ctx context.Context, name string, props map[string]interface{}) (deployment.CostService, error) {
	// Extract CPU and memory from InstanceConfiguration
	instanceConfig, ok := props["InstanceConfiguration"].(map[string]interface{})
	if !ok {
		return deployment.CostService{}, errors.New("missing InstanceConfiguration")
	}

	cpu, ok := instanceConfig["Cpu"].(string)
	if !ok {
		cpu = "1 vCPU" // default
	}

	memory, ok := instanceConfig["Memory"].(string)
	if !ok {
		memory = "2 GB" // default
	}

	// Extract numeric values from strings like "1 vCPU" and "2 GB"
	vCPU := strings.Fields(cpu)[0]
	memoryGB := strings.Fields(memory)[0]

	// Get pricing from API
	pricing, err := tp.pricingClient.GetAppRunnerPricing(ctx, vCPU, memoryGB, tp.region)
	if err != nil {
		// Fall back to static pricing
		return deployment.CostService{
			Service: deployment.Service{
				Name:     name,
				Type:     "compute",
				Provider: "apprunner",
			},
			Plan: fmt.Sprintf("%s_%s", vCPU, memoryGB),
			Cost: fallbackPricing.AppRunner["1vCPU_2GB"],
		}, nil
	}

	return deployment.CostService{
		Service: deployment.Service{
			Name:     name,
			Type:     "compute",
			Provider: "apprunner",
		},
		Plan: fmt.Sprintf("%s vCPU, %s GB", vCPU, memoryGB),
		Cost: pricing.MonthlyCost,
	}, nil
}

// priceRDSInstance estimates cost for an RDS database instance
func (tp *TemplatePricer) priceRDSInstance(ctx context.Context, name string, props map[string]interface{}) (deployment.CostService, error) {
	// Extract instance class
	instanceClass, ok := props["DBInstanceClass"].(string)
	if !ok {
		instanceClass = "db.t3.micro" // default
	}

	// Extract engine
	engine, ok := props["Engine"].(string)
	if !ok {
		engine = "postgres" // default
	}

	// Extract storage
	allocatedStorage := 20 // default
	if storage, ok := props["AllocatedStorage"].(float64); ok {
		allocatedStorage = int(storage)
	} else if storage, ok := props["AllocatedStorage"].(int); ok {
		allocatedStorage = storage
	}

	// Get pricing from API
	pricing, err := tp.pricingClient.GetRDSPricing(ctx, instanceClass, engine, tp.region)
	if err != nil {
		// Fall back to static pricing
		baseCost := fallbackPricing.RDS[instanceClass]
		if baseCost == 0 {
			baseCost = fallbackPricing.RDS["db.t3.micro"]
		}
		storageCost := float64(allocatedStorage) * fallbackPricing.Storage

		return deployment.CostService{
			Service: deployment.Service{
				Name:     name,
				Type:     "database",
				Provider: "rds",
			},
			Plan:    instanceClass,
			Storage: allocatedStorage,
			Cost:    baseCost + storageCost,
		}, nil
	}

	// Calculate total cost (compute + storage)
	computeMonthlyCost := pricing.ComputeHourlyCost * 730
	storageMonthlyCost := pricing.StorageGBMonthlyCost * float64(allocatedStorage)
	totalCost := computeMonthlyCost + storageMonthlyCost

	return deployment.CostService{
		Service: deployment.Service{
			Name:     name,
			Type:     "database",
			Provider: "rds",
		},
		Plan:    instanceClass,
		Storage: allocatedStorage,
		Cost:    totalCost,
	}, nil
}

// priceElastiCacheCluster estimates cost for an ElastiCache cluster
func (tp *TemplatePricer) priceElastiCacheCluster(ctx context.Context, name string, props map[string]interface{}) (deployment.CostService, error) {
	// Extract node type
	nodeType, ok := props["CacheNodeType"].(string)
	if !ok {
		nodeType = "cache.t3.micro" // default
	}

	// Extract number of nodes
	numNodes := 1 // default
	if nodes, ok := props["NumCacheNodes"].(float64); ok {
		numNodes = int(nodes)
	} else if nodes, ok := props["NumCacheNodes"].(int); ok {
		numNodes = nodes
	}

	// Get pricing from API
	pricing, err := tp.pricingClient.GetElastiCachePricing(ctx, nodeType, tp.region)
	if err != nil {
		// Fall back to static pricing
		baseCost := fallbackPricing.Redis[nodeType]
		if baseCost == 0 {
			baseCost = fallbackPricing.Redis["cache.t3.micro"]
		}

		return deployment.CostService{
			Service: deployment.Service{
				Name:     name,
				Type:     "cache",
				Provider: "elasticache",
			},
			Plan: nodeType,
			Cost: baseCost * float64(numNodes),
		}, nil
	}

	// Calculate total cost for all nodes
	totalCost := pricing.MonthlyCost * float64(numNodes)

	return deployment.CostService{
		Service: deployment.Service{
			Name:     name,
			Type:     "cache",
			Provider: "elasticache",
		},
		Plan: nodeType,
		Cost: totalCost,
	}, nil
}

// priceServerlessElastiCache estimates cost for Serverless ElastiCache
func (tp *TemplatePricer) priceServerlessElastiCache(ctx context.Context, name string, props map[string]interface{}) (deployment.CostService, error) {
	// Extract cache usage limits
	var dataStorageGB int = 10 // default
	var ecpuMax int = 5000     // default

	if cacheUsageLimits, ok := props["CacheUsageLimits"].(map[string]interface{}); ok {
		if dataStorage, ok := cacheUsageLimits["DataStorage"].(map[string]interface{}); ok {
			if maximum, ok := dataStorage["Maximum"].(float64); ok {
				dataStorageGB = int(maximum)
			} else if maximum, ok := dataStorage["Maximum"].(int); ok {
				dataStorageGB = maximum
			}
		}
		if ecpuPerSecond, ok := cacheUsageLimits["ECPUPerSecond"].(map[string]interface{}); ok {
			if maximum, ok := ecpuPerSecond["Maximum"].(float64); ok {
				ecpuMax = int(maximum)
			} else if maximum, ok := ecpuPerSecond["Maximum"].(int); ok {
				ecpuMax = maximum
			}
		}
	}

	// Get pricing from API
	pricing, err := tp.pricingClient.GetServerlessElastiCachePricing(ctx, tp.region)
	if err != nil {
		// Fall back to static pricing
		// Based on AWS pricing as of Jan 2025:
		// - Data storage: ~$0.125/GB-month
		// - ECPU: ~$0.0034/ECPU-hour
		//
		// IMPORTANT: ECPU is billed on actual usage, not max capacity
		// For a typical low-traffic application (like a chat app):
		// - Average consumption: ~5-10 ECPU (not 1000+)
		// - Max capacity is for burst handling, not continuous use
		//
		// Conservative estimate: assume ~0.1% of max capacity for average usage
		storageCost := float64(dataStorageGB) * 0.125
		avgECPU := float64(ecpuMax) * 0.001 // 0.1% average utilization (5 ECPU for 5000 max)
		ecpuCost := avgECPU * 0.0034 * 730  // $/ECPU-hour × hours/month

		return deployment.CostService{
			Service: deployment.Service{
				Name:     name,
				Type:     "cache",
				Provider: "serverless-elasticache",
			},
			Plan:    fmt.Sprintf("%dGB storage, %d ECPU max (~%.0f avg)", dataStorageGB, ecpuMax, avgECPU),
			Storage: dataStorageGB,
			Cost:    storageCost + ecpuCost,
		}, nil
	}

	// Calculate actual costs from API pricing
	storageCost := float64(dataStorageGB) * pricing.DataStorageGBMonthlyCost
	avgECPU := float64(ecpuMax) * 0.001 // Assume 0.1% average utilization (conservative for low-traffic apps)
	ecpuCost := avgECPU * pricing.ECPUHourlyCost * 730

	return deployment.CostService{
		Service: deployment.Service{
			Name:     name,
			Type:     "cache",
			Provider: "serverless-elasticache",
		},
		Plan:    fmt.Sprintf("%dGB storage, %d ECPU max (~%.0f avg)", dataStorageGB, ecpuMax, avgECPU),
		Storage: dataStorageGB,
		Cost:    storageCost + ecpuCost,
	}, nil
}

// priceNATGateway estimates cost for a NAT Gateway
func (tp *TemplatePricer) priceNATGateway(ctx context.Context) (deployment.CostService, error) {
	pricing, err := tp.pricingClient.GetNATGatewayPricing(ctx, tp.region)
	if err != nil {
		// Fall back to static pricing (~$32.85/month)
		return deployment.CostService{
			Service: deployment.Service{
				Name:     "NAT Gateway",
				Type:     "networking",
				Provider: "vpc",
			},
			Plan: "standard",
			Cost: 32.85,
		}, nil
	}

	return deployment.CostService{
		Service: deployment.Service{
			Name:     "NAT Gateway",
			Type:     "networking",
			Provider: "vpc",
		},
		Plan: "standard",
		Cost: pricing,
	}, nil
}

// priceSecretsManagerSecret estimates cost for a Secrets Manager secret
func (tp *TemplatePricer) priceSecretsManagerSecret(name string) deployment.CostService {
	// Secrets Manager is $0.40 per secret per month
	return deployment.CostService{
		Service: deployment.Service{
			Name:     name,
			Type:     "secrets",
			Provider: "secretsmanager",
		},
		Plan: "standard",
		Cost: 0.40,
	}
}

// Helper function to extract string from CloudFormation intrinsic functions
func extractString(value interface{}) string {
	switch v := value.(type) {
	case string:
		return v
	case map[string]interface{}:
		// Handle CloudFormation intrinsic functions like Ref, Fn::Sub, etc.
		if ref, ok := v["Ref"].(string); ok {
			return ref
		}
		// For more complex functions, return a placeholder
		return ""
	default:
		return fmt.Sprintf("%v", v)
	}
}

// Helper function to extract integer from various formats
func extractInt(value interface{}) int {
	switch v := value.(type) {
	case int:
		return v
	case float64:
		return int(v)
	case string:
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return 0
}
