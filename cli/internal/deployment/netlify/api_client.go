package netlify

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-errors/errors"

	"github.com/pushtoprodai/prod-cli/internal/deployment"
)

// NetlifyAPIClient implements deployment using direct Netlify API calls
type NetlifyAPIClient struct {
	spec         *deployment.DeploymentSpec
	writer       io.Writer
	client       *http.Client
	token        string
	siteID       string
	apiURL       string
	functionZips map[string][]byte // Stores zipped function data for upload
}

// NewNetlifyAPIClient creates a new API-based Netlify client
func NewNetlifyAPIClient(spec *deployment.DeploymentSpec, writer io.Writer) *NetlifyAPIClient {
	return &NetlifyAPIClient{
		spec:   spec,
		writer: writer,
		client: &http.Client{Timeout: 30 * time.Second},
		apiURL: "https://api.netlify.com/api/v1",
	}
}

// Deploy implements the deployment.Deployable interface using Netlify API
func (n *NetlifyAPIClient) Deploy(ctx context.Context) ([]deployment.CreatedResource, error) {
	var resources []deployment.CreatedResource

	// Step 1: Initialize configuration
	if err := n.initConfig(); err != nil {
		return resources, errors.Errorf("failed to initialize config: %w", err)
	}

	// Step 2: Get paths
	sourcePath := n.getSourcePath()
	publishDir := n.getPublishDir()

	// Step 3: Run build if configured
	if n.spec.BuildCommand != "" {
		fmt.Fprintf(n.writer, "🔨 Running build command: %s\n", n.spec.BuildCommand)
		if err := n.runBuildCommand(sourcePath); err != nil {
			return resources, errors.Errorf("build failed: %w", err)
		}
	}

	// Step 4: Hash files in publish directory
	fmt.Fprintf(n.writer, "📦 Preparing files for deployment...\n")
	files, err := n.hashFiles(filepath.Join(sourcePath, publishDir))
	if err != nil {
		return resources, errors.Errorf("failed to hash files: %w", err)
	}

	// Step 4b: Hash functions if directory exists
	functionsDir := n.getFunctionsDir()
	functions := make(map[string]string)
	if functionsDir != "" {
		functionsPath := filepath.Join(sourcePath, functionsDir)
		if _, err := os.Stat(functionsPath); err == nil {
			fmt.Fprintf(n.writer, "📦 Preparing functions for deployment...\n")
			functions, err = n.hashFunctions(functionsPath)
			if err != nil {
				return resources, errors.Errorf("failed to hash functions: %w", err)
			}
		}
	}

	// Step 5: Create deployment
	fmt.Fprintf(n.writer, "🚀 Creating deployment...\n")
	deployID, requiredFiles, requiredFunctions, err := n.createDeployment(files, functions)
	if err != nil {
		return resources, errors.Errorf("failed to create deployment: %w", err)
	}

	// Step 6: Upload required files
	if len(requiredFiles) > 0 {
		fmt.Fprintf(n.writer, "📤 Uploading %d files...\n", len(requiredFiles))
		if err := n.uploadFiles(deployID, filepath.Join(sourcePath, publishDir), requiredFiles); err != nil {
			return resources, errors.Errorf("failed to upload files: %w", err)
		}
	}

	// Step 6b: Upload required functions
	if len(requiredFunctions) > 0 {
		fmt.Fprintf(n.writer, "📤 Uploading %d functions...\n", len(requiredFunctions))
		if err := n.uploadFunctions(deployID, requiredFunctions); err != nil {
			return resources, errors.Errorf("failed to upload functions: %w", err)
		}
	}

	// Step 7: Wait for deployment to be ready
	fmt.Fprintf(n.writer, "⏳ Waiting for deployment to go live...\n")
	deployURL, err := n.waitForDeployment(deployID)
	if err != nil {
		return resources, errors.Errorf("deployment failed: %w", err)
	}

	fmt.Fprintf(n.writer, "✅ Deployment successful!\n")
	fmt.Fprintf(n.writer, "🌐 Site URL: %s\n", deployURL)

	resources = append(resources, deployment.CreatedResource{
		ID:   deployID,
		Type: "web",
		Name: n.spec.Name,
		Metadata: map[string]interface{}{
			"url":    deployURL,
			"siteID": n.siteID,
		},
	})

	return resources, nil
}

