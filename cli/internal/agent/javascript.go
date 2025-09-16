package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-errors/errors"
	"github.com/meroxa/prod/cli/internal/analyzer"
	"github.com/meroxa/prod/cli/internal/deployment"
	"github.com/pmezard/go-difflib/difflib"
)

// create package.lock
func (a *Activities) createPackageLock(ctx context.Context, plan DeployPlan, forceRecreate bool) error {
	// plan.Source is the path to the project roots
	projectPath := plan.Source

	a.uiWriter.SendStatus("analyzing", "Checking for package.json...")

	// Check if package.json exists
	packageJsonPath := filepath.Join(projectPath, "package.json")
	if _, err := os.Stat(packageJsonPath); err != nil {
		a.uiWriter.SendStatusComplete("analyzing", "❌ No package.json found")
		return errors.Errorf("package.json not found in %s", projectPath)
	}

	// Check if package-lock.json already exists
	packageLockPath := filepath.Join(projectPath, "package-lock.json")
	if _, err := os.Stat(packageLockPath); err == nil && !forceRecreate {
		a.uiWriter.SendStatusComplete("analyzing", "✅ Package-lock.json already exists")
		return nil
	}

	if forceRecreate {
		a.uiWriter.SendStatus("installing", "Updating package-lock.json after dependency changes...")
		// Remove existing package-lock.json when forcing recreate to ensure clean state
		if _, err := os.Stat(packageLockPath); err == nil {
			if err := os.Remove(packageLockPath); err != nil {
				a.uiWriter.SendStatusComplete("installing", "❌ Failed to remove old package-lock.json")
				return errors.Errorf("failed to remove old package-lock.json: %w", err)
			}
		}
		// Also remove node_modules to ensure completely clean installation
		nodeModulesPath := filepath.Join(projectPath, "node_modules")
		if _, err := os.Stat(nodeModulesPath); err == nil {
			a.uiWriter.SendStatus("installing", "Removing node_modules for clean installation...")
			if err := os.RemoveAll(nodeModulesPath); err != nil {
				a.uiWriter.SendStatusComplete("installing", "❌ Failed to remove node_modules")
				return errors.Errorf("failed to remove node_modules: %w", err)
			}
		}
	} else {
		a.uiWriter.SendStatus("installing", "Creating package-lock.json with npm install...")
	}

	// Change to project directory and run npm install
	installCmd := exec.CommandContext(ctx, "npm", "install")
	installCmd.Dir = projectPath

	// Run npm install to generate package-lock.json
	output, err := installCmd.CombinedOutput()
	if err != nil {
		a.uiWriter.SendStatusComplete("installing", "❌ Failed to create package-lock.json")
		return errors.Errorf("failed to create package-lock.json: %w\nOutput: %s", err, string(output))
	}

	// Verify that package-lock.json was created
	if _, err := os.Stat(packageLockPath); err != nil {
		a.uiWriter.SendStatusComplete("installing", "❌ Package-lock.json was not created")
		return errors.Errorf("package-lock.json was not created after npm install")
	}

	a.uiWriter.SendStatusComplete("installing", "✅ Package-lock.json created successfully")
	return nil
}

// JavaScript configuration result containing both svelte and package.json diffs
type JavaScriptConfigResult struct {
	SvelteConfigDiff   []DiffLine `json:"svelteConfigDiff,omitempty"`
	PackageJsonDiff    []DiffLine `json:"packageJsonDiff,omitempty"`
	SvelteConfigPath   string     `json:"svelteConfigPath,omitempty"`
	PackageJsonUpdated bool       `json:"packageJsonUpdated"`
}

