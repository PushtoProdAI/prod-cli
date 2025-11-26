package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-errors/errors"
	"github.com/meroxa/prod/cli/internal/analyzer"
	"github.com/pmezard/go-difflib/difflib"
)

// Python configuration result containing version file diffs and framework configuration
type PythonConfigResult struct {
	PythonVersionCreated bool       `json:"pythonVersionCreated"`
	PythonVersionDiff    []DiffLine `json:"pythonVersionDiff,omitempty"`

	// Framework-agnostic configuration fields
	FrameworkConfigUpdated bool              `json:"frameworkConfigUpdated"`
	FrameworkConfigDiff    []DiffLine        `json:"frameworkConfigDiff,omitempty"`
	FrameworkConfigPath    string            `json:"frameworkConfigPath,omitempty"`
	FrameworkEnvVars       map[string]string `json:"frameworkEnvVars,omitempty"`
	FrameworkRunCommand    string            `json:"frameworkRunCommand,omitempty"`
	ServerAdded            bool              `json:"serverAdded"`

	// Static files configuration
	StaticFilesConfigured bool       `json:"staticFilesConfigured"`
	StaticFilesDiff       []DiffLine `json:"staticFilesDiff,omitempty"`
	WhiteNoiseAdded       bool       `json:"whiteNoiseAdded"`
}

// generatePythonVersion creates .python-version file for Python projects
func (a *Activities) generatePythonVersion(ctx context.Context, plan DeployPlan) (PythonConfigResult, error) {
	result := PythonConfigResult{}
	projectPath := plan.Source

	a.uiWriter.SendStatus("python_version", "Checking for .python-version file...")

	// Check if .python-version already exists
	pythonVersionPath := filepath.Join(projectPath, ".python-version")

	var originalContent []byte
	versionExists := false
	if _, err := os.Stat(pythonVersionPath); err == nil {
		// File exists, read original content for diff
		originalContent, err = os.ReadFile(pythonVersionPath)
		if err != nil {
			a.uiWriter.SendStatusComplete("python_version", "❌ Failed to read existing .python-version")
			return result, errors.Errorf("failed to read existing .python-version: %w", err)
		}
		versionExists = true
	}

	// Create .prod directory if it doesn't exist for backups
	prodDir := filepath.Join(projectPath, ".prod")
	os.MkdirAll(prodDir, 0755)

	// Python version to use
	pythonVersion := "3.11\n"

	// Check if the file already has the correct version
	if versionExists && string(originalContent) == pythonVersion {
		a.uiWriter.SendStatusComplete("python_version", "✅ .python-version already set to 3.11")
		return result, nil
	}

	// Backup original file if it exists
	if versionExists {
		backupPath := filepath.Join(prodDir, fmt.Sprintf(".python-version.%s.bak", time.Now().Format("20060102-150405")))
		if err := os.WriteFile(backupPath, originalContent, 0644); err != nil {
			a.uiWriter.SendStatusComplete("python_version", "❌ Failed to backup .python-version")
			return result, errors.Errorf("failed to backup .python-version: %w", err)
		}
	}

	// Write the .python-version file
	if err := os.WriteFile(pythonVersionPath, []byte(pythonVersion), 0644); err != nil {
		a.uiWriter.SendStatusComplete("python_version", "❌ Failed to create .python-version")
		return result, errors.Errorf("failed to write .python-version: %w", err)
	}

	result.PythonVersionCreated = true

	// Generate diff for UI display
	if versionExists {
		diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
			A:        difflib.SplitLines(string(originalContent)),
			B:        difflib.SplitLines(pythonVersion),
			FromFile: ".python-version",
			ToFile:   ".python-version",
			Context:  3,
		})
		if err == nil {
			result.PythonVersionDiff = parseDiffString(diff)
		}
		a.uiWriter.SendStatusComplete("python_version", "✅ Updated .python-version to 3.11")
	} else {
		// For new files, create a simple diff showing the addition
		result.PythonVersionDiff = []DiffLine{
			{Type: "fileheader", Content: "--- .python-version"},
			{Type: "fileheader", Content: "+++ .python-version"},
			{Type: "added", Content: "+3.11"},
		}
		a.uiWriter.SendStatusComplete("python_version", "✅ Created .python-version with Python 3.11")
	}

	return result, nil
}