// initConfig initializes the Netlify configuration
func (n *NetlifyAPIClient) initConfig() error {
	// Get token from environment or metadata
	n.token = os.Getenv("NETLIFY_AUTH_TOKEN")
	if n.token == "" {
		if token, ok := n.spec.Metadata["netlifyToken"].(string); ok {
			n.token = token
		}
	}
	if n.token == "" {
		return errors.Errorf("NETLIFY_AUTH_TOKEN not set")
	}

	// Get or create site ID
	if siteID, ok := n.spec.Metadata["siteID"].(string); ok && siteID != "" {
		n.siteID = siteID
	} else {
		// Create new site if no ID provided
		siteID, err := n.createSite()
		if err != nil {
			return errors.Errorf("failed to create site: %w", err)
		}
		n.siteID = siteID
	}

	return nil
}

// createSite creates a new Netlify site
func (n *NetlifyAPIClient) createSite() (string, error) {
	payload := map[string]interface{}{
		"name": n.spec.Name,
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", fmt.Sprintf("%s/sites", n.apiURL), bytes.NewBuffer(body))
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", n.token))
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", errors.Errorf("failed to create site: %s", body)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	siteID, _ := result["id"].(string)
	fmt.Fprintf(n.writer, "📝 Created new site: %s\n", n.spec.Name)
	return siteID, nil
}

// hashFiles creates SHA256 hashes for all files in a directory
func (n *NetlifyAPIClient) hashFiles(dir string) (map[string]string, error) {
	files := make(map[string]string)

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories and hidden files
		if info.IsDir() || strings.HasPrefix(info.Name(), ".") {
			return nil
		}

		// Calculate relative path for Netlify
		relPath, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}

		// Normalize path separators for Netlify (always use forward slash)
		relPath = "/" + strings.ReplaceAll(relPath, string(filepath.Separator), "/")

		// Hash file
		hash, err := n.hashFile(path)
		if err != nil {
			return errors.Errorf("failed to hash %s: %w", path, err)
		}

		files[relPath] = hash
		return nil
	})

	return files, err
}