// patchPackageJSONForPlatform applies platform-specific package.json changes and returns updated content, changed flag, and error
func patchPackageJSONForPlatform(origPackageJson []byte, platform Platform, framework string) ([]byte, bool, error) {
	// Only apply SvelteKit adapter patches if this is actually a SvelteKit project
	if framework != "SvelteKit" {
		return origPackageJson, false, nil
	}

	// For platforms that need Svelte adapters
	switch platform {
	case Render, FlyIO:
		updatedPackageJson, err := patchPackageJSON(origPackageJson, "@sveltejs/adapter-node", "^5.2.0")
		if err != nil {
			return nil, false, err
		}
		// Check if anything changed
		changed := !bytes.Equal(origPackageJson, updatedPackageJson)
		return updatedPackageJson, changed, nil
	case Netlify:
		updatedPackageJson, err := patchPackageJSON(origPackageJson, "@sveltejs/adapter-netlify", "^4.3.0")
		if err != nil {
			return nil, false, err
		}
		// Check if anything changed
		changed := !bytes.Equal(origPackageJson, updatedPackageJson)
		return updatedPackageJson, changed, nil
	case Vercel:
		updatedPackageJson, err := patchPackageJSON(origPackageJson, "@sveltejs/adapter-vercel", "^5.10.2")
		if err != nil {
			return nil, false, err
		}
		// Check if anything changed
		changed := !bytes.Equal(origPackageJson, updatedPackageJson)
		return updatedPackageJson, changed, nil

	default:
		// For other platforms, just return the original content unchanged
		return origPackageJson, false, nil
	}
}

// handleSvelteConfig processes Svelte configuration updates for the specified platform
func (a *Activities) handleSvelteConfig(projectPath string, platform Platform) ([]DiffLine, string, error) {
	// Find svelte config file (prefer TS, fallback to JS)
	svelteConfigTS := filepath.Join(projectPath, "svelte.config.ts")
	svelteConfigJS := filepath.Join(projectPath, "svelte.config.js")
	var configPath string

	if _, err := os.Stat(svelteConfigTS); err == nil {
		configPath = svelteConfigTS
	} else if _, err := os.Stat(svelteConfigJS); err == nil {
		configPath = svelteConfigJS
	}

	// No Svelte config found, return empty results
	if configPath == "" {
		return nil, "", nil
	}

	// This is a Svelte project
	a.uiWriter.SendStatus("configuring", fmt.Sprintf("Updating Svelte config for %s platform...", platform))

	// Determine which adapter to use based on platform
	var newAdapter string
	switch platform {
	case Render, FlyIO:
		newAdapter = "@sveltejs/adapter-node"
	case Netlify:
		newAdapter = "@sveltejs/adapter-netlify"
	case Vercel:
		newAdapter = "@sveltejs/adapter-vercel"
	default:
		a.uiWriter.SendStatusComplete("configuring", "❌ Unsupported platform for Svelte")
		return nil, "", errors.Errorf("unsupported platform for Svelte: %s", platform)
	}

	// Read original config
	origConfig, err := os.ReadFile(configPath)
	if err != nil {
		a.uiWriter.SendStatusComplete("configuring", "❌ Failed to read Svelte config")
		return nil, "", errors.Errorf("failed to read %s: %w", configPath, err)
	}

	// Patch the config
	updatedConfig := patchSvelteConfig(origConfig, newAdapter)

	// Create backup in .prod directory
	prodDir := filepath.Join(projectPath, ".prod")
	if err := os.MkdirAll(prodDir, 0755); err != nil {
		a.uiWriter.SendStatusComplete("configuring", "❌ Failed to create backup directory")
		return nil, "", errors.Errorf("failed to create .prod directory: %w", err)
	}

	configFilename := filepath.Base(configPath)
	backupPath := filepath.Join(prodDir, fmt.Sprintf("%s.%s.bak", configFilename, time.Now().Format("20060102-150405")))
	if err := os.WriteFile(backupPath, origConfig, 0644); err != nil {
		a.uiWriter.SendStatusComplete("configuring", "❌ Failed to create Svelte config backup")
		return nil, "", errors.Errorf("failed to create backup at %s: %w", backupPath, err)
	}

	// Write updated config
	if err := os.WriteFile(configPath, updatedConfig, 0644); err != nil {
		a.uiWriter.SendStatusComplete("configuring", "❌ Failed to update Svelte config")
		return nil, "", errors.Errorf("failed to write updated config to %s: %w", configPath, err)
	}

	// Generate Svelte config diff
	diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(string(origConfig)),
		B:        difflib.SplitLines(string(updatedConfig)),
		FromFile: "svelte.config (before)",
		ToFile:   "svelte.config (after)",
		Context:  3,
	})
	if err != nil {
		return nil, "", errors.Errorf("failed to generate Svelte config diff: %w", err)
	}

	a.uiWriter.SendStatusComplete("configuring", fmt.Sprintf("✅ Svelte config updated for %s", newAdapter))

	return parseDiffString(diff), configPath, nil
}

