package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/pushtoprodai/prod-cli/internal/analyzer"
)

func main() {
	out := os.Stdout
	testProjects := []string{
		"../test-projects/flask-app",
		"../test-projects/django-app",
		"../test-projects/fastapi-app",
		"../test-projects/poetry-project",
		"../test-projects/pipenv-project",
		"../test-projects/node-app",
	}

	fmt.Fprintf(out, "🔍 Project Analyzer Test\n")
	fmt.Fprintf(out, "========================\n")

	for _, projectPath := range testProjects {
		fmt.Fprintf(out, "📁 Analyzing: %s\n", projectPath)
		fmt.Fprintf(out, "─%s\n", strings.Repeat("─", len(projectPath)+10))

		// Check if project exists
		if _, err := os.Stat(projectPath); os.IsNotExist(err) {
			fmt.Fprintf(out, "❌ Project not found: %s\n\n", projectPath)
			continue
		}

		// Get analyzer for the project
		analyzerPtr, err := analyzer.GetAnalyzer(projectPath)
		if err != nil {
			fmt.Fprintf(out, "❌ Failed to get analyzer: %v\n\n", err)
			continue
		}

		// Analyze the project
		spec, err := analyzerPtr.Analyze()
		if err != nil {
			fmt.Fprintf(out, "❌ Failed to analyze project: %v\n\n", err)
			continue
		}

		// Display results
		fmt.Fprintf(out, "✅ Project: %s\n", spec.Name)
		fmt.Fprintf(out, "🔤 Language: %s\n", spec.Language)

		if len(spec.ServiceRequirements) > 0 {
			fmt.Fprintf(out, "🔧 Required Services:\n")
			for _, service := range spec.ServiceRequirements {
				fmt.Fprintf(out, "  • %s (%s)\n", service.Type, service.Provider)
			}
		} else {
			fmt.Fprintf(out, "🔧 Required Services: None detected\n")
		}

		// Show JSON output
		fmt.Fprintf(out, "📄 JSON Output:\n")
		jsonData, err := json.MarshalIndent(spec, "", "  ")
		if err != nil {
			fmt.Fprintf(out, "❌ Failed to marshal JSON: %v\n", err)
		} else {
			fmt.Fprintf(out, "%s\n", string(jsonData))
		}

		fmt.Fprintf(out, "\n")
	}
}
