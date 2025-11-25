package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-errors/errors"
	"github.com/meroxa/prod/cli/internal/analyzer"
	"github.com/pmezard/go-difflib/difflib"
)

// Python configuration result containing version file diffs
type PythonConfigResult struct {
	PythonVersionCreated bool              `json:"pythonVersionCreated"`
	PythonVersionDiff    []DiffLine        `json:"pythonVersionDiff,omitempty"`
	DjangoConfigUpdated  bool              `json:"djangoConfigUpdated"`
	DjangoConfigDiff     []DiffLine        `json:"djangoConfigDiff,omitempty"`
	DjangoConfigPath     string            `json:"djangoConfigPath,omitempty"`
	DjangoEnvVars        map[string]string `json:"djangoEnvVars,omitempty"`
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

// findPythonFrameworkFromServiceRequirements extracts the Python framework from ServiceRequirements
func findPythonFrameworkFromServiceRequirements(serviceRequirements []analyzer.ServiceRequirement) string {
	for _, sr := range serviceRequirements {
		if sr.Type == "framework" {
			return sr.Provider // Returns "django", "flask", "fastapi", etc.
		}
	}
	return ""
}

// configureDjango updates Django settings for deployment
func (a *Activities) configureDjango(ctx context.Context, plan DeployPlan) (PythonConfigResult, error) {
	result := PythonConfigResult{}

	// Check if this is a Django project by looking at ServiceRequirements
	framework := findPythonFrameworkFromServiceRequirements(plan.Spec.ServiceRequirements)
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
		result.DjangoEnvVars = handler.GetRequiredEnvVars(plan.Platform)
		return result, nil
	}

	// Try to find and update Django settings file directly (no env vars detected)
	diff, configPath, err := handler.HandleConfig(plan.Source, plan.Platform)
	if err != nil {
		a.uiWriter.SendStatusComplete("django_config", "⚠️  Django settings.py not found, will use environment variables")
		// Settings file not found, provide env vars as fallback
		result.DjangoEnvVars = handler.GetRequiredEnvVars(plan.Platform)
		return result, nil
	}

	if len(diff) > 0 {
		result.DjangoConfigUpdated = true
		result.DjangoConfigDiff = diff
		result.DjangoConfigPath = configPath
		a.uiWriter.SendStatusComplete("django_config", "✅ Updated Django settings for deployment")
	} else {
		a.uiWriter.SendStatusComplete("django_config", "✅ Django settings already configured")
	}

	return result, nil
}
