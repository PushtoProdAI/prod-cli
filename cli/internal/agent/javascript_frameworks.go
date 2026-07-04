package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-errors/errors"
	"github.com/pmezard/go-difflib/difflib"
	"github.com/pushtoprodai/prod-cli/internal/deployment"
)

// generateConfigDiff generates a diff between original and updated config (JS-specific helper)
func (b *BaseFrameworkHandler) generateConfigDiff(origContent, updatedContent []byte, filename string) ([]DiffLine, error) {
	diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(string(origContent)),
		B:        difflib.SplitLines(string(updatedContent)),
		FromFile: fmt.Sprintf("%s (before)", filename),
		ToFile:   fmt.Sprintf("%s (after)", filename),
		Context:  3,
	})
	if err != nil {
		return nil, errors.Errorf("failed to generate %s diff: %w", filename, err)
	}
	return parseDiffString(diff), nil
}

// RemixHandler handles Remix-specific operations
type RemixHandler struct {
	BaseFrameworkHandler
}

func (h *RemixHandler) GetName() string {
	return "Remix"
}

func (h *RemixHandler) GetConfigFilenames() []string {
	return []string{"vite.config.ts", "vite.config.js"}
}

func (h *RemixHandler) PatchPackageJSON(origPackageJson []byte, platform Platform) ([]byte, bool, error) {
	switch platform {
	case Render, FlyIO, Heroku, AWS:
		// For Node.js platforms, ensure @remix-run/serve is available as a dependency (not devDependency)
		updatedPackageJson, err := patchPackageJSONForRemixServe(origPackageJson, "@remix-run/serve", "^2.0.0")
		if err != nil {
			return nil, false, err
		}
		// Remove platform-specific adapters
		updatedPackageJson, err = removeRemixAdapter(updatedPackageJson, "@netlify/remix-adapter")
		if err != nil {
			return nil, false, err
		}
		updatedPackageJson, err = removeRemixAdapter(updatedPackageJson, "@vercel/remix")
		if err != nil {
			return nil, false, err
		}
		// Check if anything changed
		changed := !bytes.Equal(origPackageJson, updatedPackageJson)
		return updatedPackageJson, changed, nil
	case Netlify:
		updatedPackageJson, err := patchPackageJSONForRemix(origPackageJson, "@netlify/remix-adapter", "^2.0.0")
		if err != nil {
			return nil, false, err
		}
		// Remove other platform adapters
		updatedPackageJson, err = removeRemixAdapter(updatedPackageJson, "@remix-run/serve")
		if err != nil {
			return nil, false, err
		}
		updatedPackageJson, err = removeRemixAdapter(updatedPackageJson, "@vercel/remix")
		if err != nil {
			return nil, false, err
		}
		// Check if anything changed
		changed := !bytes.Equal(origPackageJson, updatedPackageJson)
		return updatedPackageJson, changed, nil
	case Vercel:
		// For Vercel, install @vercel/remix with a known stable version
		// We'll use --legacy-peer-deps during npm install to handle peer dependency conflicts
		updatedPackageJson, err := patchPackageJSONForRemix(origPackageJson, "@vercel/remix", "^2.0.0")
		if err != nil {
			return nil, false, err
		}
		// Remove other platform adapters
		updatedPackageJson, err = removeRemixAdapter(updatedPackageJson, "@netlify/remix-adapter")
		if err != nil {
			return nil, false, err
		}
		updatedPackageJson, err = removeRemixAdapter(updatedPackageJson, "@remix-run/serve")
		if err != nil {
			return nil, false, err
		}
		// Check if anything changed
		changed := !bytes.Equal(origPackageJson, updatedPackageJson)
		return updatedPackageJson, changed, nil
	default:
		// For other platforms, remove all platform-specific adapters
		updatedPackageJson := origPackageJson
		var err error

		// Remove all platform-specific adapters
		updatedPackageJson, err = removeRemixAdapter(updatedPackageJson, "@netlify/remix-adapter")
		if err != nil {
			return nil, false, err
		}
		updatedPackageJson, err = removeRemixAdapter(updatedPackageJson, "@vercel/remix")
		if err != nil {
			return nil, false, err
		}

		changed := !bytes.Equal(origPackageJson, updatedPackageJson)
		return updatedPackageJson, changed, nil
	}
}

func (h *RemixHandler) HandleConfig(projectPath string, platform Platform) ([]DiffLine, string, error) {
	// Find vite config file (prefer TS, fallback to JS)
	configPath, err := h.findConfigFile(projectPath, h.GetConfigFilenames())
	if err != nil {
		return nil, "", errors.Errorf("no vite.config.{ts,js} found in %s", projectPath)
	}

	// Read original config
	origConfig, err := os.ReadFile(configPath)
	if err != nil {
		return nil, "", errors.Errorf("failed to read %s: %w", configPath, err)
	}

	// Patch the config based on platform
	var updatedConfig []byte
	switch platform {
	case Netlify:
		// Remove Vercel preset first, then add Netlify plugin
		updatedConfig = removeVercelPresetFromRemixConfig(origConfig)
		updatedConfig = patchRemixViteConfigForNetlify(updatedConfig)
	case Vercel:
		// Remove Netlify plugin first, then add Vercel preset
		updatedConfig = removeNetlifyPluginFromRemixConfig(origConfig)
		updatedConfig = PatchRemixViteConfigForVercel(updatedConfig)
	default:
		// For other platforms (Render, FlyIO), remove both platform-specific configs
		updatedConfig = removeNetlifyPluginFromRemixConfig(origConfig)
		updatedConfig = removeVercelPresetFromRemixConfig(updatedConfig)
	}

	// Create backup
	configFilename := filepath.Base(configPath)
	if err := h.createBackup(projectPath, configFilename, origConfig); err != nil {
		return nil, "", err
	}

	// Write updated config
	if err := os.WriteFile(configPath, updatedConfig, 0o644); err != nil {
		return nil, "", errors.Errorf("failed to update %s: %w", configPath, err)
	}

	// Generate diff
	diff, err := h.generateConfigDiff(origConfig, updatedConfig, "vite.config")
	if err != nil {
		return nil, "", err
	}

	return diff, configPath, nil
}