// configureDjango updates Django settings for deployment
func (a *Activities) configureDjango(ctx context.Context, plan DeployPlan) (PythonConfigResult, error) {
	result := PythonConfigResult{}

	// Check if this is a Django project by looking at ServiceRequirements
	framework := findFrameworkFromServiceRequirements(plan.Spec.ServiceRequirements)
	if framework != "django" {
		// Not a Django project, skip configuration
		a.uiWriter.SendStatus("django_config", "Skipping Django configuration (not a Django project)")
		return result, nil
	}

	a.uiWriter.SendStatus("django_config", "Configuring Django settings...")

	// Create Django handler
	handler := &DjangoHandler{}

	// Check if Django settings already use environment variables
	// The analyzer already scanned for env vars, so we can check plan.Spec.EnvVars
	usesEnvVars := false
	for _, envVar := range plan.Spec.EnvVars {
		// Check if ALLOWED_HOSTS or DJANGO_ALLOWED_HOSTS is referenced
		if strings.Contains(envVar.VarName, "ALLOWED_HOSTS") ||
			strings.Contains(envVar.VarName, "CSRF_TRUSTED_ORIGINS") {
			usesEnvVars = true
			break
		}
	}

	if usesEnvVars {
		a.uiWriter.SendStatusComplete("django_config", "✅ Django already uses environment variables")
		// Provide the env vars that should be set
		result.FrameworkEnvVars = handler.GetRequiredEnvVars(plan.Platform)
		return result, nil
	}

	// Try to find and update Django settings file directly (no env vars detected)
	diff, configPath, err := handler.HandleConfig(plan.Source, plan.Platform)
	if err != nil {
		a.uiWriter.SendStatusComplete("django_config", "⚠️  Django settings.py not found, will use environment variables")
		// Settings file not found, provide env vars as fallback
		result.FrameworkEnvVars = handler.GetRequiredEnvVars(plan.Platform)
		return result, nil
	}

	if len(diff) > 0 {
		result.FrameworkConfigUpdated = true
		result.FrameworkConfigDiff = diff
		result.FrameworkConfigPath = configPath
		a.uiWriter.SendStatusComplete("django_config", "✅ Updated Django settings for deployment")
	} else {
		a.uiWriter.SendStatusComplete("django_config", "✅ Django settings already configured")
	}

	return result, nil
}

// setupDjangoServer detects and configures production server (gunicorn/uvicorn) for Django
func (a *Activities) setupDjangoServer(ctx context.Context, plan DeployPlan) (PythonConfigResult, error) {
	result := PythonConfigResult{}

	// Check if this is a Django project
	framework := findFrameworkFromServiceRequirements(plan.Spec.ServiceRequirements)
	if framework != "django" {
		// Not a Django project, skip
		return result, nil
	}

	a.uiWriter.SendStatus("django_server", "Setting up production server for Django...")

	handler := &DjangoHandler{}

	// Scan dependencies from the project files directly
	dependencies, err := scanPythonDependencies(plan.Source)
	if err != nil {
		slog.Warn("Failed to extract dependencies", "error", err)
		dependencies = []analyzer.Dependency{}
	}

	// Check if we already have a production-ready command from LLM/Procfile
	existingCommand := strings.TrimSpace(plan.Spec.StartCommand)
	hasProductionServer := strings.Contains(existingCommand, "gunicorn") ||
		strings.Contains(existingCommand, "uvicorn") ||
		strings.Contains(existingCommand, "daphne")

	var serverConfig *DjangoServerConfig
	var runCommand string

	if hasProductionServer {
		// LLM/Procfile already suggested a production server
		slog.Info("Found existing production server command", "command", existingCommand)

		// Parse to determine which server and ensure dependency is installed
		if strings.Contains(existingCommand, "uvicorn") || strings.Contains(existingCommand, "daphne") {
			serverConfig = &DjangoServerConfig{ServerType: ServerTypeASGI}
		} else {
			serverConfig = &DjangoServerConfig{ServerType: ServerTypeWSGI}
		}

		// Use the existing command
		runCommand = existingCommand

	} else {
		// No production server found (empty, or manage.py runserver, etc.)
		// Detect WSGI vs ASGI and generate command ourselves
		slog.Info("No production server in StartCommand, detecting from project structure")

		serverConfig, err = handler.detectDjangoServer(plan.Source, dependencies)
		if err != nil {
			a.uiWriter.SendStatusComplete("django_server", "⚠️  Could not detect Django server configuration")
			slog.Warn("Failed to detect Django server", "error", err)
			return result, nil
		}

		slog.Info("Detected Django server configuration",
			"type", serverConfig.ServerType,
			"module", serverConfig.ProjectModule,
			"hasChannels", serverConfig.HasChannels)

		// Generate run command
		runCommand = handler.generateRunCommand(serverConfig)
		slog.Info("Generated Django run command", "command", runCommand)
	}

	// Check if server dependency is already installed
	serverAdded := false
	if !handler.hasServerInstalled(dependencies, serverConfig.ServerType) {
		// Add server dependency
		err := handler.addServerDependency(plan.Source, serverConfig.ServerType)
		if err != nil {
			slog.Warn("Failed to add server dependency", "error", err)
			// Continue anyway - user can add it manually
		} else {
			serverAdded = true
			slog.Info("Added server dependency", "server", handler.getRequiredServer(serverConfig.ServerType))
		}
	} else {
		slog.Info("Server already installed", "server", handler.getRequiredServer(serverConfig.ServerType))
	}

	result.FrameworkRunCommand = runCommand
	result.ServerAdded = serverAdded

	a.uiWriter.SendStatusComplete("django_server", fmt.Sprintf("✅ Configured %s for production", handler.getRequiredServer(serverConfig.ServerType)))

	return result, nil
}