// hashFile calculates SHA256 hash of a file (Netlify uses SHA256 for file digests)
func (n *NetlifyAPIClient) hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// createDeployment creates a new deployment and returns required files and functions to upload
func (n *NetlifyAPIClient) createDeployment(files, functions map[string]string) (string, []string, []string, error) {
	payload := map[string]interface{}{
		"files":     files,
		"functions": functions,
		"draft":     false,
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", fmt.Sprintf("%s/sites/%s/deploys", n.apiURL, n.siteID), bytes.NewBuffer(body))
	if err != nil {
		return "", nil, nil, err
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", n.token))
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		return "", nil, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return "", nil, nil, errors.Errorf("failed to create deployment: %s", body)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", nil, nil, err
	}

	deployID, _ := result["id"].(string)

	// Get list of required files (files that need to be uploaded)
	var requiredFiles []string
	if required, ok := result["required"].([]interface{}); ok {
		for _, file := range required {
			if hash, ok := file.(string); ok {
				// Find the file path for this hash
				for path, fileHash := range files {
					if fileHash == hash {
						requiredFiles = append(requiredFiles, path)
						break
					}
				}
			}
		}
	}

	// Get list of required functions
	var requiredFunctions []string
	if requiredFuncs, ok := result["required_functions"].([]interface{}); ok {
		for _, fn := range requiredFuncs {
			if hash, ok := fn.(string); ok {
				// Find the function path for this hash
				for path, funcHash := range functions {
					if funcHash == hash {
						requiredFunctions = append(requiredFunctions, path)
						break
					}
				}
			}
		}
	}

	return deployID, requiredFiles, requiredFunctions, nil
}

// uploadFiles uploads required files to the deployment
func (n *NetlifyAPIClient) uploadFiles(deployID, baseDir string, files []string) error {
	for i, filePath := range files {
		// Remove leading slash for file system path
		fsPath := strings.TrimPrefix(filePath, "/")
		fullPath := filepath.Join(baseDir, fsPath)

		// Read file content
		content, err := os.ReadFile(fullPath)
		if err != nil {
			return errors.Errorf("failed to read file %s: %w", fullPath, err)
		}

		// Upload file
		url := fmt.Sprintf("%s/deploys/%s/files%s", n.apiURL, deployID, filePath)
		req, err := http.NewRequest("PUT", url, bytes.NewBuffer(content))
		if err != nil {
			return err
		}

		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", n.token))
		req.Header.Set("Content-Type", "application/octet-stream")

		resp, err := n.client.Do(req)
		if err != nil {
			return errors.Errorf("failed to upload %s: %w", filePath, err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return errors.Errorf("failed to upload %s: status %d", filePath, resp.StatusCode)
		}

		// Show progress
		fmt.Fprintf(n.writer, "  Uploaded %d/%d: %s\n", i+1, len(files), filePath)
	}

	return nil
}

// waitForDeployment polls the deployment status until it's ready
func (n *NetlifyAPIClient) waitForDeployment(deployID string) (string, error) {
	maxAttempts := 60 // 5 minutes with 5-second intervals

	for i := 0; i < maxAttempts; i++ {
		req, err := http.NewRequest("GET", fmt.Sprintf("%s/deploys/%s", n.apiURL, deployID), nil)
		if err != nil {
			return "", err
		}

		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", n.token))

		resp, err := n.client.Do(req)
		if err != nil {
			return "", err
		}

		var result map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			return "", err
		}
		resp.Body.Close()

		state, _ := result["state"].(string)

		switch state {
		case "ready":
			// Deployment is live
			if sslURL, ok := result["ssl_url"].(string); ok && sslURL != "" {
				return sslURL, nil
			}
			if deployURL, ok := result["deploy_ssl_url"].(string); ok && deployURL != "" {
				return deployURL, nil
			}
			if url, ok := result["url"].(string); ok && url != "" {
				return url, nil
			}
		case "error":
			errorMsg, _ := result["error_message"].(string)
			return "", errors.Errorf("deployment failed: %s", errorMsg)
		}

		// Wait before next poll
		time.Sleep(5 * time.Second)
	}

	return "", errors.Errorf("deployment timed out")
}

// getSourcePath gets the source path for deployment
func (n *NetlifyAPIClient) getSourcePath() string {
	if path, ok := n.spec.Metadata["buildContext"].(string); ok && path != "" {
		return path
	}
	return "."
}

// getPublishDir determines the publish directory
func (n *NetlifyAPIClient) getPublishDir() string {
	// Check if explicitly set in metadata
	if dir, ok := n.spec.Metadata["publishDir"].(string); ok && dir != "" {
		return dir
	}

	// Common build output directories
	commonDirs := []string{
		"dist",
		"build",
		"out",
		"public",
		"_site",
		".next",
		"output",
	}

	sourcePath := n.getSourcePath()
	for _, dir := range commonDirs {
		if _, err := os.Stat(filepath.Join(sourcePath, dir)); err == nil {
			return dir
		}
	}

	// Default to current directory
	return "."
}

// getFunctionsDir determines the functions directory
func (n *NetlifyAPIClient) getFunctionsDir() string {
	// Check if explicitly set in metadata
	if dir, ok := n.spec.Metadata["functionsDir"].(string); ok && dir != "" {
		return dir
	}

	// Common functions directories
	commonDirs := GetCommonFunctionDirs()

	sourcePath := n.getSourcePath()
	for _, dir := range commonDirs {
		if _, err := os.Stat(filepath.Join(sourcePath, dir)); err == nil {
			return dir
		}
	}

	// No functions directory found
	return ""
}

// hashFunctions creates SHA256 hashes for all functions in a directory
// Functions are zipped before hashing as required by Netlify API
func (n *NetlifyAPIClient) hashFunctions(dir string) (map[string]string, error) {
	functions := make(map[string]string)

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Only process JavaScript/TypeScript files
		ext := filepath.Ext(path)
		if ext != ".js" && ext != ".ts" && ext != ".mjs" && ext != ".jsx" && ext != ".tsx" {
			return nil
		}

		// Calculate relative path for Netlify
		relPath, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}

		// Remove extension and path separators for function name
		functionName := strings.TrimSuffix(relPath, ext)
		functionName = strings.ReplaceAll(functionName, string(filepath.Separator), "-")

		// Create a zip of the function
		zipData, err := n.zipFunction(path)
		if err != nil {
			return errors.Errorf("failed to zip function %s: %w", path, err)
		}

		// Hash the zipped function with SHA256
		hasher := sha256.New()
		hasher.Write(zipData)
		hash := hex.EncodeToString(hasher.Sum(nil))

		functions[functionName] = hash

		// Store the zipped data in memory for later upload
		// We'll need to pass this data along somehow
		if n.functionZips == nil {
			n.functionZips = make(map[string][]byte)
		}
		n.functionZips[functionName] = zipData

		return nil
	})

	return functions, err
}

