package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"os"

	"github.com/meroxa/prod/cli/internal/backend"
	"github.com/meroxa/prod/cli/internal/config"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run cmd/llm-test/main.go <function> [args...]")
		fmt.Println("Available functions:")
		fmt.Println("  ExtractIntent <request>")
		fmt.Println("  SummarizeIntent <intent> <name> <language>")
		fmt.Println("  SummarizeSteps <steps>")
		fmt.Println("  SummarizeDeployError <error> <os> <intent> <spec>")
		fmt.Println("")
		fmt.Println("Examples:")
		fmt.Println("  go run cmd/llm-test/main.go ExtractIntent \"Deploy my React app to Render\"")
		fmt.Println("  go run cmd/llm-test/main.go SummarizeIntent '{\"action\":\"DEPLOY\",\"platform\":\"Render\"}' \"my-app\" \"nodejs\"")
		os.Exit(1)
	}

	function := os.Args[1]
	args := os.Args[2:]

	// Initialize client
	client := backend.NewClient()
	ctx := context.Background()

	// Get configuration
	llmMode := config.GetLLMMode()
	preferredModel := config.GetPreferredModel()
	fallbackEnabledStr := config.GetFallbackEnabled()
	fallbackEnabled := fallbackEnabledStr == "true"

	fmt.Printf("LLM Configuration:\n")
	fmt.Printf("  Mode: %s\n", llmMode)
	fmt.Printf("  Preferred Model: %s\n", preferredModel)
	fmt.Printf("  Fallback Enabled: %t\n", fallbackEnabled)
	fmt.Printf("  Supabase URL: %s\n", config.GetSupabaseURL())
	fmt.Printf("  Supabase Key: %s\n", maskKey(config.GetSupabaseAnonKey()))
	fmt.Println()

	slog.Info("LLM Configuration:\n")
	slog.Info("  Mode: %s\n", llmMode)
	slog.Info("  Preferred Model: %s\n", preferredModel)
	slog.Info("  Fallback Enabled: %t\n", fallbackEnabled)
	slog.Info("  Supabase URL: %s\n", config.GetSupabaseURL())

	// Build function arguments
	var functionArgs map[string]interface{}

	switch function {
	case "ExtractIntent":
		if len(args) < 1 {
			log.Fatal("ExtractIntent requires a request argument")
		}
		functionArgs = map[string]interface{}{
			"request": args[0],
		}

	case "SummarizeIntent":
		if len(args) < 3 {
			log.Fatal("SummarizeIntent requires intent, name, and language arguments")
		}
		var intent map[string]interface{}
		if err := json.Unmarshal([]byte(args[0]), &intent); err != nil {
			log.Fatalf("Failed to parse intent JSON: %v", err)
		}
		functionArgs = map[string]interface{}{
			"intent":   intent,
			"name":     args[1],
			"language": args[2],
		}

	case "SummarizeSteps":
		if len(args) < 1 {
			log.Fatal("SummarizeSteps requires a steps argument")
		}
		var steps []string
		if err := json.Unmarshal([]byte(args[0]), &steps); err != nil {
			log.Fatalf("Failed to parse steps JSON: %v", err)
		}
		functionArgs = map[string]interface{}{
			"steps": steps,
		}

	case "SummarizeDeployError":
		if len(args) < 4 {
			log.Fatal("SummarizeDeployError requires error, os, intent, and spec arguments")
		}
		var intent, spec map[string]interface{}
		if err := json.Unmarshal([]byte(args[2]), &intent); err != nil {
			log.Fatalf("Failed to parse intent JSON: %v", err)
		}
		if err := json.Unmarshal([]byte(args[3]), &spec); err != nil {
			log.Fatalf("Failed to parse spec JSON: %v", err)
		}
		functionArgs = map[string]interface{}{
			"errorMsg": args[0],
			"os":       args[1],
			"intent":   intent,
			"spec":     spec,
		}

	default:
		log.Fatalf("Unknown function: %s", function)
	}

	// Test local mode first if configured
	if config.IsLocalMode() {
		fmt.Println("Testing local LLM mode...")
		testLocalMode(ctx, client, function, functionArgs)
		fmt.Println()
	}

	// Test proxy mode if configured
	if config.IsProxyMode() {
		fmt.Println("Testing proxy LLM mode...")
		testProxyMode(ctx, client, function, functionArgs, preferredModel, fallbackEnabled)
		fmt.Println()
	}

	// Test usage statistics
	fmt.Println("Testing usage statistics...")
	testUsageStats(ctx, client)
}

