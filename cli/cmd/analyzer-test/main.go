package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/meroxa/prod/cli/internal/analyzer"
)

func main() {
	// Test projects to analyze
	testProjects := []string{
		"../test-projects/flask-app",
		"../test-projects/django-app",
		"../test-projects/fastapi-app",
		"../test-projects/poetry-project",
		"../test-projects/pipenv-project",
		"../test-projects/node-app",
	}

	fmt.Println("🔍 Project Analyzer Test")
	fmt.Println("========================\n")

	for _, projectPath := range testProjects {
		fmt.Printf("📁 Analyzing: %s\n", projectPath)
		fmt.Println("─" + strings.Repeat("─", len(projectPath)+10))

		// Check if project exists
		if _, err := os.Stat(projectPath); os.IsNotExist(err) {
			fmt.Printf("❌ Project not found: %s\n\n", projectPath)
			continue
		}

		// Get analyzer for the project
		analyzerPtr, err := analyzer.GetAnalyzer(projectPath)
		if err != nil {
			fmt.Printf("❌ Failed to get analyzer: %v\n\n", err)
			continue
		}

		// Analyze the project
		spec, err := (*analyzerPtr).Analyze()
		if err != nil {
			fmt.Printf("❌ Failed to analyze project: %v\n\n", err)
			continue
		}

		// Display results
		fmt.Printf("✅ Project: %s\n", spec.Name)
		fmt.Printf("🔤 Language: %s\n", spec.Language)

		if len(spec.ServiceRequirements) > 0 {
			fmt.Printf("🔧 Required Services:\n")
			for _, service := range spec.ServiceRequirements {
				fmt.Printf("  • %s (%s)\n", service.Type, service.Provider)
			}
		} else {
			fmt.Printf("🔧 Required Services: None detected\n")
		}

		// Show JSON output
		fmt.Printf("📄 JSON Output:\n")
		jsonData, err := json.MarshalIndent(spec, "", "  ")
		if err != nil {
			fmt.Printf("❌ Failed to marshal JSON: %v\n", err)
		} else {
			fmt.Println(string(jsonData))
		}

		fmt.Println()
	}
}
