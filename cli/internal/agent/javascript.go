package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-errors/errors"
	"github.com/meroxa/prod/cli/internal/analyzer"
	"github.com/pmezard/go-difflib/difflib"
)

// JavaScript configuration result containing framework config and package.json diffs
type JavaScriptConfigResult struct {
	ConfigDiff         []DiffLine `json:"configDiff,omitempty"`
	ConfigPath         string     `json:"configPath,omitempty"`
	PackageJsonDiff    []DiffLine `json:"packageJsonDiff,omitempty"`
	PackageJsonUpdated bool       `json:"packageJsonUpdated"`
}

// createPackageLockJSON creates package-lock.json for JavaScript projects
func (a *Activities) createPackageLockJSON(ctx context.Context, plan DeployPlan, forceRecreate bool) error {
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

	// Check if package.json contains packages that are known to have peer dependency conflicts
	packageJsonContent, readErr := os.ReadFile(packageJsonPath)
	useLegacyPeerDeps := false
	if readErr == nil {
		if strings.Contains(string(packageJsonContent), "@vercel/remix") {
			useLegacyPeerDeps = true
			a.uiWriter.SendStatus("installing", "Detected @vercel/remix - using --legacy-peer-deps to handle peer dependency conflicts...")
		}
	}

	// Change to project directory and run npm install
	var installCmd *exec.Cmd
	if useLegacyPeerDeps {
		installCmd = exec.CommandContext(ctx, "npm", "install", "--legacy-peer-deps")
	} else {
		installCmd = exec.CommandContext(ctx, "npm", "install")
	}
	installCmd.Dir = projectPath

	// Run npm install to generate package-lock.json
	output, err := installCmd.CombinedOutput()
	if err != nil {
		// If npm install fails due to peer dependency conflicts, try progressive fallbacks
		outputStr := string(output)
		if strings.Contains(outputStr, "ERESOLVE") || strings.Contains(outputStr, "peer dep") || strings.Contains(outputStr, "peer dependency") {
			a.uiWriter.SendStatus("installing", "Retrying npm install with --legacy-peer-deps due to peer dependency conflicts...")

			// First retry with --legacy-peer-deps flag
			retryCmd := exec.CommandContext(ctx, "npm", "install", "--legacy-peer-deps")
			retryCmd.Dir = projectPath
			retryOutput, retryErr := retryCmd.CombinedOutput()

			if retryErr != nil {
				retryOutputStr := string(retryOutput)
				// Check if the error is due to missing commands in postinstall scripts
				needsIgnoreScripts := strings.Contains(retryOutputStr, "command not found") ||
					strings.Contains(retryOutputStr, "patch-package")

				// Second retry with --force flag as last resort
				a.uiWriter.SendStatus("installing", "Retrying npm install with --force as final attempt...")
				var forceCmd *exec.Cmd
				if needsIgnoreScripts {
					a.uiWriter.SendStatus("installing", "Detected script errors, using --ignore-scripts to skip postinstall scripts...")
					forceCmd = exec.CommandContext(ctx, "npm", "install", "--force", "--ignore-scripts")
				} else {
					forceCmd = exec.CommandContext(ctx, "npm", "install", "--force")
				}
				forceCmd.Dir = projectPath
				forceOutput, forceErr := forceCmd.CombinedOutput()

				if forceErr != nil {
					a.uiWriter.SendStatusComplete("installing", "❌ Failed to create package-lock.json with all fallback options")
					return errors.Errorf("failed to create package-lock.json: %w\nOriginal output: %s\nLegacy-peer-deps output: %s\nForce output: %s", forceErr, outputStr, string(retryOutput), string(forceOutput))
				} else {
					if needsIgnoreScripts {
						a.uiWriter.SendStatusComplete("installing", "⚠️ Package-lock.json created with --force and --ignore-scripts (postinstall scripts skipped)")
					} else {
						a.uiWriter.SendStatusComplete("installing", "⚠️ Package-lock.json created with --force flag (peer dependency conflicts ignored)")
					}
				}
			} else {
				a.uiWriter.SendStatusComplete("installing", "⚠️ Package-lock.json created with --legacy-peer-deps")
			}
		} else {
			a.uiWriter.SendStatusComplete("installing", "❌ Failed to create package-lock.json")
			return errors.Errorf("failed to create package-lock.json: %w\nOutput: %s", err, string(output))
		}
	}

	// Verify that package-lock.json was created
	if _, err := os.Stat(packageLockPath); err != nil {
		a.uiWriter.SendStatusComplete("installing", "❌ Package-lock.json was not created")
		return errors.Errorf("package-lock.json was not created after npm install")
	}

	a.uiWriter.SendStatusComplete("installing", "✅ Package-lock.json created successfully")
	return nil
}