// zipFunction creates a zip archive containing a single function file
func (n *NetlifyAPIClient) zipFunction(filePath string) ([]byte, error) {
	var buf bytes.Buffer
	zipWriter := zip.NewWriter(&buf)

	// Read the function file
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	// Get just the filename for the zip entry
	fileName := filepath.Base(filePath)

	// Create a file in the zip
	writer, err := zipWriter.Create(fileName)
	if err != nil {
		return nil, err
	}

	// Write the file content
	if _, err := writer.Write(content); err != nil {
		return nil, err
	}

	// Close the zip writer
	if err := zipWriter.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// uploadFunctions uploads required functions to the deployment
func (n *NetlifyAPIClient) uploadFunctions(deployID string, functions []string) error {
	for i, functionName := range functions {
		// Get the zipped function data from our cache
		zipData, ok := n.functionZips[functionName]
		if !ok {
			return errors.Errorf("no zip data found for function %s", functionName)
		}

		// Upload function as a zip with runtime parameter
		url := fmt.Sprintf("%s/deploys/%s/functions/%s?runtime=js", n.apiURL, deployID, functionName)

		req, err := http.NewRequest("PUT", url, bytes.NewBuffer(zipData))
		if err != nil {
			return err
		}

		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", n.token))
		req.Header.Set("Content-Type", "application/zip")

		resp, err := n.client.Do(req)
		if err != nil {
			return errors.Errorf("failed to upload function %s: %w", functionName, err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return errors.Errorf("failed to upload function %s: status %d", functionName, resp.StatusCode)
		}

		// Show progress
		fmt.Fprintf(n.writer, "  Uploaded function %d/%d: %s\n", i+1, len(functions), functionName)
	}

	return nil
}

// runBuildCommand executes the build command
func (n *NetlifyAPIClient) runBuildCommand(workDir string) error {
	// Parse the build command
	parts := strings.Fields(n.spec.BuildCommand)
	if len(parts) == 0 {
		return errors.Errorf("empty build command")
	}

	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Dir = workDir
	cmd.Stdout = n.writer
	cmd.Stderr = n.writer

	// Set up environment
	cmd.Env = os.Environ()

	// Add any environment variables from spec
	for _, envVar := range n.spec.EnvVars {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", envVar.Name, envVar.Value))
	}

	// Run the build
	if err := cmd.Run(); err != nil {
		return errors.Errorf("build command failed: %w", err)
	}

	fmt.Fprintf(n.writer, "✅ Build completed successfully\n")
	return nil
}