// configurePythonFramework is a framework-agnostic activity that dispatches to the appropriate framework handler
func (a *Activities) configurePythonFramework(ctx context.Context, plan DeployPlan) (PythonConfigResult, error) {
	framework := findFrameworkFromServiceRequirements(plan.Spec.ServiceRequirements)

	slog.Info("Configuring Python framework", "framework", framework)

	switch framework {
	case "django":
		return a.configureDjango(ctx, plan)
	case "flask":
		// TODO: Implement Flask configuration
		return PythonConfigResult{}, nil
	case "fastapi":
		// TODO: Implement FastAPI configuration
		return PythonConfigResult{}, nil
	default:
		slog.Info("No framework-specific configuration needed")
		return PythonConfigResult{}, nil
	}
}

// setupPythonServer is a framework-agnostic activity that sets up production server for Python frameworks
func (a *Activities) setupPythonServer(ctx context.Context, plan DeployPlan) (PythonConfigResult, error) {
	framework := findFrameworkFromServiceRequirements(plan.Spec.ServiceRequirements)

	slog.Info("Setting up Python server", "framework", framework)

	switch framework {
	case "django":
		return a.setupDjangoServer(ctx, plan)
	case "flask":
		// TODO: Implement Flask server setup (gunicorn for Flask)
		return PythonConfigResult{}, nil
	case "fastapi":
		// TODO: Implement FastAPI server setup (uvicorn for FastAPI)
		return PythonConfigResult{}, nil
	default:
		slog.Info("No framework-specific server setup needed")
		return PythonConfigResult{}, nil
	}
}