// findRuntimeFramework extracts the runtime framework from ServiceRequirements
func findRuntimeFrameworkFromServiceRequirements(serviceRequirements []analyzer.ServiceRequirement) string {
	for _, sr := range serviceRequirements {
		if sr.Type == "framework" {
			return sr.Provider
		}
	}
	return ""
}

// updateJavaScriptConfig handles both Svelte config and package.json updates for JavaScript projects
func (a *Activities) updateJavaScriptConfig(_ context.Context, plan DeployPlan) (JavaScriptConfigResult, error) {
	projectPath := plan.Source
	result := JavaScriptConfigResult{}
	runtimeFramework := findRuntimeFrameworkFromServiceRequirements(plan.Spec.ServiceRequirements)

	a.uiWriter.SendStatus("configuring", "Configuring JavaScript project...")

	// First, handle package.json updates for all platforms
	packageJsonPath := filepath.Join(projectPath, "package.json")
	if _, err := os.Stat(packageJsonPath); err == nil {
		origPackageJson, err := os.ReadFile(packageJsonPath)
		if err != nil {
			a.uiWriter.SendStatusComplete("configuring", "❌ Failed to read package.json")
			return JavaScriptConfigResult{}, errors.Errorf("failed to read package.json: %w", err)
		}

		// Apply platform-specific package.json patches
		updatedPackageJson, packageUpdated, err := patchPackageJSONForPlatform(origPackageJson, plan.Platform, runtimeFramework)
		if err != nil {
			a.uiWriter.SendStatusComplete("configuring", "❌ Failed to patch package.json")
			return JavaScriptConfigResult{}, errors.Errorf("failed to patch package.json: %w", err)
		}

		if packageUpdated {
			// Create backup directory
			prodDir := filepath.Join(projectPath, ".prod")
			if err := os.MkdirAll(prodDir, 0755); err != nil {
				a.uiWriter.SendStatusComplete("configuring", "❌ Failed to create backup directory")
				return JavaScriptConfigResult{}, errors.Errorf("failed to create .prod directory: %w", err)
			}

			// Create backup for package.json
			packageJsonBackupPath := filepath.Join(prodDir, fmt.Sprintf("package.json.%s.bak", time.Now().Format("20060102-150405")))
			if err := os.WriteFile(packageJsonBackupPath, origPackageJson, 0644); err != nil {
				a.uiWriter.SendStatusComplete("configuring", "❌ Failed to create package.json backup")
				return JavaScriptConfigResult{}, errors.Errorf("failed to create package.json backup at %s: %w", packageJsonBackupPath, err)
			}

			// Write updated package.json
			if err := os.WriteFile(packageJsonPath, updatedPackageJson, 0644); err != nil {
				a.uiWriter.SendStatusComplete("configuring", "❌ Failed to write package.json")
				return JavaScriptConfigResult{}, errors.Errorf("failed to write updated package.json: %w", err)
			}

			// Generate package.json diff
			diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
				A:        difflib.SplitLines(string(origPackageJson)),
				B:        difflib.SplitLines(string(updatedPackageJson)),
				FromFile: "package.json (before)",
				ToFile:   "package.json (after)",
				Context:  3,
			})
			if err != nil {
				a.uiWriter.SendStatusComplete("configuring", "❌ Failed to generate package.json diff")
				return JavaScriptConfigResult{}, errors.Errorf("failed to generate package.json diff: %w", err)
			}

			result.PackageJsonDiff = parseDiffString(diff)
			result.PackageJsonUpdated = true
		}
	}

	// Handle Svelte config if this is a SvelteKit project
	var svelteConfigDiff []DiffLine
	var svelteConfigPath string
	if runtimeFramework == "SvelteKit" {
		var err error
		svelteConfigDiff, svelteConfigPath, err = a.handleSvelteConfig(projectPath, plan.Platform)
		if err != nil {
			return JavaScriptConfigResult{}, err
		}
	}

	result.SvelteConfigDiff = svelteConfigDiff
	result.SvelteConfigPath = svelteConfigPath

	// Summary message
	if result.PackageJsonUpdated || len(result.SvelteConfigDiff) > 0 {
		a.uiWriter.SendStatusComplete("configuring", "✅ JavaScript project configuration completed")
	} else {
		a.uiWriter.SendStatusComplete("configuring", "✅ No configuration changes needed")
	}

	return result, nil
}