func (h *RemixHandler) HandlePlatformSpecificFiles(projectPath string, platform Platform) error {
	npmrcPath := filepath.Join(projectPath, ".npmrc")

	if platform == Vercel {
		// Create .npmrc to handle peer dependencies during deployment
		npmrcContent := "legacy-peer-deps=true\n"
		return os.WriteFile(npmrcPath, []byte(npmrcContent), 0o644)
	} else {
		// Remove .npmrc if it exists (for non-Vercel platforms)
		if _, err := os.Stat(npmrcPath); err == nil {
			return os.Remove(npmrcPath)
		}
	}
	return nil
}

func (h *RemixHandler) RestoreConfigFromBackup(ctx context.Context, plan DeployPlan) ([]DiffLine, error) {
	projectPath := plan.Source
	prodDir := filepath.Join(projectPath, ".prod")

	// Check if .prod directory exists
	if _, err := os.Stat(prodDir); err != nil {
		return nil, errors.Errorf("no .prod backup directory found in %s", projectPath)
	}

	// Find vite config file that should be restored (prefer TS, fallback to JS)
	configPath, err := h.findConfigFile(projectPath, h.GetConfigFilenames())
	if err != nil {
		return nil, errors.Errorf("no vite.config.{ts,js} found to restore in %s", projectPath)
	}

	configFilename := filepath.Base(configPath)

	// Find the latest backup file
	backupPath, err := findLatestBackup(prodDir, configFilename)
	if err != nil {
		return nil, errors.Errorf("failed to find backup for %s: %w", configFilename, err)
	}

	// Read current config for diff
	currentConfig, err := os.ReadFile(configPath)
	if err != nil {
		return nil, errors.Errorf("failed to read current config %s: %w", configPath, err)
	}

	// Read backup
	backupConfig, err := os.ReadFile(backupPath)
	if err != nil {
		return nil, errors.Errorf("failed to read backup %s: %w", backupPath, err)
	}

	// Restore the backup
	if err := os.WriteFile(configPath, backupConfig, 0o644); err != nil {
		return nil, errors.Errorf("failed to restore config %s: %w", configPath, err)
	}

	// Generate diff showing the restoration
	diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(string(currentConfig)),
		B:        difflib.SplitLines(string(backupConfig)),
		FromFile: "vite.config (current)",
		ToFile:   "vite.config (restored)",
		Context:  3,
	})
	if err != nil {
		return nil, errors.Errorf("failed to generate diff: %w", err)
	}

	return parseDiffString(diff), nil
}

func (h *RemixHandler) PrepareDeployment(plan DeployPlan) DeployPlan {
	// Apply platform-specific deployment configuration for Remix
	switch plan.Platform {
	case Render, FlyIO, Heroku, AWS:
		plan.Spec.StartCommand = "npx remix-serve ./build/server/index.js"
	}
	return plan
}

// SvelteKitHandler handles SvelteKit-specific operations
type SvelteKitHandler struct {
	BaseFrameworkHandler
}

func (h *SvelteKitHandler) GetName() string {
	return "SvelteKit"
}

func (h *SvelteKitHandler) GetConfigFilenames() []string {
	return []string{"svelte.config.ts", "svelte.config.js"}
}