// configureDjangoStaticFiles sets up WhiteNoise and collectstatic for Django projects
func (a *Activities) configureDjangoStaticFiles(ctx context.Context, plan DeployPlan) (PythonConfigResult, error) {
	result := PythonConfigResult{}

	// Check if this is a Django project
	framework := findFrameworkFromServiceRequirements(plan.Spec.ServiceRequirements)
	if framework != "django" {
		// Not a Django project, skip
		return result, nil
	}

	a.uiWriter.SendStatus("django_static", "Configuring static files for Django...")

	handler := &DjangoHandler{}

	// Find Django settings file
	settingsPath, _, err := handler.findDjangoSettings(plan.Source)
	if err != nil {
		a.uiWriter.SendStatusComplete("django_static", "⚠️  Could not find Django settings.py")
		slog.Warn("Failed to find Django settings for static files configuration", "error", err)
		return result, nil
	}

	// Scan dependencies
	dependencies, err := scanPythonDependencies(plan.Source)
	if err != nil {
		slog.Warn("Failed to extract dependencies for static files check", "error", err)
		dependencies = []analyzer.Dependency{}
	}

	// Detect current static files setup
	staticConfig, err := handler.detectStaticFilesSetup(plan.Source, settingsPath, dependencies)
	if err != nil {
		slog.Warn("Failed to detect static files setup", "error", err)
		a.uiWriter.SendStatusComplete("django_static", "⚠️  Could not detect static files configuration")
		return result, nil
	}

	slog.Info("Detected static files configuration",
		"hasStaticRoot", staticConfig.HasStaticRoot,
		"hasWhiteNoise", staticConfig.HasWhiteNoise,
		"hasStaticFiles", staticConfig.HasStaticFilesDir,
		"needsConfig", staticConfig.NeedsConfiguration)

	// If no static files or already configured, skip
	if !staticConfig.HasStaticFilesDir {
		a.uiWriter.SendStatusComplete("django_static", "✅ No static files detected")
		slog.Info("No static files directory found, skipping static configuration")
		return result, nil
	}

	if !staticConfig.NeedsConfiguration {
		a.uiWriter.SendStatusComplete("django_static", "✅ Static files already configured")
		slog.Info("Static files already configured with WhiteNoise and STATIC_ROOT")
		return result, nil
	}

	// Add WhiteNoise to requirements.txt if needed
	if !staticConfig.HasWhiteNoise {
		err := handler.addWhiteNoiseDependency(plan.Source)
		if err != nil {
			slog.Warn("Failed to add WhiteNoise dependency", "error", err)
		} else {
			result.WhiteNoiseAdded = true
			slog.Info("Added WhiteNoise to requirements.txt")
		}
	}

	// Update settings.py with STATIC_ROOT and WhiteNoise middleware
	originalContent, updatedContent, err := handler.configureStaticFiles(plan.Source, settingsPath)
	if err != nil {
		a.uiWriter.SendStatusComplete("django_static", "⚠️  Failed to configure static files")
		slog.Warn("Failed to update settings.py for static files", "error", err)
		return result, nil
	}

	// Create backup
	if err := handler.createBackup(plan.Source, settingsPath, originalContent); err != nil {
		slog.Warn("Failed to create backup of settings.py", "error", err)
	}

	// Write updated settings
	if err := os.WriteFile(settingsPath, updatedContent, 0644); err != nil {
		a.uiWriter.SendStatusComplete("django_static", "❌ Failed to write updated settings")
		return result, errors.Errorf("failed to write updated settings: %w", err)
	}

	// Generate diff for UI display
	diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(string(originalContent)),
		B:        difflib.SplitLines(string(updatedContent)),
		FromFile: filepath.Base(settingsPath),
		ToFile:   filepath.Base(settingsPath),
		Context:  3,
	})
	if err == nil && diff != "" {
		result.StaticFilesConfigured = true
		result.StaticFilesDiff = parseDiffString(diff)
	}

	a.uiWriter.SendStatusComplete("django_static", "✅ Configured WhiteNoise for static files")
	slog.Info("Successfully configured Django static files with WhiteNoise")

	return result, nil
}

// scanPythonDependencies quickly scans requirements.txt to check for installed packages
func scanPythonDependencies(projectPath string) ([]analyzer.Dependency, error) {
	var dependencies []analyzer.Dependency

	// Check requirements.txt
	requirementsPath := filepath.Join(projectPath, "requirements.txt")
	if content, err := os.ReadFile(requirementsPath); err == nil {
		lines := strings.Split(string(content), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}

			// Extract package name (ignore version specifiers)
			packageName := line
			for _, sep := range []string{"==", ">=", "<=", "!=", "~=", ">", "<"} {
				if idx := strings.Index(line, sep); idx != -1 {
					packageName = line[:idx]
					break
				}
			}
			// Handle extras syntax like uvicorn[standard]
			packageName = strings.TrimSpace(packageName)

			dependencies = append(dependencies, analyzer.Dependency{
				Name:   packageName,
				Source: "requirements.txt",
			})
		}
	}

	return dependencies, nil
}