func (a *Activities) prepareNuxtBuild(_ context.Context, plan DeployPlan) (DeployPlan, error) {
	a.uiWriter.SendStatus("configuring", "Checking for Nuxt configuration...")

	isNuxt := false
	for _, sr := range plan.Spec.ServiceRequirements {
		if strings.ToLower(sr.Provider) == "nuxt" {
			isNuxt = true
			break
		}
	}
	if !isNuxt {
		a.uiWriter.SendStatusComplete("configuring", "✅ No Nuxt config found, skipping")
		return plan, nil
	}

	switch plan.Platform {
	case Render, FlyIO:
		plan.Spec.StartCommand = "node .output/server/index.mjs"
		plan.CollectedEnvVars = append(plan.CollectedEnvVars, deployment.EnvVar{Name: "NITRO_PRESET", Value: "node-server"})
	case Netlify:
		plan.CollectedEnvVars = append(plan.CollectedEnvVars, deployment.EnvVar{Name: "SERVER_PRESET", Value: "netlify_edge"})
		plan.Spec.BuildOutput.Path = "dist"
	case Vercel:
		plan.CollectedEnvVars = append(plan.CollectedEnvVars, deployment.EnvVar{Name: "SERVER_PRESET", Value: "vercel_edge"})
	}
	a.uiWriter.SendStatusComplete("configuring", "✅ Nuxt configuration complete")
	return plan, nil
}

// patchSvelteConfig updates the Svelte config to use the specified adapter
func patchSvelteConfig(input []byte, newAdapter string) []byte {
	// Replace adapter import
	importRe := regexp.MustCompile(`(?m)^import\s+adapter\s+from\s+['"].+['"];`)
	updated := importRe.ReplaceAll(input, fmt.Appendf(nil, `import adapter from '%s';`, newAdapter))

	// Ensure kit.adapter exists
	kitRe := regexp.MustCompile(`adapter\s*:\s*adapter\(\s*\)`)
	if !kitRe.Match(updated) {
		updated = bytes.Replace(updated,
			[]byte("kit: {"),
			[]byte("kit: {\n    adapter: adapter(),"),
			1)
	}
	return updated
}