func (h *SvelteKitHandler) PatchPackageJSON(origPackageJson []byte, platform Platform) ([]byte, bool, error) {
	// For platforms that need Svelte adapters
	switch platform {
	case Render, FlyIO, Heroku, AWS:
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

func (h *SvelteKitHandler) HandleConfig(projectPath string, platform Platform) ([]DiffLine, string, error) {
	// Find svelte config file (prefer TS, fallback to JS)
	configPath, err := h.findConfigFile(projectPath, h.GetConfigFilenames())
	if err != nil {
		return nil, "", nil // No Svelte config found, return empty results
	}

	// Determine which adapter to use based on platform
	var newAdapter string
	switch platform {
	case Render, FlyIO, Heroku, AWS:
		newAdapter = "@sveltejs/adapter-node"
	case Netlify:
		newAdapter = "@sveltejs/adapter-netlify"
	case Vercel:
		newAdapter = "@sveltejs/adapter-vercel"
	default:
		return nil, "", errors.Errorf("unsupported platform for Svelte: %s", platform)
	}

	// Read original config
	origConfig, err := os.ReadFile(configPath)
	if err != nil {
		return nil, "", errors.Errorf("failed to read %s: %w", configPath, err)
	}

	// Patch the config
	updatedConfig := patchSvelteConfig(origConfig, newAdapter)

	// Create backup
	configFilename := filepath.Base(configPath)
	if err := h.createBackup(projectPath, configFilename, origConfig); err != nil {
		return nil, "", err
	}

	// Write updated config
	if err := os.WriteFile(configPath, updatedConfig, 0o644); err != nil {
		return nil, "", errors.Errorf("failed to write updated config to %s: %w", configPath, err)
	}

	// Generate diff
	diff, err := h.generateConfigDiff(origConfig, updatedConfig, "svelte.config")
	if err != nil {
		return nil, "", err
	}

	return diff, configPath, nil
}

func (h *SvelteKitHandler) HandlePlatformSpecificFiles(projectPath string, platform Platform) error {
	// SvelteKit doesn't need any platform-specific file handling
	return nil
}

func (h *SvelteKitHandler) RestoreConfigFromBackup(ctx context.Context, plan DeployPlan) ([]DiffLine, error) {
	projectPath := plan.Source
	prodDir := filepath.Join(projectPath, ".prod")

	// Check if .prod directory exists
	if _, err := os.Stat(prodDir); err != nil {
		return nil, errors.Errorf("no .prod backup directory found in %s", projectPath)
	}

	// Find svelte config file that should be restored (prefer TS, fallback to JS)
	configPath, err := h.findConfigFile(projectPath, h.GetConfigFilenames())
	if err != nil {
		return nil, errors.Errorf("no svelte.config.{ts,js} found to restore in %s", projectPath)
	}

	configFilename := filepath.Base(configPath)

	// Find the latest backup file
	backupPath, err := findLatestBackup(prodDir, configFilename)
	if err != nil {
		return nil, errors.Errorf("failed to find backup for %s: %w", configFilename, err)
	}

	// Read current config for diff
	currentConfig, err := os.ReadFile(configPath)
	if err != nil {
		return nil, errors.Errorf("failed to read current config %s: %w", configPath, err)
	}

	// Read backup
	backupConfig, err := os.ReadFile(backupPath)
	if err != nil {
		return nil, errors.Errorf("failed to read backup %s: %w", backupPath, err)
	}

	// Restore from backup
	if err := os.WriteFile(configPath, backupConfig, 0o644); err != nil {
		return nil, errors.Errorf("failed to restore config from backup: %w", err)
	}

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

func (h *SvelteKitHandler) PrepareDeployment(plan DeployPlan) DeployPlan {
	// Apply platform-specific deployment configuration for SvelteKit
	// For Node.js platforms (Render, Fly, Heroku), SvelteKit with adapter-node outputs to build/
	switch plan.Platform {
	case Render, FlyIO, Heroku, AWS:
		plan.Spec.StartCommand = "node build"
	}
	// For Netlify and Vercel, the platform handles the runtime (no start command needed)
	// For adapter-static, no start command needed (it's truly static)

	return plan
}

// NuxtHandler handles Nuxt-specific operations
type NuxtHandler struct {
	BaseFrameworkHandler
}

func (h *NuxtHandler) GetName() string {
	return "Nuxt"
}

func (h *NuxtHandler) GetConfigFilenames() []string {
	return []string{"nuxt.config.ts", "nuxt.config.js"}
}

func (h *NuxtHandler) PatchPackageJSON(origPackageJson []byte, platform Platform) ([]byte, bool, error) {
	// Nuxt doesn't need package.json modifications for different platforms
	return origPackageJson, false, nil
}

func (h *NuxtHandler) HandleConfig(projectPath string, platform Platform) ([]DiffLine, string, error) {
	// Nuxt doesn't need config file modifications for different platforms
	return nil, "", nil
}

func (h *NuxtHandler) RestoreConfigFromBackup(ctx context.Context, plan DeployPlan) ([]DiffLine, error) {
	// Nuxt doesn't modify config files, so no restoration needed
	return nil, nil
}

func (h *NuxtHandler) HandlePlatformSpecificFiles(projectPath string, platform Platform) error {
	// Nuxt doesn't need platform-specific file handling
	return nil
}

func (h *NuxtHandler) PrepareDeployment(plan DeployPlan) DeployPlan {
	// Apply platform-specific deployment configuration for Nuxt
	switch plan.Platform {
	case Render, FlyIO, Heroku, AWS:
		plan.Spec.StartCommand = "node .output/server/index.mjs"
		plan.CollectedEnvVars = append(plan.CollectedEnvVars, deployment.EnvVar{Name: "NITRO_PRESET", Value: "node-server"})
	case Netlify:
		plan.CollectedEnvVars = append(plan.CollectedEnvVars, deployment.EnvVar{Name: "SERVER_PRESET", Value: "netlify_edge"})
		plan.Spec.BuildOutput.Path = "dist"
	case Vercel:
		plan.CollectedEnvVars = append(plan.CollectedEnvVars, deployment.EnvVar{Name: "SERVER_PRESET", Value: "vercel_edge"})
	}
	return plan
}

// removeTanStackStartNetlifyPlugin removes the Netlify plugin from TanStack Start config
func removeTanStackStartNetlifyPlugin(config []byte) []byte {
	configStr := string(config)

	// Remove the netlify import
	importRegex := regexp.MustCompile(`import\s+netlify\s+from\s+["']@netlify/vite-plugin-tanstack-start["'];?\s*\n?`)
	configStr = importRegex.ReplaceAllString(configStr, "")

	// Remove netlify() from plugins array
	// Try to match: comma + whitespace + netlify() (middle/end of array)
	pluginRegex1 := regexp.MustCompile(`\s*,\s*netlify\(\)`)
	configStr = pluginRegex1.ReplaceAllString(configStr, "")

	// Try to match: netlify() + comma + whitespace (beginning/middle of array)
	pluginRegex2 := regexp.MustCompile(`netlify\(\)\s*,\s*`)
	configStr = pluginRegex2.ReplaceAllString(configStr, "")

	// Try to match: just netlify() if it's alone (edge case)
	pluginRegex3 := regexp.MustCompile(`\s*netlify\(\)\s*`)
	configStr = pluginRegex3.ReplaceAllString(configStr, "")

	return []byte(configStr)
}

// removeTanStackStartNitroPlugin removes the Nitro plugin from TanStack Start config
func removeTanStackStartNitroPlugin(config []byte) []byte {
	configStr := string(config)

	// Remove the nitro import
	importRegex := regexp.MustCompile(`import\s+{\s*nitro\s*}\s+from\s+["']nitro/vite["'];?\s*\n?`)
	configStr = importRegex.ReplaceAllString(configStr, "")

	// Remove nitro() with any config from plugins array
	// Try to match: comma + whitespace + nitro(...) (middle/end of array)
	pluginRegex1 := regexp.MustCompile(`\s*,\s*nitro\([^)]*\)`)
	configStr = pluginRegex1.ReplaceAllString(configStr, "")

	// Try to match: nitro(...) + comma + whitespace (beginning/middle of array)
	pluginRegex2 := regexp.MustCompile(`nitro\([^)]*\)\s*,\s*`)
	configStr = pluginRegex2.ReplaceAllString(configStr, "")

	// Try to match: just nitro(...) if it's alone (edge case)
	pluginRegex3 := regexp.MustCompile(`\s*nitro\([^)]*\)\s*`)
	configStr = pluginRegex3.ReplaceAllString(configStr, "")

	return []byte(configStr)
}

// patchTanStackStartConfigForNitro adds the Nitro plugin with specified preset to TanStack Start config
func patchTanStackStartConfigForNitro(config []byte, isAppConfig bool, preset string) []byte {
	configStr := string(config)

	// Check if nitro plugin is already imported and added
	if strings.Contains(configStr, "nitro") && strings.Contains(configStr, "nitro/vite") {
		return config // Already configured
	}

	// Add import for nitro plugin if not present
	if !strings.Contains(configStr, `import { nitro } from "nitro/vite"`) {
		// Find existing imports and add the nitro import
		importRegex := regexp.MustCompile(`(import\s+.*from\s+["'].*["'];?\s*\n)`)
		matches := importRegex.FindAllStringIndex(configStr, -1)

		nitroImport := `import { nitro } from "nitro/vite";` + "\n"

		if len(matches) > 0 {
			// Insert after the last import
			lastImportEnd := matches[len(matches)-1][1]
			configStr = configStr[:lastImportEnd] + nitroImport + configStr[lastImportEnd:]
		} else {
			// No imports found, add at the beginning
			configStr = nitroImport + configStr
		}
	}

	// Add nitro() to plugins array if not present
	if !strings.Contains(configStr, "nitro(") {
		if isAppConfig {
			// For app.config, we need to add to vite: { plugins: [...] }
			vitePluginsRegex := regexp.MustCompile(`vite:\s*\{[^}]*plugins:\s*\[\s*([^\]]*)\s*\]`)
			if vitePluginsRegex.MatchString(configStr) {
				// Add nitro to existing vite.plugins array (after netlify if present, or at the beginning)
				configStr = vitePluginsRegex.ReplaceAllStringFunc(configStr, func(match string) string {
					pluginsStart := strings.Index(match, "plugins: [")
					if pluginsStart != -1 {
						afterOpenBracket := pluginsStart + len("plugins: [")
						// Insert nitro() with specified preset
						insertion := fmt.Sprintf("\n      nitro({ config: { preset: '%s' } }),", preset)
						return match[:afterOpenBracket] + insertion + match[afterOpenBracket:]
					}
					return match
				})
			} else {
				// Check if vite object exists but without plugins
				viteRegex := regexp.MustCompile(`vite:\s*\{`)
				if viteRegex.MatchString(configStr) {
					// Add plugins array to existing vite object
					nitroPlugin := fmt.Sprintf("nitro({ config: { preset: '%s' } })", preset)
					configStr = viteRegex.ReplaceAllString(configStr, fmt.Sprintf(`vite: {
    plugins: [
      %s,
    ],`, nitroPlugin))
				} else {
					// No vite object, add it to the config
					defineConfigRegex := regexp.MustCompile(`export\s+default\s+defineConfig\s*\(\s*\{`)
					if defineConfigRegex.MatchString(configStr) {
						nitroPlugin := fmt.Sprintf("nitro({ config: { preset: '%s' } })", preset)
						configStr = defineConfigRegex.ReplaceAllString(configStr, fmt.Sprintf(`export default defineConfig({
  vite: {
    plugins: [
      %s,
    ],
  },`, nitroPlugin))
					}
				}
			}
		} else {
			// For vite.config, add to the main plugins array
			pluginsStart := strings.Index(configStr, "plugins:")
			if pluginsStart != -1 {
				// Find the opening bracket after "plugins:"
				bracketStart := strings.Index(configStr[pluginsStart:], "[")
				if bracketStart != -1 {
					bracketStart += pluginsStart

					// Count brackets to find the matching closing bracket
					bracketCount := 0
					bracketEnd := -1
					inString := false
					var stringChar rune

					for i := bracketStart; i < len(configStr); i++ {
						ch := rune(configStr[i])

						// Handle string literals
						if ch == '"' || ch == '\'' || ch == '`' {
							if !inString {
								inString = true
								stringChar = ch
							} else if ch == stringChar && (i == 0 || configStr[i-1] != '\\') {
								inString = false
							}
						}

						if !inString {
							if ch == '[' {
								bracketCount++
							} else if ch == ']' {
								bracketCount--
								if bracketCount == 0 {
									bracketEnd = i
									break
								}
							}
						}
					}

					if bracketEnd != -1 {
						// Insert nitro() at the beginning of the plugins array (after tanstackStart)
						afterOpenBracket := bracketStart + 1
						// Skip whitespace after opening bracket
						for afterOpenBracket < len(configStr) && (configStr[afterOpenBracket] == ' ' || configStr[afterOpenBracket] == '\n' || configStr[afterOpenBracket] == '\t') {
							afterOpenBracket++
						}

						// Look for tanstackStart() and insert after it
						tanstackPos := strings.Index(configStr[afterOpenBracket:bracketEnd], "tanstackStart()")
						if tanstackPos != -1 {
							// Find the comma after tanstackStart()
							commaPos := strings.Index(configStr[afterOpenBracket+tanstackPos:bracketEnd], ",")
							if commaPos != -1 {
								insertPos := afterOpenBracket + tanstackPos + commaPos + 1
								nitroPlugin := fmt.Sprintf("\n    nitro({ config: { preset: '%s' } }),", preset)
								newConfig := configStr[:insertPos] + nitroPlugin + configStr[insertPos:]
								return []byte(newConfig)
							}
						}

						// If no tanstackStart found, insert at the beginning
						nitroPlugin := fmt.Sprintf("\n    nitro({ config: { preset: '%s' } }),\n    ", preset)
						newConfig := configStr[:afterOpenBracket] + nitroPlugin + configStr[afterOpenBracket:]
						return []byte(newConfig)
					}
				}
			} else {
				// No plugins array found, need to add one
				defineConfigRegex := regexp.MustCompile(`export\s+default\s+defineConfig\s*\(\s*\{`)
				if defineConfigRegex.MatchString(configStr) {
					nitroPlugin := fmt.Sprintf("nitro({ config: { preset: '%s' } })", preset)
					configStr = defineConfigRegex.ReplaceAllString(configStr, fmt.Sprintf(`export default defineConfig({
  plugins: [
    %s,
  ],`, nitroPlugin))
				}
			}
		}
	}

	return []byte(configStr)
}

// TanStackStartHandler handles TanStack Start-specific operations
type TanStackStartHandler struct {
	BaseFrameworkHandler
}

func (h *TanStackStartHandler) GetName() string {
	return "TanStack Start"
}

func (h *TanStackStartHandler) GetConfigFilenames() []string {
	return []string{"app.config.ts", "app.config.js", "vite.config.ts", "vite.config.js"}
}

func (h *TanStackStartHandler) PatchPackageJSON(origPackageJson []byte, platform Platform) ([]byte, bool, error) {
	switch platform {
	case Netlify:
		updatedPackageJson, err := patchPackageJSON(origPackageJson, "@netlify/vite-plugin-tanstack-start", "^1.0.0")
		if err != nil {
			return nil, false, err
		}
		// Check if anything changed
		changed := !bytes.Equal(origPackageJson, updatedPackageJson)
		return updatedPackageJson, changed, nil
	case Vercel, Render, FlyIO, Heroku, AWS:
		// For Vercel and Node.js platforms, we'll use Nitro v3
		// We need to add nitro as a dependency
		updatedPackageJson, err := patchPackageJSON(origPackageJson, "nitro", "^3.0.0")
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

func (h *TanStackStartHandler) HandleConfig(projectPath string, platform Platform) ([]DiffLine, string, error) {
	// Find config file (prefer app.config.ts, then app.config.js, then vite.config.ts, then vite.config.js)
	configPath, err := h.findConfigFile(projectPath, h.GetConfigFilenames())
	if err != nil {
		return nil, "", nil // No config found, return empty results
	}

	// Only handle platforms with specific config requirements
	if platform != Netlify && platform != Vercel && platform != Render && platform != FlyIO && platform != Heroku && platform != AWS {
		return nil, "", nil
	}

	// Read original config
	origConfig, err := os.ReadFile(configPath)
	if err != nil {
		return nil, "", errors.Errorf("failed to read %s: %w", configPath, err)
	}

	// Determine config type and patch accordingly
	configFilename := filepath.Base(configPath)
	var updatedConfig []byte
	var configName string
	isAppConfig := strings.HasPrefix(configFilename, "app.config")

	if isAppConfig {
		configName = "app.config"
	} else {
		configName = "vite.config"
	}

	// Apply platform-specific patches
	// First, clean up any existing platform-specific plugins
	cleanConfig := origConfig
	switch platform {
	case Netlify:
		// Remove Nitro plugin if switching to Netlify
		cleanConfig = removeTanStackStartNitroPlugin(cleanConfig)
		if isAppConfig {
			updatedConfig = patchTanStackStartAppConfigForNetlify(cleanConfig)
		} else {
			updatedConfig = patchTanStackStartViteConfigForNetlify(cleanConfig)
		}
	case Vercel, Render, FlyIO, Heroku, AWS:
		// Remove Netlify plugin if switching to Nitro-based platforms
		cleanConfig = removeTanStackStartNetlifyPlugin(cleanConfig)
		// Also remove any existing Nitro plugin to avoid duplicates
		cleanConfig = removeTanStackStartNitroPlugin(cleanConfig)

		if platform == Vercel {
			updatedConfig = patchTanStackStartConfigForNitro(cleanConfig, isAppConfig, "vercel")
		} else {
			updatedConfig = patchTanStackStartConfigForNitro(cleanConfig, isAppConfig, "node-server")
		}
	}

	// Create backup
	if err := h.createBackup(projectPath, configFilename, origConfig); err != nil {
		return nil, "", err
	}

	// Write updated config
	if err := os.WriteFile(configPath, updatedConfig, 0o644); err != nil {
		return nil, "", errors.Errorf("failed to write updated config to %s: %w", configPath, err)
	}

	// Generate diff
	diff, err := h.generateConfigDiff(origConfig, updatedConfig, configName)
	if err != nil {
		return nil, "", err
	}

	return diff, configPath, nil
}

func (h *TanStackStartHandler) HandlePlatformSpecificFiles(projectPath string, platform Platform) error {
	// TanStack Start doesn't need any platform-specific file handling
	return nil
}

func (h *TanStackStartHandler) RestoreConfigFromBackup(ctx context.Context, plan DeployPlan) ([]DiffLine, error) {
	projectPath := plan.Source
	prodDir := filepath.Join(projectPath, ".prod")

	// Check if .prod directory exists
	if _, err := os.Stat(prodDir); err != nil {
		return nil, errors.Errorf("no .prod backup directory found in %s", projectPath)
	}

	// Find config file that should be restored (prefer app.config, then vite.config)
	configPath, err := h.findConfigFile(projectPath, h.GetConfigFilenames())
	if err != nil {
		return nil, errors.Errorf("no app.config.{ts,js} or vite.config.{ts,js} found to restore in %s", projectPath)
	}

	configFilename := filepath.Base(configPath)

	// Find the latest backup file
	backupPath, err := findLatestBackup(prodDir, configFilename)
	if err != nil {
		return nil, errors.Errorf("failed to find backup for %s: %w", configFilename, err)
	}

	// Read current config for diff
	currentConfig, err := os.ReadFile(configPath)
	if err != nil {
		return nil, errors.Errorf("failed to read current config %s: %w", configPath, err)
	}

	// Read backup
	backupConfig, err := os.ReadFile(backupPath)
	if err != nil {
		return nil, errors.Errorf("failed to read backup %s: %w", backupPath, err)
	}

	// Restore from backup
	if err := os.WriteFile(configPath, backupConfig, 0o644); err != nil {
		return nil, errors.Errorf("failed to restore config from backup: %w", err)
	}

	// Determine config name for diff
	configName := "config"
	if strings.HasPrefix(configFilename, "app.config") {
		configName = "app.config"
	} else if strings.HasPrefix(configFilename, "vite.config") {
		configName = "vite.config"
	}

	// Generate diff showing the restoration
	diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(string(currentConfig)),
		B:        difflib.SplitLines(string(backupConfig)),
		FromFile: fmt.Sprintf("%s (current)", configName),
		ToFile:   fmt.Sprintf("%s (restored)", configName),
		Context:  3,
	})
	if err != nil {
		return nil, errors.Errorf("failed to generate diff: %w", err)
	}

	return parseDiffString(diff), nil
}

func (h *TanStackStartHandler) PrepareDeployment(plan DeployPlan) DeployPlan {
	// Apply platform-specific deployment configuration for TanStack Start
	switch plan.Platform {
	case Render, FlyIO, Heroku, AWS:
		// For Node.js platforms, set the start command to run the Nitro output
		plan.Spec.StartCommand = "node .output/server/index.mjs"
	}
	return plan
}

// patchRemixViteConfigForNetlify adds the Netlify plugin to Remix vite config
func patchRemixViteConfigForNetlify(config []byte) []byte {
	configStr := string(config)

	// Check if netlifyPlugin is already imported and added
	if strings.Contains(configStr, "netlifyPlugin") && strings.Contains(configStr, "@netlify/remix-adapter/plugin") {
		return config // Already configured
	}

	// Add import for netlifyPlugin if not present
	if !strings.Contains(configStr, `import { netlifyPlugin } from "@netlify/rem-ixadapter/plugin"`) {
		// Find existing imports and add the netlify import
		importRegex := regexp.MustCompile(`(import\s+.*from\s+["'].*["'];?\s*\n)`)
		matches := importRegex.FindAllStringIndex(configStr, -1)

		netlifyImport := `import { netlifyPlugin } from "@netlify/remix-adapter/plugin";` + "\n"

		if len(matches) > 0 {
			// Insert after the last import
			lastImportEnd := matches[len(matches)-1][1]
			configStr = configStr[:lastImportEnd] + netlifyImport + configStr[lastImportEnd:]
		} else {
			// No imports found, add at the beginning
			configStr = netlifyImport + configStr
		}
	}

	// Add netlifyPlugin() to plugins array if not present
	if !strings.Contains(configStr, "netlifyPlugin()") {
		// Find plugins array and add netlifyPlugin
		pluginsRegex := regexp.MustCompile(`plugins:\s*\[\s*([^\]]*)\s*\]`)
		if pluginsRegex.MatchString(configStr) {
			// Replace the plugins array to include netlifyPlugin
			configStr = pluginsRegex.ReplaceAllStringFunc(configStr, func(match string) string {
				// Extract the content inside the plugins array
				submatch := pluginsRegex.FindStringSubmatch(match)
				if len(submatch) > 1 {
					existingPlugins := strings.TrimSpace(submatch[1])
					if existingPlugins == "" {
						return "plugins: [\n    remix(),\n    tsconfigPaths(),\n    netlifyPlugin()\n  ]"
					} else {
						// Add netlifyPlugin to existing plugins
						if strings.HasSuffix(existingPlugins, ",") {
							return fmt.Sprintf("plugins: [\n    %s\n    netlifyPlugin()\n  ]", existingPlugins)
						} else {
							return fmt.Sprintf("plugins: [\n    %s,\n    netlifyPlugin()\n  ]", existingPlugins)
						}
					}
				}
				return match
			})
		} else {
			// No plugins array found, need to add one
			defineConfigRegex := regexp.MustCompile(`export\s+default\s+defineConfig\s*\(\s*\{([^}]*)\}\s*\)`)
			if defineConfigRegex.MatchString(configStr) {
				configStr = defineConfigRegex.ReplaceAllStringFunc(configStr, func(match string) string {
					return strings.Replace(match, "{", "{\n  plugins: [\n    remix(),\n    tsconfigPaths(),\n    netlifyPlugin()\n  ],", 1)
				})
			}
		}
	}

	return []byte(configStr)
}

// removeNetlifyPluginFromRemixConfig removes Netlify plugin from Remix vite config
func removeNetlifyPluginFromRemixConfig(config []byte) []byte {
	configStr := string(config)

	// Remove the netlify import
	importRegex := regexp.MustCompile(`import\s+{\s*netlifyPlugin\s*}\s+from\s+["']@netlify/remix-adapter/plugin["'];?\s*\n?`)
	configStr = importRegex.ReplaceAllString(configStr, "")

	// Remove netlifyPlugin() from plugins array
	pluginRegex := regexp.MustCompile(`,?\s*netlifyPlugin\(\)\s*,?`)
	configStr = pluginRegex.ReplaceAllString(configStr, "")

	// Clean up any trailing commas in plugins array
	configStr = regexp.MustCompile(`,\s*\]`).ReplaceAllString(configStr, "\n  ]")

	return []byte(configStr)
}

// patchRemixViteConfigForVercel adds the Vercel preset to Remix vite config
// Note: @vercel/remix is provided by Vercel at build time, not as a local dependency
func PatchRemixViteConfigForVercel(config []byte) []byte {
	configStr := string(config)

	// Check if vercelPreset is already fully configured (both import and usage)
	if strings.Contains(configStr, "presets: [vercelPreset()]") {
		return config // Already configured
	}

	// Add imports for Vercel preset and installGlobals if not present
	if !strings.Contains(configStr, `import { vercelPreset } from "@vercel/remix/vite"`) {
		// Find existing imports and add the vercel import
		importRegex := regexp.MustCompile(`(import\s+.*from\s+["'].*["'];?\s*\n)`)
		matches := importRegex.FindAllStringIndex(configStr, -1)

		// Note: This import will be resolved by Vercel at build time
		vercelImport := `import { vercelPreset } from "@vercel/remix/vite";` + "\n"

		if len(matches) > 0 {
			// Insert after the last import
			lastImportEnd := matches[len(matches)-1][1]
			configStr = configStr[:lastImportEnd] + vercelImport + configStr[lastImportEnd:]
		} else {
			// No imports found, add at the beginning
			configStr = vercelImport + configStr
		}
	}

	// Add installGlobals import and call if not present
	if !strings.Contains(configStr, `import { installGlobals } from "@remix-run/node"`) {
		// Find the vercel import we just added or existing imports
		importRegex := regexp.MustCompile(`(import\s+.*from\s+["'].*["'];?\s*\n)`)
		matches := importRegex.FindAllStringIndex(configStr, -1)

		installGlobalsImport := `import { installGlobals } from "@remix-run/node";` + "\n"

		if len(matches) > 0 {
			// Insert after the last import
			lastImportEnd := matches[len(matches)-1][1]
			configStr = configStr[:lastImportEnd] + installGlobalsImport + configStr[lastImportEnd:]
		} else {
			// Add at the beginning
			configStr = installGlobalsImport + configStr
		}
	}

	// Add installGlobals() call if not present
	if !strings.Contains(configStr, "installGlobals()") {
		// Find the end of imports and add the call
		defineConfigPos := strings.Index(configStr, "export default defineConfig")
		if defineConfigPos > 0 {
			configStr = configStr[:defineConfigPos] + "installGlobals();\n\n" + configStr[defineConfigPos:]
		}
	}

	// Update remix plugin to include vercelPreset if not present
	if !strings.Contains(configStr, "presets: [vercelPreset()]") {
		// Find remix() plugin call and add presets
		remixRegex := regexp.MustCompile(`remix\(\s*\)`)
		if remixRegex.MatchString(configStr) {
			// Replace remix() with remix({ presets: [vercelPreset()] })
			configStr = remixRegex.ReplaceAllString(configStr, `remix({
      presets: [vercelPreset()],
    })`)
		} else {
			// Look for existing remix plugin with config - handle nested braces properly
			remixStart := strings.Index(configStr, "remix(")
			if remixStart != -1 {
				// Find the opening brace
				braceStart := strings.Index(configStr[remixStart:], "{")
				if braceStart != -1 {
					braceStart += remixStart

					// Count braces to find the matching closing brace
					braceCount := 0
					braceEnd := -1
					for i := braceStart; i < len(configStr); i++ {
						if configStr[i] == '{' {
							braceCount++
						} else if configStr[i] == '}' {
							braceCount--
							if braceCount == 0 {
								braceEnd = i
								break
							}
						}
					}

					if braceEnd != -1 {
						// Find the closing parenthesis after the closing brace
						parenEnd := strings.Index(configStr[braceEnd:], ")")
						if parenEnd != -1 {
							parenEnd += braceEnd

							// Extract the content inside the braces
							existingConfig := strings.TrimSpace(configStr[braceStart+1 : braceEnd])

							// Check if presets already exists
							if !strings.Contains(existingConfig, "presets:") {
								// Add presets to existing config
								var newConfig string
								if existingConfig == "" {
									newConfig = "remix({\n      presets: [vercelPreset()],\n    })"
								} else {
									// Add comma if needed
									if !strings.HasSuffix(strings.TrimSpace(existingConfig), ",") {
										existingConfig += ","
									}
									newConfig = fmt.Sprintf("remix({\n      %s\n      presets: [vercelPreset()],\n    })", existingConfig)
								}

								// Replace the entire remix(...) call
								configStr = configStr[:remixStart] + newConfig + configStr[parenEnd+1:]
							}
						}
					}
				}
			}
		}
	}

	return []byte(configStr)
}

// removeVercelPresetFromRemixConfig removes Vercel preset from Remix vite config
func removeVercelPresetFromRemixConfig(config []byte) []byte {
	configStr := string(config)

	// Remove the vercel import
	importRegex := regexp.MustCompile(`import\s+{\s*vercelPreset\s*}\s+from\s+["']@vercel/remix/vite["'];?\s*\n?`)
	configStr = importRegex.ReplaceAllString(configStr, "")

	// Remove installGlobals import and call (since they're Vercel-specific in this context)
	installGlobalsImportRegex := regexp.MustCompile(`import\s+{\s*installGlobals\s*}\s+from\s+["']@remix-run/node["'];?\s*\n?`)
	configStr = installGlobalsImportRegex.ReplaceAllString(configStr, "")

	installGlobalsCallRegex := regexp.MustCompile(`installGlobals\(\);\s*\n?`)
	configStr = installGlobalsCallRegex.ReplaceAllString(configStr, "")

	// Remove presets configuration from remix plugin - handle nested braces properly
	remixStart := strings.Index(configStr, "remix(")
	if remixStart != -1 {
		// Find the opening brace
		braceStart := strings.Index(configStr[remixStart:], "{")
		if braceStart != -1 {
			braceStart += remixStart

			// Count braces to find the matching closing brace
			braceCount := 0
			braceEnd := -1
			for i := braceStart; i < len(configStr); i++ {
				if configStr[i] == '{' {
					braceCount++
				} else if configStr[i] == '}' {
					braceCount--
					if braceCount == 0 {
						braceEnd = i
						break
					}
				}
			}

			if braceEnd != -1 {
				// Find the closing parenthesis after the closing brace
				parenEnd := strings.Index(configStr[braceEnd:], ")")
				if parenEnd != -1 {
					parenEnd += braceEnd

					// Extract the content inside the braces
					existingConfig := configStr[braceStart+1 : braceEnd]

					// Remove presets line
					presetsRegex := regexp.MustCompile(`,?\s*presets:\s*\[[^\]]*\]\s*,?`)
					newConfig := presetsRegex.ReplaceAllString(existingConfig, "")

					// Clean up any trailing commas
					newConfig = regexp.MustCompile(`,\s*$`).ReplaceAllString(newConfig, "")
					newConfig = regexp.MustCompile(`^\s*,`).ReplaceAllString(newConfig, "")
					newConfig = strings.TrimSpace(newConfig)

					var replacement string
					if newConfig == "" {
						replacement = "remix()"
					} else {
						replacement = fmt.Sprintf("remix({\n%s\n    })", newConfig)
					}

					// Replace the entire remix(...) call
					configStr = configStr[:remixStart] + replacement + configStr[parenEnd+1:]
				}
			}
		}
	}

	return []byte(configStr)
}

// patchPackageJSONForRemix adds Remix adapter to package.json (as devDependency)
func patchPackageJSONForRemix(packageJsonBytes []byte, adapter, version string) ([]byte, error) {
	var pkg map[string]any
	if err := json.Unmarshal(packageJsonBytes, &pkg); err != nil {
		return nil, errors.Errorf("failed to parse package.json: %w", err)
	}

	// Ensure devDependencies exists
	devDeps, ok := pkg["devDependencies"].(map[string]any)
	if !ok {
		devDeps = map[string]any{}
		pkg["devDependencies"] = devDeps
	}

	// Add the new adapter to devDependencies
	devDeps[adapter] = version

	// Use custom encoder to prevent HTML escaping
	buffer := &bytes.Buffer{}
	encoder := json.NewEncoder(buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(pkg); err != nil {
		return nil, errors.Errorf("failed to encode updated package.json: %w", err)
	}

	return buffer.Bytes(), nil
}

// patchPackageJSONForRemixServe adds @remix-run/serve to package.json dependencies (not devDependencies)
func patchPackageJSONForRemixServe(packageJsonBytes []byte, adapter, version string) ([]byte, error) {
	var pkg map[string]any
	if err := json.Unmarshal(packageJsonBytes, &pkg); err != nil {
		return nil, errors.Errorf("failed to parse package.json: %w", err)
	}

	// Ensure dependencies exists
	deps, ok := pkg["dependencies"].(map[string]any)
	if !ok {
		deps = map[string]any{}
		pkg["dependencies"] = deps
	}

	// Add @remix-run/serve to dependencies
	deps[adapter] = version

	// Also remove it from devDependencies if it exists there
	if devDeps, ok := pkg["devDependencies"].(map[string]any); ok {
		delete(devDeps, adapter)
	}

	// Use custom encoder to prevent HTML escaping
	buffer := &bytes.Buffer{}
	encoder := json.NewEncoder(buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(pkg); err != nil {
		return nil, errors.Errorf("failed to encode updated package.json: %w", err)
	}

	return buffer.Bytes(), nil
}

// removeRemixAdapter removes specific Remix adapter from package.json
func removeRemixAdapter(packageJsonBytes []byte, adapter string) ([]byte, error) {
	var pkg map[string]any
	if err := json.Unmarshal(packageJsonBytes, &pkg); err != nil {
		return nil, errors.Errorf("failed to parse package.json: %w", err)
	}

	// Remove from both dependencies and devDependencies
	for _, section := range []string{"dependencies", "devDependencies"} {
		if deps, ok := pkg[section].(map[string]any); ok {
			delete(deps, adapter)
		}
	}

	// Use custom encoder to prevent HTML escaping
	buffer := &bytes.Buffer{}
	encoder := json.NewEncoder(buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(pkg); err != nil {
		return nil, errors.Errorf("failed to encode updated package.json: %w", err)
	}

	return buffer.Bytes(), nil
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

// patchTanStackStartAppConfigForNetlify adds the Netlify plugin to TanStack Start app.config
func patchTanStackStartAppConfigForNetlify(config []byte) []byte {
	configStr := string(config)

	// Check if netlify plugin is already imported and added
	if strings.Contains(configStr, "netlify") && strings.Contains(configStr, "@netlify/vite-plugin-tanstack-start") {
		return config // Already configured
	}

	// Add import for netlify plugin if not present
	if !strings.Contains(configStr, `import netlify from "@netlify/vite-plugin-tanstack-start"`) {
		// Find existing imports and add the netlify import
		importRegex := regexp.MustCompile(`(import\s+.*from\s+["'].*["'];?\s*\n)`)
		matches := importRegex.FindAllStringIndex(configStr, -1)

		netlifyImport := `import netlify from "@netlify/vite-plugin-tanstack-start";` + "\n"

		if len(matches) > 0 {
			// Insert after the last import
			lastImportEnd := matches[len(matches)-1][1]
			configStr = configStr[:lastImportEnd] + netlifyImport + configStr[lastImportEnd:]
		} else {
			// No imports found, add at the beginning
			configStr = netlifyImport + configStr
		}
	}

	// Add netlify() to vite.plugins array if not present
	if !strings.Contains(configStr, "netlify()") {
		// For app.config, we need to add to vite: { plugins: [...] }
		// First check if vite.plugins exists
		vitePluginsRegex := regexp.MustCompile(`vite:\s*\{[^}]*plugins:\s*\[\s*([^\]]*)\s*\]`)
		if vitePluginsRegex.MatchString(configStr) {
			// Add netlify to existing vite.plugins array
			configStr = vitePluginsRegex.ReplaceAllStringFunc(configStr, func(match string) string {
				// Find the plugins array content
				submatch := vitePluginsRegex.FindStringSubmatch(match)
				if len(submatch) > 1 {
					existingPlugins := strings.TrimSpace(submatch[1])
					if existingPlugins == "" {
						return strings.Replace(match, "plugins: [", "plugins: [\n      netlify(),", 1)
					} else {
						// Add netlify to existing plugins
						if strings.HasSuffix(existingPlugins, ",") {
							return strings.Replace(match, "plugins: [", "plugins: [\n      netlify(),\n      ", 1)
						} else {
							// Insert netlify before the last plugin
							pluginsStart := strings.Index(match, "plugins: [")
							if pluginsStart != -1 {
								afterBracket := pluginsStart + len("plugins: [")
								return match[:afterBracket] + "\n      netlify()," + match[afterBracket:]
							}
						}
					}
				}
				return match
			})
		} else {
			// Check if vite object exists but without plugins
			viteRegex := regexp.MustCompile(`vite:\s*\{`)
			if viteRegex.MatchString(configStr) {
				// Add plugins array to existing vite object
				configStr = viteRegex.ReplaceAllString(configStr, `vite: {
    plugins: [
      netlify(),
    ],`)
			} else {
				// No vite object, add it to the config
				defineConfigRegex := regexp.MustCompile(`export\s+default\s+defineConfig\s*\(\s*\{`)
				if defineConfigRegex.MatchString(configStr) {
					configStr = defineConfigRegex.ReplaceAllString(configStr, `export default defineConfig({
  vite: {
    plugins: [
      netlify(),
    ],
  },`)
				}
			}
		}
	}

	return []byte(configStr)
}

// patchTanStackStartViteConfigForNetlify adds the Netlify plugin to TanStack Start vite config
func patchTanStackStartViteConfigForNetlify(config []byte) []byte {
	configStr := string(config)

	// Check if netlify plugin is already imported and added
	if strings.Contains(configStr, "netlify") && strings.Contains(configStr, "@netlify/vite-plugin-tanstack-start") {
		return config // Already configured
	}

	// Add import for netlify plugin if not present
	if !strings.Contains(configStr, `import netlify from "@netlify/vite-plugin-tanstack-start"`) {
		// Find existing imports and add the netlify import
		importRegex := regexp.MustCompile(`(import\s+.*from\s+["'].*["'];?\s*\n)`)
		matches := importRegex.FindAllStringIndex(configStr, -1)

		netlifyImport := `import netlify from "@netlify/vite-plugin-tanstack-start";` + "\n"

		if len(matches) > 0 {
			// Insert after the last import
			lastImportEnd := matches[len(matches)-1][1]
			configStr = configStr[:lastImportEnd] + netlifyImport + configStr[lastImportEnd:]
		} else {
			// No imports found, add at the beginning
			configStr = netlifyImport + configStr
		}
	}

	// Add netlify() to plugins array if not present
	if !strings.Contains(configStr, "netlify()") {
		// Find the plugins array by looking for "plugins: [" and matching brackets
		pluginsStart := strings.Index(configStr, "plugins:")
		if pluginsStart != -1 {
			// Find the opening bracket after "plugins:"
			bracketStart := strings.Index(configStr[pluginsStart:], "[")
			if bracketStart != -1 {
				bracketStart += pluginsStart

				// Count brackets to find the matching closing bracket
				bracketCount := 0
				bracketEnd := -1
				inString := false
				var stringChar rune

				for i := bracketStart; i < len(configStr); i++ {
					ch := rune(configStr[i])

					// Handle string literals
					if ch == '"' || ch == '\'' || ch == '`' {
						if !inString {
							inString = true
							stringChar = ch
						} else if ch == stringChar && (i == 0 || configStr[i-1] != '\\') {
							inString = false
						}
					}

					if !inString {
						if ch == '[' {
							bracketCount++
						} else if ch == ']' {
							bracketCount--
							if bracketCount == 0 {
								bracketEnd = i
								break
							}
						}
					}
				}

				if bracketEnd != -1 {
					// Insert netlify() at the beginning of the plugins array
					afterOpenBracket := bracketStart + 1
					// Skip whitespace after opening bracket
					for afterOpenBracket < len(configStr) && (configStr[afterOpenBracket] == ' ' || configStr[afterOpenBracket] == '\n' || configStr[afterOpenBracket] == '\t') {
						afterOpenBracket++
					}

					// Build the new config with netlify() inserted
					newConfig := configStr[:afterOpenBracket] + "\n    netlify(),\n    " + configStr[afterOpenBracket:]
					return []byte(newConfig)
				}
			}
		} else {
			// No plugins array found, need to add one
			defineConfigRegex := regexp.MustCompile(`export\s+default\s+defineConfig\s*\(\s*\{`)
			if defineConfigRegex.MatchString(configStr) {
				configStr = defineConfigRegex.ReplaceAllString(configStr, `export default defineConfig({
  plugins: [
    netlify(),
  ],`)
			}
		}
	}

	return []byte(configStr)
}
