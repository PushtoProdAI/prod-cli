package scratchpad

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/meroxa/prod/cli/internal/analyzer"
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

	// Get tenant ID for Docker deployments
	tenantID := os.Getenv("TENANT_ID")
	if tenantID == "" {
		fmt.Println("⚠️  TENANT_ID not set - Docker deployment will be skipped")
	}

	fmt.Println("🚀 Starting live Render deployment test...")
	fmt.Println("📂 Analyzing test-projects/node-app...")

	// Step 1: Analyze the project
	// Debug: print current working directory
	cwd, _ := os.Getwd()
	fmt.Printf("Current working directory: %s\n", cwd)

	// Use relative path from the cli directory
	projectPath := "../test-projects/node-app"

	// Convert to absolute path for Docker build
	absProjectPath, err := filepath.Abs(projectPath)
	if err != nil {
		log.Fatalf("❌ Failed to get absolute path: %v", err)
	}
	fmt.Printf("Project path (relative): %s\n", projectPath)
	fmt.Printf("Project path (absolute): %s\n", absProjectPath)

	// Check if the directory exists
	if _, err := os.Stat(absProjectPath); os.IsNotExist(err) {
		log.Fatalf("❌ Project directory does not exist: %s", absProjectPath)
	}

	// Check for package.json
	packageJsonPath := filepath.Join(absProjectPath, "package.json")
	if _, err := os.Stat(packageJsonPath); os.IsNotExist(err) {
		log.Fatalf("❌ package.json does not exist: %s", packageJsonPath)
	}

	analyzer, err := analyzer.GetAnalyzer(absProjectPath)
	if err != nil {
		log.Fatalf("❌ Failed to get analyzer: %v", err)
	}

	projectSpec, err := analyzer.Analyze()
	if err != nil {
		log.Fatalf("❌ Failed to analyze project: %v", err)
	}

	fmt.Printf("\n📊 Project Analysis Results:\n")
	fmt.Printf("  Name: %s\n", projectSpec.Name)
	fmt.Printf("  Language: %s\n", projectSpec.Language)
	fmt.Printf("  Build Command: %s\n", projectSpec.BuildCommand)
	fmt.Printf("  Start Command: %s\n", projectSpec.StartCommand)
	fmt.Printf("  Service Requirements: %d\n", len(projectSpec.ServiceRequirements))
	for i, req := range projectSpec.ServiceRequirements {
		fmt.Printf("    %d. %s (%s)\n", i+1, req.Provider, req.Type)
	}

	// Step 2: Convert to deployment spec
	deploymentSpec := &deployment.DeploymentSpec{
		Name:         projectSpec.Name,
		Language:     projectSpec.Language,
		BuildCommand: "",
		StartCommand: projectSpec.StartCommand,
		Services:     []deployment.Service{},
		Metadata:     make(map[string]any),
	}

	// Add metadata for deployment
	deploymentSpec.Metadata["buildContext"] = absProjectPath
	if tenantID != "" {
		deploymentSpec.Metadata["tenantID"] = tenantID
	}
	fmt.Printf("\nMetadata: buildContext=%s, tenantID=%s\n", deploymentSpec.Metadata["buildContext"], deploymentSpec.Metadata["tenantID"])

	// Convert service requirements to deployment services
	for _, req := range projectSpec.ServiceRequirements {
		service := deployment.Service{
			Name:     fmt.Sprintf("%s-%s", projectSpec.Name, req.Provider),
			Provider: req.Provider,
			Type:     req.Type,
		}
		deploymentSpec.Services = append(deploymentSpec.Services, service)
	}

	fmt.Printf("\n🔧 Deployment Configuration:\n")
	fmt.Printf("  App Name: %s\n", deploymentSpec.Name)
	fmt.Printf("  Language: %s\n", deploymentSpec.Language)
	fmt.Printf("  Build Command: %s\n", deploymentSpec.BuildCommand)
	fmt.Printf("  Start Command: %s\n", deploymentSpec.StartCommand)
	fmt.Printf("  Services: %d\n", len(deploymentSpec.Services))

	for i, service := range deploymentSpec.Services {
		fmt.Printf("    %d. %s (%s %s)\n", i+1, service.Name, service.Provider, service.Type)
	}

	// Check if we should use Docker
	useDockerfile := true
	if tenantID != "" && deployment.IsDockerAvailable() {
		// Check if Dockerfile exists
		if _, err := os.Stat(fmt.Sprintf("%s/Dockerfile", projectPath)); err == nil {
			useDockerfile = true
			fmt.Println("\n🐳 Docker deployment enabled:")
			fmt.Println("  - Dockerfile found")
			fmt.Println("  - Docker daemon available")
			fmt.Println("  - Tenant ID configured")
		}
	}

	// Create HTTP client for real API calls
	httpClient := render.NewHTTPRenderClient(apiKey)

	// Create Docker generator
	dockerGen := deployment.NewDockerGenerator()
	defer dockerGen.Close()

	// Create queued deployment strategy
	deployment := render.NewQueuedDeployment(httpClient, deploymentSpec, dockerGen, useDockerfile)

	fmt.Println("\n📋 Executing deployment steps...")

	// Execute the deployment
	ctx := context.Background()
	_, err = deployment.Deploy(ctx)
	if err != nil {
		log.Fatalf("❌ Deployment failed: %v", err)
	}

	fmt.Println("\n✅ Live deployment test completed successfully!")
	fmt.Println("\n🎯 Next steps:")
	fmt.Println("1. Check your Render dashboard at https://dashboard.render.com")
	fmt.Println("2. Verify the services were created:")
	fmt.Printf("   - Web service: %s-web\n", deploymentSpec.Name)
	for _, service := range deploymentSpec.Services {
		fmt.Printf("   - %s service: %s\n", service.Type, service.Name)
	}
	fmt.Println("3. Check the web service logs for connection details")
	if useDockerfile {
		fmt.Println("4. Verify Docker image was pushed to ECR")
		fmt.Println("5. Check registry credential was created in Render")
	}
}

