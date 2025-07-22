package scratchpad

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/meroxa/prod/cli/internal/deployment"
	"github.com/meroxa/prod/cli/internal/deployment/render"
)

// LiveTestRenderDeployment tests a real deployment to Render using a live API key
func LiveTestRenderDeployment() {
	// Get API key from environment variable
	apiKey := os.Getenv("RENDER_API_KEY")
	if apiKey == "" {
		log.Fatal("RENDER_API_KEY environment variable must be set")
	}

	// Create deployment spec for a sample Node.js app with Redis and Postgres
	spec := &deployment.DeploymentSpec{
		Name:         "live-test-node-app",
		Language:     "node",
		BuildCommand: "npm install",
		StartCommand: "npm start",
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

	fmt.Println("🚀 Starting live Render deployment test...")
	fmt.Printf("App Name: %s\n", spec.Name)
	fmt.Printf("Language: %s\n", spec.Language)
	fmt.Printf("Build Command: %s\n", spec.BuildCommand)
	fmt.Printf("Start Command: %s\n", spec.StartCommand)
	fmt.Printf("Services: %d\n", len(spec.Services))
	
	for i, service := range spec.Services {
		fmt.Printf("  Service %d: %s (%s %s)\n", i+1, service.Name, service.Provider, service.Type)
	}

	// Create HTTP client for real API calls
	httpClient := render.NewHTTPRenderClient(apiKey)
	
	// Create Docker generator (we'll test native deployment)
	dockerGen := deployment.NewDockerGenerator()
	
	// Create queued deployment strategy
	deployment := render.NewQueuedDeployment(httpClient, spec, dockerGen, false)
	
	fmt.Println("\n📋 Executing deployment steps...")
	
	// Execute the deployment
	ctx := context.Background()
	err := deployment.Deploy(ctx)
	if err != nil {
		log.Fatalf("❌ Deployment failed: %v", err)
	}
	
	fmt.Println("✅ Live deployment test completed successfully!")
	fmt.Println("\n🎯 Next steps:")
	fmt.Println("1. Check your Render dashboard at https://dashboard.render.com")
	fmt.Println("2. Verify the project and services were created")
	fmt.Println("3. Test the connection strings in your app")
}

// TestProjectLookup tests finding existing projects
func TestProjectLookup() {
	apiKey := os.Getenv("RENDER_API_KEY")
	if apiKey == "" {
		log.Fatal("RENDER_API_KEY environment variable must be set")
	}

	httpClient := render.NewHTTPRenderClient(apiKey)
	
	fmt.Println("🔍 Testing project lookup...")
	
	ctx := context.Background()
	projects, err := httpClient.ListProjects(ctx)
	if err != nil {
		log.Fatalf("❌ Failed to list projects: %v", err)
	}
	
	fmt.Printf("Found %d existing projects:\n", len(projects))
	for i, project := range projects {
		fmt.Printf("  %d. %s (ID: %s, Env: %s)\n", i+1, project.Name, project.ID, project.Environment)
	}
}