// patchPackageJSONForPlatform applies platform-specific package.json changes and returns updated content, changed flag, and error
func patchPackageJSONForPlatform(origPackageJson []byte, platform Platform, framework string) ([]byte, bool, error) {
	handler := frameworkRegistry.GetHandler(framework)
	if handler == nil {
		// No specific handler for this framework, return unchanged
		return origPackageJson, false, nil
	}

	return handler.PatchPackageJSON(origPackageJson, platform)
}

// findRuntimeFramework extracts the runtime framework from ServiceRequirements
func findRuntimeFrameworkFromServiceRequirements(serviceRequirements []analyzer.ServiceRequirement) string {
	// Prioritize actual web frameworks over WSGI servers
	// First pass: look for Django, Flask, FastAPI, etc.
	preferredFrameworks := []string{"django", "flask", "fastapi", "express", "nextjs", "remix", "svelte"}
	for _, sr := range serviceRequirements {
		if sr.Type == "framework" {
			for _, preferred := range preferredFrameworks {
				if sr.Provider == preferred {
					return sr.Provider
				}
			}
		}
	}

	// Second pass: return any framework (including wsgi, asgi, etc.)
	for _, sr := range serviceRequirements {
		if sr.Type == "framework" {
			return sr.Provider
		}
	}
	return ""
}

// updateJavaScriptConfig handles both framework config and package.json updates for JavaScript projects
func (a *Activities) updateJavaScriptConfig(_ context.Context, plan DeployPlan) (JavaScriptConfigResult, error) {
	projectPath := plan.Source
	result := JavaScriptConfigResult{}
	runtimeFramework := findRuntimeFrameworkFromServiceRequirements(plan.Spec.ServiceRequirements)

	a.uiWriter.SendStatus("configuring", "Configuring JavaScript project...")

	handler := frameworkRegistry.GetHandler(runtimeFramework)

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

			if err := ensureInGitignore(projectPath, ".prod"); err != nil {
				a.uiWriter.SendStatusComplete("configuring", "❌ Failed to update .gitignore")
				return JavaScriptConfigResult{}, errors.Errorf("failed to update .gitignore: %w", err)
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

	// Handle framework-specific config if we have a handler
	if handler != nil {
		configDiff, configPath, err := handler.HandleConfig(projectPath, plan.Platform)
		if err != nil {
			return JavaScriptConfigResult{}, err
		}

		result.ConfigDiff = configDiff
		result.ConfigPath = configPath

		// Handle any platform-specific files (like .npmrc for Remix)
		if err := handler.HandlePlatformSpecificFiles(projectPath, plan.Platform); err != nil {
			// Don't fail the whole process for file operations, just log
			a.uiWriter.SendStatus("configuring", fmt.Sprintf("⚠️ Could not handle platform-specific files: %v", err))
		}
	}

	// Summary message
	if result.PackageJsonUpdated || len(result.ConfigDiff) > 0 {
		a.uiWriter.SendStatusComplete("configuring", "✅ JavaScript project configuration completed")
	} else {
		a.uiWriter.SendStatusComplete("configuring", "✅ No configuration changes needed")
	}

	return result, nil
}

func (a *Activities) prepareDeployment(_ context.Context, plan DeployPlan) (DeployPlan, error) {
	runtimeFramework := findRuntimeFrameworkFromServiceRequirements(plan.Spec.ServiceRequirements)

	slog.Info("prepareDeployment framework detection", "runtimeFramework", runtimeFramework)

	a.uiWriter.SendStatus("configuring", fmt.Sprintf("Configuring %s deployment...", runtimeFramework))

	handler := frameworkRegistry.GetHandler(runtimeFramework)
	if handler == nil {
		slog.Debug("No handler found for framework", "framework", runtimeFramework)
		a.uiWriter.SendStatusComplete("configuring", "✅ No framework-specific deployment config needed")
		return plan, nil
	}

	plan = handler.PrepareDeployment(plan)

	a.uiWriter.SendStatusComplete("configuring", fmt.Sprintf("✅ %s deployment configuration complete", runtimeFramework))
	return plan, nil
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

// restoreFromBackup restores config files from the latest backup based on framework
func (a *Activities) restoreFromBackup(ctx context.Context, plan DeployPlan) ([]DiffLine, error) {
	runtimeFramework := findRuntimeFrameworkFromServiceRequirements(plan.Spec.ServiceRequirements)

	handler := frameworkRegistry.GetHandler(runtimeFramework)
	if handler == nil {
		return nil, errors.Errorf("no backup restoration available for framework: %s", runtimeFramework)
	}

	return handler.RestoreConfigFromBackup(ctx, plan)
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
