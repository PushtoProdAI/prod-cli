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

// update svelte.config.js
func (a *Activities) updateSvelteConfig(_ context.Context, plan DeployPlan) (string, error) {
	projectPath := plan.Source

	a.uiWriter.SendStatus("configuring", "Checking for Svelte configuration...")

	// Determine which adapter to use based on platform
	var newAdapter string
	switch plan.Platform {
	case Render, FlyIO:
		newAdapter = "@sveltejs/adapter-node"
	case Netlify:
		newAdapter = "@sveltejs/adapter-netlify"
	default:
		a.uiWriter.SendStatusComplete("configuring", "❌ Unsupported platform for Svelte")
		return "", errors.Errorf("unsupported platform for Svelte: %s", plan.Platform)
	}

	// Find svelte config file (prefer TS, fallback to JS)
	configPath := ""
	svelteConfigTS := filepath.Join(projectPath, "svelte.config.ts")
	svelteConfigJS := filepath.Join(projectPath, "svelte.config.js")

	if _, err := os.Stat(svelteConfigTS); err == nil {
		configPath = svelteConfigTS
	} else if _, err := os.Stat(svelteConfigJS); err == nil {
		configPath = svelteConfigJS
	} else {
		a.uiWriter.SendStatusComplete("configuring", "✅ No Svelte config found, skipping")
		return "", nil
	}

	a.uiWriter.SendStatus("configuring", fmt.Sprintf("Updating Svelte config for %s platform...", plan.Platform))

	// Read original config
	origConfig, err := os.ReadFile(configPath)
	if err != nil {
		a.uiWriter.SendStatusComplete("configuring", "❌ Failed to read Svelte config")
		return "", errors.Errorf("failed to read %s: %w", configPath, err)
	}

	// Patch the config
	updatedConfig := patchSvelteConfig(origConfig, newAdapter)

	// Create backup in .prod directory
	prodDir := filepath.Join(projectPath, ".prod")
	if err := os.MkdirAll(prodDir, 0755); err != nil {
		a.uiWriter.SendStatusComplete("configuring", "❌ Failed to create backup directory")
		return "", errors.Errorf("failed to create .prod directory: %w", err)
	}

	configFilename := filepath.Base(configPath)
	backupPath := filepath.Join(prodDir, fmt.Sprintf("%s.%s.bak", configFilename, time.Now().Format("20060102-150405")))
	if err := os.WriteFile(backupPath, origConfig, 0644); err != nil {
		a.uiWriter.SendStatusComplete("configuring", "❌ Failed to create backup")
		return "", errors.Errorf("failed to create backup at %s: %w", backupPath, err)
	}

	// Write updated config
	if err := os.WriteFile(configPath, updatedConfig, 0644); err != nil {
		a.uiWriter.SendStatusComplete("configuring", "❌ Failed to update Svelte config")
		return "", errors.Errorf("failed to write updated config to %s: %w", configPath, err)
	}

	// Update package.json to add the adapter dependency
	packageJsonPath := filepath.Join(projectPath, "package.json")
	if _, err := os.Stat(packageJsonPath); err == nil {
		origPackageJson, err := os.ReadFile(packageJsonPath)
		if err != nil {
			a.uiWriter.SendStatusComplete("configuring", "❌ Failed to read package.json")
			return "", errors.Errorf("failed to read package.json: %w", err)
		}

		// Determine adapter version - using latest stable versions
		version := "^5.2.0"
		if newAdapter == "@sveltejs/adapter-netlify" {
			version = "^4.3.0"
		}

		// Create backup for package.json as well
		packageJsonFilename := "package.json"
		packageJsonBackupPath := filepath.Join(prodDir, fmt.Sprintf("%s.%s.bak", packageJsonFilename, time.Now().Format("20060102-150405")))
		if err := os.WriteFile(packageJsonBackupPath, origPackageJson, 0644); err != nil {
			a.uiWriter.SendStatusComplete("configuring", "❌ Failed to create package.json backup")
			return "", errors.Errorf("failed to create package.json backup at %s: %w", packageJsonBackupPath, err)
		}

		updatedPackageJson, err := patchPackageJSON(origPackageJson, newAdapter, version)
		if err != nil {
			a.uiWriter.SendStatusComplete("configuring", "❌ Failed to update package.json")
			return "", errors.Errorf("failed to patch package.json: %w", err)
		}

		if err := os.WriteFile(packageJsonPath, updatedPackageJson, 0644); err != nil {
			a.uiWriter.SendStatusComplete("configuring", "❌ Failed to write package.json")
			return "", errors.Errorf("failed to write updated package.json: %w", err)
		}
	}

	a.uiWriter.SendStatusComplete("configuring", fmt.Sprintf("✅ Svelte config updated for %s", newAdapter))

	// Generate diff
	diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(string(origConfig)),
		B:        difflib.SplitLines(string(updatedConfig)),
		FromFile: "svelte.config (before)",
		ToFile:   "svelte.config (after)",
		Context:  3,
	})
	if err != nil {
		return "", errors.Errorf("failed to generate diff: %w", err)
	}

	return diff, nil
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

	return json.MarshalIndent(pkg, "", "  ")
}

// restoreFromBackup restores svelte.config.js|ts from the latest backup
func (a *Activities) restoreFromBackup(_ context.Context, plan DeployPlan) (string, error) {
	projectPath := plan.Source
	prodDir := filepath.Join(projectPath, ".prod")

	a.uiWriter.SendStatus("restoring", "Looking for Svelte config backups...")

	// Check if .prod directory exists
	if _, err := os.Stat(prodDir); err != nil {
		a.uiWriter.SendStatusComplete("restoring", "❌ No backup directory found")
		return "", errors.Errorf("no .prod backup directory found in %s", projectPath)
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
		return "", errors.Errorf("no svelte.config.{ts,js} found to restore in %s", projectPath)
	}

	a.uiWriter.SendStatus("restoring", fmt.Sprintf("Finding latest backup for %s...", configFilename))

	// Find the latest backup file
	backupPath, err := findLatestBackup(prodDir, configFilename)
	if err != nil {
		a.uiWriter.SendStatusComplete("restoring", "❌ No backup files found")
		return "", errors.Errorf("failed to find backup for %s: %w", configFilename, err)
	}

	// Read current config for diff
	currentConfig, err := os.ReadFile(configPath)
	if err != nil {
		a.uiWriter.SendStatusComplete("restoring", "❌ Failed to read current config")
		return "", errors.Errorf("failed to read current config %s: %w", configPath, err)
	}

	// Read backup
	backupConfig, err := os.ReadFile(backupPath)
	if err != nil {
		a.uiWriter.SendStatusComplete("restoring", "❌ Failed to read backup file")
		return "", errors.Errorf("failed to read backup %s: %w", backupPath, err)
	}

	// Restore from backup
	if err := os.WriteFile(configPath, backupConfig, 0644); err != nil {
		a.uiWriter.SendStatusComplete("restoring", "❌ Failed to restore from backup")
		return "", errors.Errorf("failed to restore config from backup: %w", err)
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
		return "", errors.Errorf("failed to generate diff: %w", err)
	}

	return diff, nil
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