// TestWorkspaces tests listing workspaces
func TestWorkspaces() {
	apiKey := os.Getenv("RENDER_API_KEY")
	if apiKey == "" {
		log.Fatal("RENDER_API_KEY environment variable must be set")
	}

	httpClient := render.NewHTTPRenderClient(apiKey)

	fmt.Println("🔍 Testing workspace lookup...")

	ctx := context.Background()
	workspaces, err := httpClient.ListWorkspaces(ctx)
	if err != nil {
		log.Fatalf("❌ Failed to list workspaces: %v", err)
	}

	fmt.Printf("\nFound %d workspaces:\n", len(workspaces))
	for i, workspace := range workspaces {
		fmt.Printf("  %d. %s (ID: %s, Email: %s)\n", i+1, workspace.Owner.Name, workspace.Owner.ID, workspace.Owner.Email)
	}
}

// TestDockerBuildLocal tests just the Docker build and push functionality
func TestDockerBuildLocal() {
	tenantID := os.Getenv("TENANT_ID")
	if tenantID == "" {
		log.Fatal("TENANT_ID environment variable must be set for Docker testing")
	}

	fmt.Println("🐳 Testing Docker build and push...")

	// Analyze the project first
	projectPath := "../test-projects/node-app"
	absProjectPath, err := filepath.Abs(projectPath)
	if err != nil {
		log.Fatalf("❌ Failed to get absolute path: %v", err)
	}

	analyzer, err := analyzer.GetAnalyzer(absProjectPath)
	if err != nil {
		log.Fatalf("❌ Failed to get analyzer: %v", err)
	}

	projectSpec, err := analyzer.Analyze()
	if err != nil {
		log.Fatalf("❌ Failed to analyze project: %v", err)
	}

	// Create deployment spec
	deploymentSpec := &deployment.DeploymentSpec{
		Name:         projectSpec.Name,
		Language:     projectSpec.Language,
		BuildCommand: projectSpec.BuildCommand,
		StartCommand: projectSpec.StartCommand,
	}

	// Create Docker generator
	dockerGen := deployment.NewDockerGenerator()
	defer dockerGen.Close()

	// Build and push
	ctx := context.Background()
	buildResult, pushResult, err := dockerGen.BuildAndPush(ctx, deploymentSpec, absProjectPath, tenantID)
	if err != nil {
		log.Fatalf("❌ Docker build/push failed: %v", err)
	}

	fmt.Printf("\n✅ Docker build successful:\n")
	fmt.Printf("  Image Name: %s\n", buildResult.ImageName)
	fmt.Printf("  Image ID: %s\n", buildResult.ImageID)

	if pushResult != nil {
		fmt.Printf("\n✅ Docker push successful:\n")
		fmt.Printf("  Pushed Image URL: %s\n", pushResult.PushedImageURL)
	}
}

