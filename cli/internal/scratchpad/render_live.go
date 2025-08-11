package scratchpad

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/meroxa/prod/cli/internal/analyzer"
	"github.com/meroxa/prod/cli/internal/deployment"
	"github.com/meroxa/prod/cli/internal/deployment/render"
	"github.com/meroxa/prod/cli/internal/output"
)

// LiveTestRenderDeployment tests a real deployment to Render using a live API key
func LiveTestRenderDeployment() {
	// Create console writer for output
	out := output.NewConsoleWriter()

	// Get API key from environment variable
	apiKey := os.Getenv("RENDER_API_KEY")
	if apiKey == "" {
		log.Fatal("RENDER_API_KEY environment variable must be set")
	}

	// Get tenant ID for Docker deployments
	tenantID := os.Getenv("TENANT_ID")
	if tenantID == "" {
		fmt.Fprintf(out, "⚠️  TENANT_ID not set - Docker deployment will be skipped\n\n")
	}

	fmt.Fprintf(out, "🚀 Starting live Render deployment test...\n\n")
	fmt.Fprintf(out, "📂 Analyzing test-projects/node-app...\n\n")

	// Step 1: Analyze the project
	// Debug: print current working directory
	cwd, _ := os.Getwd()
	fmt.Fprintf(out, "Current working directory: %s\n", cwd)

	// Use relative path from the cli directory
	projectPath := "../test-projects/node-app"

	// Convert to absolute path for Docker build
	absProjectPath, err := filepath.Abs(projectPath)
	if err != nil {
		log.Fatalf("❌ Failed to get absolute path: %v", err)
	}
	fmt.Fprintf(out, "Project path (relative): %s\n", projectPath)
	fmt.Fprintf(out, "Project path (absolute): %s\n", absProjectPath)

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

	fmt.Fprintf(out, "\n📊 Project Analysis Results:\n\n")
	fmt.Fprintf(out, "  Name: %s\n", projectSpec.Name)
	fmt.Fprintf(out, "  Language: %s\n", projectSpec.Language)
	fmt.Fprintf(out, "  Build Command: %s\n", projectSpec.BuildCommand)
	fmt.Fprintf(out, "  Start Command: %s\n", projectSpec.StartCommand)
	fmt.Fprintf(out, "  Service Requirements: %d\n", len(projectSpec.ServiceRequirements))
	for i, req := range projectSpec.ServiceRequirements {
		fmt.Fprintf(out, "    %d. %s (%s)\n", i+1, req.Provider, req.Type)
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
	fmt.Fprintf(out, "\nMetadata: buildContext=%s, tenantID=%s\n", deploymentSpec.Metadata["buildContext"], deploymentSpec.Metadata["tenantID"])

	// Convert service requirements to deployment services
	for _, req := range projectSpec.ServiceRequirements {
		service := deployment.Service{
			Name:     fmt.Sprintf("%s-%s", projectSpec.Name, req.Provider),
			Provider: req.Provider,
			Type:     req.Type,
		}
		deploymentSpec.Services = append(deploymentSpec.Services, service)
	}

	fmt.Fprintf(out, "\n🔧 Deployment Configuration:\n\n")
	fmt.Fprintf(out, "  App Name: %s\n", deploymentSpec.Name)
	fmt.Fprintf(out, "  Language: %s\n", deploymentSpec.Language)
	fmt.Fprintf(out, "  Build Command: %s\n", deploymentSpec.BuildCommand)
	fmt.Fprintf(out, "  Start Command: %s\n", deploymentSpec.StartCommand)
	fmt.Fprintf(out, "  Services: %d\n", len(deploymentSpec.Services))

	for i, service := range deploymentSpec.Services {
		fmt.Fprintf(out, "    %d. %s (%s %s)\n", i+1, service.Name, service.Provider, service.Type)
	}

	// Check if we should use Docker
	useDockerfile := true
	if tenantID != "" && deployment.IsDockerAvailable() {
		// Check if Dockerfile exists
		if _, err := os.Stat(fmt.Sprintf("%s/Dockerfile", projectPath)); err == nil {
			useDockerfile = true
			fmt.Fprintf(out, "\n🐳 Docker deployment enabled:\n")
			fmt.Fprintf(out, "  - Dockerfile found\n")
			fmt.Fprintf(out, "  - Docker daemon available\n")
			fmt.Fprintf(out, "  - Tenant ID configured\n")
		}
	}

	// Create HTTP client for real API calls
	httpClient := render.NewHTTPRenderClient(apiKey, out)

	// Create Docker generator
	dockerGen := deployment.NewDockerGenerator(out)
	defer dockerGen.Close()

	// Create queued deployment strategy
	deployment := render.NewQueuedDeployment(httpClient, deploymentSpec, dockerGen, useDockerfile, out)

	fmt.Fprintf(out, "\n📋 Executing deployment steps...\n")

	// Execute the deployment
	ctx := context.Background()
	_, err = deployment.Deploy(ctx)
	if err != nil {
		log.Fatalf("❌ Deployment failed: %v", err)
	}

	fmt.Fprintf(out, "\n✅ Live deployment test completed successfully!\n")
	fmt.Fprintf(out, "\n🎯 Next steps:\n")
	fmt.Fprintf(out, "1. Check your Render dashboard at https://dashboard.render.com\n")
	fmt.Fprintf(out, "2. Verify the services were created:\n")
	fmt.Fprintf(out, "   - Web service: %s-web\n", deploymentSpec.Name)
	for _, service := range deploymentSpec.Services {
		fmt.Fprintf(out, "   - %s service: %s\n", service.Type, service.Name)
	}
	fmt.Fprintf(out, "3. Check the web service logs for connection details\n")
	if useDockerfile {
		fmt.Fprintf(out, "4. Verify Docker image was pushed to ECR\n")
		fmt.Fprintf(out, "5. Check registry credential was created in Render\n")
	}
}

// TestWorkspaces tests listing workspaces
func TestWorkspaces(out output.UnifiedOutputWriter) {
	apiKey := os.Getenv("RENDER_API_KEY")
	if apiKey == "" {
		log.Fatal("RENDER_API_KEY environment variable must be set")
	}

	httpClient := render.NewHTTPRenderClient(apiKey, out)

	fmt.Fprintf(out, "🔍 Testing workspace lookup...\n")

	ctx := context.Background()
	workspaces, err := httpClient.ListWorkspaces(ctx)
	if err != nil {
		log.Fatalf("❌ Failed to list workspaces: %v", err)
	}

	fmt.Fprintf(out, "\nFound %d workspaces:\n", len(workspaces))
	for i, workspace := range workspaces {
		fmt.Fprintf(out, "  %d. %s (ID: %s, Email: %s)\n", i+1, workspace.Owner.Name, workspace.Owner.ID, workspace.Owner.Email)
	}
}

// TestDockerBuildLocal tests just the Docker build and push functionality
func TestDockerBuildLocal(out output.UnifiedOutputWriter) {
	tenantID := os.Getenv("TENANT_ID")
	if tenantID == "" {
		log.Fatal("TENANT_ID environment variable must be set for Docker testing")
	}

	fmt.Fprintf(out, "🐳 Testing Docker build and push...\n")

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
	dockerGen := deployment.NewDockerGenerator(out)
	defer dockerGen.Close()

	// Build and push
	ctx := context.Background()
	buildResult, pushResult, err := dockerGen.BuildAndPush(ctx, deploymentSpec, absProjectPath, tenantID)
	if err != nil {
		log.Fatalf("❌ Docker build/push failed: %v", err)
	}

	fmt.Fprintf(out, "\n✅ Docker build successful:\n\n")
	fmt.Fprintf(out, "  Image Name: %s\n", buildResult.ImageName)
	fmt.Fprintf(out, "  Image ID: %s\n", buildResult.ImageID)

	if pushResult != nil {
		fmt.Fprintf(out, "\n✅ Docker push successful:\n\n")
		fmt.Fprintf(out, "  Pushed Image URL: %s\n", pushResult.PushedImageURL)
	}
}

// TestRenderDeploymentOnly tests only the Render deployment part (no Docker build/push)
// This assumes an image has already been pushed to the registry
func TestRenderDeploymentOnly(out io.Writer) {
	// Get required environment variables
	apiKey := os.Getenv("RENDER_API_KEY")
	if apiKey == "" {
		log.Fatal("RENDER_API_KEY environment variable must be set")
	}

	tenantID := os.Getenv("TENANT_ID")
	if tenantID == "" {
		log.Fatal("TENANT_ID environment variable must be set")
	}

	fmt.Fprintf(out, "🚀 Starting Render deployment test (using pre-pushed image)...\n")
	fmt.Fprintf(out, "📂 Setting up deployment configuration...\n")

	// Create HTTP client for real API calls
	httpClient := render.NewHTTPRenderClient(apiKey, out)

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
	fmt.Fprintf(out, "Using workspace: %s (ID: %s)\n", workspaces[0].Owner.Name, ownerID)

	// Create Docker generator for getting pull credentials
	dockerGen := deployment.NewDockerGenerator(out)
	defer dockerGen.Close()

	// Step 1: Create registry credential
	fmt.Fprintf(out, "\n📋 Step 1: Creating registry credential...\n")
	registryCredStep := render.NewCreateRegistryCredentialStep(render.CreateRegistryCredentialStepConfig{
		ID:          "step-1",
		Description: "Create Docker registry credential in Render",
		Name:        "my-node-app-registry-cred",
		TenantID:    tenantID,
		OwnerID:     ownerID,
		DependsOn:   []string{}, // No dependencies
	})

	registryCred, err := registryCredStep.Execute(ctx, httpClient, map[string]any{})
	if err != nil {
		log.Fatalf("❌ Failed to create registry credential: %v", err)
	}

	fmt.Fprintf(out, "✅ Created registry credential: %s\n", registryCred.(*render.RegistryCredential).ID)

	// Step 2: Create web service
	fmt.Fprintf(out, "\n📋 Step 2: Creating web service...\n")

	// Prepare the web service step
	webServiceStep := render.NewCreateWebServiceStep(render.CreateWebServiceStepConfig{
		ID:                 "step-2",
		Description:        "Create web service with registry credential",
		Name:               "my-node-app-web",
		Type:               "web_service",
		OwnerID:            ownerID,
		BuildCommand:       "", // No build command for Docker
		StartCommand:       "", // No start command for Docker
		Environment:        "docker",
		Dockerfile:         "",                 // No dockerfile path
		DockerImageStepID:  "mock-docker-step", // Set a non-empty docker image step ID to trigger Docker path
		RegistryCredStepID: "step-1",           // Registry credential step ID
		TenantID:           tenantID,
		EnvVars:            map[string]string{}, // No env vars
		ConnectionStepIDs:  []string{},          // No connection steps
		DependsOn:          []string{"step-1"},  // Depends on registry credential
	})

	// Execute with step results containing the registry credential
	stepResults := map[string]any{
		"step-1": registryCred,
	}

	webService, err := webServiceStep.Execute(ctx, httpClient, stepResults)
	if err != nil {
		log.Fatalf("❌ Failed to create web service: %v", err)
	}

	renderService := webService.(*render.RenderService)
	fmt.Fprintf(out, "✅ Created web service: %s (ID: %s)\n", renderService.Name, renderService.ID)

	fmt.Fprintf(out, "\n✅ Render deployment test completed successfully!\n")
	fmt.Fprintf(out, "\n🎯 What was created:\n")
	fmt.Fprintf(out, "  - Registry credential for pulling from ECR\n")
	fmt.Fprintf(out, "  - Web service: %s\n", renderService.Name)
	fmt.Fprintf(out, "\n📝 Next steps:\n")
	fmt.Fprintf(out, "  1. Check your Render dashboard at https://dashboard.render.com\n")
	fmt.Fprintf(out, "  2. Verify the web service is running with the Docker image\n")
	fmt.Fprintf(out, "  3. Check the service logs for any issues\n")
}
