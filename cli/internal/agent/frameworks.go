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
	"time"

	"github.com/go-errors/errors"
	"github.com/meroxa/prod/cli/internal/deployment"
	"github.com/pmezard/go-difflib/difflib"
)

// FrameworkHandler defines the interface for framework-specific operations
type FrameworkHandler interface {
	// GetName returns the framework name
	GetName() string

	// PatchPackageJSON applies framework-specific package.json changes for a platform
	PatchPackageJSON(origPackageJson []byte, platform Platform) ([]byte, bool, error)

	// HandleConfig updates framework configuration files for a platform
	HandleConfig(projectPath string, platform Platform) ([]DiffLine, string, error)

	// RestoreConfigFromBackup restores configuration from backup
	RestoreConfigFromBackup(ctx context.Context, plan DeployPlan) ([]DiffLine, error)

	// GetConfigFilenames returns possible config filenames for this framework (in preference order)
	GetConfigFilenames() []string

	// HandlePlatformSpecificFiles handles any platform-specific file operations (like .npmrc for Remix)
	HandlePlatformSpecificFiles(projectPath string, platform Platform) error

	// PrepareDeployment applies framework-specific deployment configuration (start commands, env vars)
	PrepareDeployment(plan DeployPlan) DeployPlan
}

// FrameworkRegistry manages framework handlers
type FrameworkRegistry struct {
	handlers map[string]FrameworkHandler
}

// NewFrameworkRegistry creates a new registry with default handlers
func NewFrameworkRegistry() *FrameworkRegistry {
	registry := &FrameworkRegistry{
		handlers: make(map[string]FrameworkHandler),
	}

	// Register built-in handlers
	registry.RegisterHandler(&RemixHandler{})
	registry.RegisterHandler(&SvelteKitHandler{})
	registry.RegisterHandler(&NuxtHandler{})

	return registry
}

// RegisterHandler adds a framework handler to the registry
func (r *FrameworkRegistry) RegisterHandler(handler FrameworkHandler) {
	r.handlers[handler.GetName()] = handler
}

// GetHandler returns the handler for a framework, or nil if not found
func (r *FrameworkRegistry) GetHandler(framework string) FrameworkHandler {
	return r.handlers[framework]
}

// BaseFrameworkHandler provides common functionality for framework handlers
type BaseFrameworkHandler struct{}

// HandlePlatformSpecificFiles provides a default implementation that does nothing
func (b *BaseFrameworkHandler) HandlePlatformSpecificFiles(projectPath string, platform Platform) error {
	return nil
}

// PrepareDeployment provides a default implementation that returns the plan unchanged
func (b *BaseFrameworkHandler) PrepareDeployment(plan DeployPlan) DeployPlan {
	return plan
}

// findConfigFile finds the first existing config file from a list of possible filenames
func (b *BaseFrameworkHandler) findConfigFile(projectPath string, filenames []string) (string, error) {
	for _, filename := range filenames {
		configPath := filepath.Join(projectPath, filename)
		if _, err := os.Stat(configPath); err == nil {
			return configPath, nil
		}
	}
	return "", errors.Errorf("no config file found in %s", projectPath)
}

// createBackup creates a timestamped backup of a file
func (b *BaseFrameworkHandler) createBackup(projectPath, filename string, content []byte) error {
	prodDir := filepath.Join(projectPath, ".prod")
	if err := os.MkdirAll(prodDir, 0755); err != nil {
		return errors.Errorf("failed to create .prod directory: %w", err)
	}

	timestamp := time.Now().Format("20060102-150405")
	backupPath := filepath.Join(prodDir, fmt.Sprintf("%s.%s.bak", filename, timestamp))
	return os.WriteFile(backupPath, content, 0644)
}

// generateConfigDiff generates a diff between original and updated config
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
	case Render, FlyIO, Heroku:
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
	if err := os.WriteFile(configPath, updatedConfig, 0644); err != nil {
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
		return os.WriteFile(npmrcPath, []byte(npmrcContent), 0644)
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
	if err := os.WriteFile(configPath, backupConfig, 0644); err != nil {
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
	case Render, FlyIO, Heroku:
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
	case Render, FlyIO, Heroku:
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
	case Render, FlyIO, Heroku:
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
	if err := os.WriteFile(configPath, updatedConfig, 0644); err != nil {
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
	if err := os.WriteFile(configPath, backupConfig, 0644); err != nil {
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
	case Render, FlyIO, Heroku:
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

// Global framework registry instance
var frameworkRegistry = NewFrameworkRegistry()