// patchPackageJSON adds the adapter dependency to dependencies, ensuring only one Svelte adapter exists
// and updates build scripts for Netlify adapter
func patchPackageJSON(input []byte, adapter, version string) ([]byte, error) {
	var pkg map[string]any
	if err := json.Unmarshal(input, &pkg); err != nil {
		return nil, err
	}

	// List of known Svelte adapters to remove
	svelteAdapters := []string{
		"@sveltejs/adapter-auto",
		"@sveltejs/adapter-cloudflare",
		"@sveltejs/adapter-cloudflare-workers",
		"@sveltejs/adapter-netlify",
		"@sveltejs/adapter-node",
		"@sveltejs/adapter-static",
		"@sveltejs/adapter-vercel",
	}

	// Remove existing Svelte adapters from both dependencies and devDependencies
	for _, section := range []string{"dependencies", "devDependencies"} {
		if deps, ok := pkg[section].(map[string]any); ok {
			for _, existingAdapter := range svelteAdapters {
				delete(deps, existingAdapter)
			}
		}
	}

	// Add the new adapter to dependencies
	deps, ok := pkg["dependencies"].(map[string]any)
	if !ok {
		deps = map[string]any{}
	}
	deps[adapter] = version
	pkg["dependencies"] = deps

	// Handle scripts section based on adapter
	scripts, ok := pkg["scripts"].(map[string]any)
	if !ok {
		scripts = map[string]any{}
	}

	// Clean up any existing Netlify-specific scripts first
	cleanupNetlifyScripts(scripts)

	// If this is the Netlify adapter, add Netlify-specific scripts
	if adapter == "@sveltejs/adapter-netlify" {
		// Add netlify-functions-build script
		scripts["netlify-functions-build"] = "netlify functions:build --functions .netlify/functions --src .netlify/functions-internal"

		// Update build script to include functions build
		currentBuild, hasBuild := scripts["build"]
		if hasBuild {
			if buildStr, ok := currentBuild.(string); ok {
				// Append the functions build to the existing build command
				scripts["build"] = buildStr + " && npm run netlify-functions-build"
			}
		} else {
			// No existing build script, create one
			scripts["build"] = "vite build && npm run netlify-functions-build"
		}
	}

	pkg["scripts"] = scripts

	// Use custom encoder to prevent HTML escaping (e.g., && becoming \u0026)
	buffer := &bytes.Buffer{}
	encoder := json.NewEncoder(buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(pkg); err != nil {
		return nil, err
	}

	// Remove the trailing newline that Encode adds
	result := buffer.Bytes()
	if len(result) > 0 && result[len(result)-1] == '\n' {
		result = result[:len(result)-1]
	}

	return result, nil
}

// cleanupNetlifyScripts removes Netlify-specific scripts and cleans up build scripts
func cleanupNetlifyScripts(scripts map[string]any) {
	// Remove netlify-functions-build script
	delete(scripts, "netlify-functions-build")

	// Clean up build script if it contains Netlify-specific commands
	if buildScript, exists := scripts["build"]; exists {
		if buildStr, ok := buildScript.(string); ok {
			// Remove all variations of netlify-functions-build from the build script
			cleanedBuild := strings.ReplaceAll(buildStr, " && npm run netlify-functions-build", "")
			cleanedBuild = strings.ReplaceAll(cleanedBuild, "npm run netlify-functions-build && ", "")

			// Handle case where it might be the only command
			if cleanedBuild == "npm run netlify-functions-build" {
				cleanedBuild = "vite build" // Default SvelteKit build command
			}

			// Update the script if it changed
			if cleanedBuild != buildStr {
				scripts["build"] = cleanedBuild
			}
		}
	}
}

// parseDiffString converts a unified diff string into structured DiffLine data
func parseDiffString(diffStr string) []DiffLine {
	if diffStr == "" {
		return nil
	}

	lines := strings.Split(diffStr, "\n")
	var diffLines []DiffLine

	for _, line := range lines {
		var lineType string

		if strings.HasPrefix(line, "@@") {
			lineType = "header"
		} else if strings.HasPrefix(line, "---") || strings.HasPrefix(line, "+++") {
			lineType = "fileheader"
		} else if strings.HasPrefix(line, "+") {
			lineType = "added"
		} else if strings.HasPrefix(line, "-") {
			lineType = "removed"
		} else {
			lineType = "context"
		}

		diffLines = append(diffLines, DiffLine{
			Type:    lineType,
			Content: line,
		})
	}

	return diffLines
}

// restoreFromBackup restores svelte.config.js|ts from the latest backup
func (a *Activities) restoreFromBackup(_ context.Context, plan DeployPlan) ([]DiffLine, error) {
	projectPath := plan.Source
	prodDir := filepath.Join(projectPath, ".prod")

	a.uiWriter.SendStatus("restoring", "Looking for Svelte config backups...")

	// Check if .prod directory exists
	if _, err := os.Stat(prodDir); err != nil {
		a.uiWriter.SendStatusComplete("restoring", "❌ No backup directory found")
		return nil, errors.Errorf("no .prod backup directory found in %s", projectPath)
	}

	// Find svelte config file that should be restored (prefer TS, fallback to JS)
	configPath := ""
	svelteConfigTS := filepath.Join(projectPath, "svelte.config.ts")
	svelteConfigJS := filepath.Join(projectPath, "svelte.config.js")
	configFilename := ""

	if _, err := os.Stat(svelteConfigTS); err == nil {
		configPath = svelteConfigTS
		configFilename = "svelte.config.ts"
	} else if _, err := os.Stat(svelteConfigJS); err == nil {
		configPath = svelteConfigJS
		configFilename = "svelte.config.js"
	} else {
		a.uiWriter.SendStatusComplete("restoring", "❌ No Svelte config found to restore")
		return nil, errors.Errorf("no svelte.config.{ts,js} found to restore in %s", projectPath)
	}

	a.uiWriter.SendStatus("restoring", fmt.Sprintf("Finding latest backup for %s...", configFilename))

	// Find the latest backup file
	backupPath, err := findLatestBackup(prodDir, configFilename)
	if err != nil {
		a.uiWriter.SendStatusComplete("restoring", "❌ No backup files found")
		return nil, errors.Errorf("failed to find backup for %s: %w", configFilename, err)
	}

	// Read current config for diff
	currentConfig, err := os.ReadFile(configPath)
	if err != nil {
		a.uiWriter.SendStatusComplete("restoring", "❌ Failed to read current config")
		return nil, errors.Errorf("failed to read current config %s: %w", configPath, err)
	}

	// Read backup
	backupConfig, err := os.ReadFile(backupPath)
	if err != nil {
		a.uiWriter.SendStatusComplete("restoring", "❌ Failed to read backup file")
		return nil, errors.Errorf("failed to read backup %s: %w", backupPath, err)
	}

	// Restore from backup
	if err := os.WriteFile(configPath, backupConfig, 0644); err != nil {
		a.uiWriter.SendStatusComplete("restoring", "❌ Failed to restore from backup")
		return nil, errors.Errorf("failed to restore config from backup: %w", err)
	}

	a.uiWriter.SendStatusComplete("restoring", "✅ Svelte config restored from backup")

	// Generate diff showing the restoration
	diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(string(currentConfig)),
		B:        difflib.SplitLines(string(backupConfig)),
		FromFile: "svelte.config (current)",
		ToFile:   "svelte.config (restored)",
		Context:  3,
	})
	if err != nil {
		return nil, errors.Errorf("failed to generate diff: %w", err)
	}

	return parseDiffString(diff), nil
}

// findLatestBackup finds the most recent backup file for a given config filename
func findLatestBackup(prodDir, configFilename string) (string, error) {
	entries, err := os.ReadDir(prodDir)
	if err != nil {
		return "", errors.Errorf("failed to read .prod directory: %w", err)
	}

	var backups []string
	prefix := configFilename + "."
	suffix := ".bak"

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, suffix) {
			backups = append(backups, name)
		}
	}

	if len(backups) == 0 {
		return "", errors.Errorf("no backup files found for %s", configFilename)
	}

	// Sort backups by filename (which includes timestamp) to get the latest
	sort.Strings(backups)
	latestBackup := backups[len(backups)-1]

	return filepath.Join(prodDir, latestBackup), nil
}
