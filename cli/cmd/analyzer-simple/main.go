package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/meroxa/prod/cli/internal/analyzer"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run cmd/analyzer-simple/main.go <project-path>")
		fmt.Println("Example: go run cmd/analyzer-simple/main.go ../test-projects/flask-app")
		os.Exit(1)
	}

	projectPath := os.Args[1]

	// Get analyzer for the project
	analyzerPtr, err := analyzer.GetAnalyzer(projectPath)
	if err != nil {
		log.Fatalf("Failed to get analyzer: %v", err)
	}

	// Analyze the project
	spec, err := (*analyzerPtr).Analyze()
	if err != nil {
		log.Fatalf("Failed to analyze project: %v", err)
	}

	// Display results
	fmt.Printf("Project Analysis Results:\n")
	fmt.Printf("========================\n")
	fmt.Printf("Name: %s\n", spec.Name)
	fmt.Printf("Language: %s\n", spec.Language)
	fmt.Printf("Services Required: %d\n", len(spec.ServiceRequirements))

	if len(spec.ServiceRequirements) > 0 {
		fmt.Printf("\nRequired Services:\n")
		for i, service := range spec.ServiceRequirements {
			fmt.Printf("  %d. %s (%s)\n", i+1, service.Type, service.Provider)
		}
	}

	// Output as JSON
	fmt.Printf("\nJSON Output:\n")
	jsonData, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		log.Fatalf("Failed to marshal JSON: %v", err)
	}
	fmt.Println(string(jsonData))
}