func testLocalMode(ctx context.Context, client *backend.Client, function string, args map[string]interface{}) {
	// This would test the local BAML client
	// For now, just print that we would test it
	fmt.Printf("  [LOCAL] Would test function: %s\n", function)
	fmt.Printf("  [LOCAL] Args: %+v\n", args)
}

func testProxyMode(ctx context.Context, client *backend.Client, function string, args map[string]interface{}, preferredModel string, fallbackEnabled bool) {
	// Get the Supabase anon key for authentication
	authToken := config.GetSupabaseAnonKey()

	response, err := client.CallLLMFunction(
		ctx,
		authToken,
		function,
		args,
		preferredModel,
		fallbackEnabled,
	)

	if err != nil {
		fmt.Printf("  [PROXY] Error: %v\n", err)
		return
	}

	if !response.Success {
		fmt.Printf("  [PROXY] Failed: %v\n", response.Error)
		return
	}

	fmt.Printf("  [PROXY] Success!\n")
	fmt.Printf("  [PROXY] Model Used: %s\n", response.Metadata.ModelUsed)
	fmt.Printf("  [PROXY] Tokens Used: %d\n", response.Metadata.TokensUsed)
	fmt.Printf("  [PROXY] Cost: $%.6f\n", response.Metadata.Cost)
	fmt.Printf("  [PROXY] Response Time: %dms\n", response.Metadata.ResponseTimeMs)

	// Pretty print the result
	resultJSON, err := json.MarshalIndent(response.Result, "  [PROXY] ", "  ")
	if err != nil {
		fmt.Printf("  [PROXY] Result: %+v\n", response.Result)
	} else {
		fmt.Printf("  [PROXY] Result:\n%s\n", string(resultJSON))
	}
}

func testUsageStats(ctx context.Context, client *backend.Client) {
	// Get the Supabase anon key for authentication
	authToken := config.GetSupabaseAnonKey()

	stats, err := client.GetLLMUsage(ctx, authToken, "", 30)
	if err != nil {
		fmt.Printf("  [USAGE] Error: %v\n", err)
		return
	}

	fmt.Printf("  [USAGE] Total Requests: %d\n", stats.TotalRequests)
	fmt.Printf("  [USAGE] Total Tokens: %d\n", stats.TotalTokens)
	fmt.Printf("  [USAGE] Total Cost: $%.6f\n", stats.TotalCost)
	fmt.Printf("  [USAGE] Avg Response Time: %.2fms\n", stats.AverageLatency)

	if len(stats.RequestsByModel) > 0 {
		fmt.Printf("  [USAGE] By Model:\n")
		for model, requests := range stats.RequestsByModel {
			cost := stats.CostByModel[model]
			fmt.Printf("    %s: %d requests, $%.6f\n", model, requests, cost)
		}
	}

	if len(stats.RequestsByDay) > 0 {
		fmt.Printf("  [USAGE] By Day:\n")
		for day, requests := range stats.RequestsByDay {
			fmt.Printf("    %s: %d requests\n", day, requests)
		}
	}
}

func maskKey(key string) string {
	if key == "" {
		return "(not set)"
	}
	if len(key) < 8 {
		return "***"
	}
	return key[:4] + "***" + key[len(key)-4:]
}