// TestRenderDeploymentOnly tests only the Render deployment part (no Docker build/push)
// This assumes an image has already been pushed to the registry
func TestRenderDeploymentOnly() {
	// Get required environment variables
	apiKey := os.Getenv("RENDER_API_KEY")
	if apiKey == "" {
		log.Fatal("RENDER_API_KEY environment variable must be set")
	}

	tenantID := os.Getenv("TENANT_ID")
	if tenantID == "" {
		log.Fatal("TENANT_ID environment variable must be set")
	}

	fmt.Println("🚀 Starting Render deployment test (using pre-pushed image)...")
	fmt.Println("📂 Setting up deployment configuration...")

	// Create HTTP client for real API calls
	httpClient := render.NewHTTPRenderClient(apiKey)

	// Get workspace first
	ctx := context.Background()
	workspaces, err := httpClient.ListWorkspaces(ctx)
	if err != nil {
		log.Fatalf("❌ Failed to list workspaces: %v", err)
	}

	if len(workspaces) == 0 {
		log.Fatal("❌ No workspaces found")
	}

	ownerID := workspaces[0].Owner.ID
	fmt.Printf("Using workspace: %s (ID: %s)\n", workspaces[0].Owner.Name, ownerID)

	// Create Docker generator for getting pull credentials
	dockerGen := deployment.NewDockerGenerator()
	defer dockerGen.Close()

	// Step 1: Create registry credential
	fmt.Println("\n📋 Step 1: Creating registry credential...")
	registryCredStep := render.NewCreateRegistryCredentialStep(
		"step-1",
		"Create Docker registry credential in Render",
		"my-node-app-registry-cred",
		tenantID,
		ownerID,
		[]string{}, // No dependencies
	)

	registryCred, err := registryCredStep.Execute(ctx, httpClient, map[string]any{})
	if err != nil {
		log.Fatalf("❌ Failed to create registry credential: %v", err)
	}

	fmt.Printf("✅ Created registry credential: %s\n", registryCred.(*render.RegistryCredential).ID)

	// Step 2: Create web service
	fmt.Println("\n📋 Step 2: Creating web service...")

	// Prepare the web service step
	webServiceStep := render.NewCreateWebServiceStep(
		"step-2",
		"Create web service with registry credential",
		"my-node-app-web",
		"web_service",
		ownerID,
		"", // No build command for Docker
		"", // No start command for Docker
		"docker",
		"",                 // No dockerfile path
		"mock-docker-step", // Set a non-empty docker image step ID to trigger Docker path
		"step-1",           // Registry credential step ID
		tenantID,
		map[string]string{}, // No env vars
		[]string{},          // No connection steps
		[]string{"step-1"},  // Depends on registry credential
	)

	// Execute with step results containing the registry credential
	stepResults := map[string]any{
		"step-1": registryCred,
	}

	webService, err := webServiceStep.Execute(ctx, httpClient, stepResults)
	if err != nil {
		log.Fatalf("❌ Failed to create web service: %v", err)
	}

	renderService := webService.(*render.RenderService)
	fmt.Printf("✅ Created web service: %s (ID: %s)\n", renderService.Name, renderService.ID)

	fmt.Println("\n✅ Render deployment test completed successfully!")
	fmt.Println("\n🎯 What was created:")
	fmt.Println("  - Registry credential for pulling from ECR")
	fmt.Printf("  - Web service: %s\n", renderService.Name)
	fmt.Println("\n📝 Next steps:")
	fmt.Println("  1. Check your Render dashboard at https://dashboard.render.com")
	fmt.Println("  2. Verify the web service is running with the Docker image")
	fmt.Println("  3. Check the service logs for any issues")
}
