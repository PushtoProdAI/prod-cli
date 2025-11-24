package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/go-errors/errors"
	"github.com/pmezard/go-difflib/difflib"
)

// Python configuration result containing version file diffs
type PythonConfigResult struct {
	PythonVersionCreated bool       `json:"pythonVersionCreated"`
	PythonVersionDiff    []DiffLine `json:"pythonVersionDiff,omitempty"`
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
	if err := os.MkdirAll(prodDir, 0755); err != nil {
		a.uiWriter.SendStatusComplete("python_version", "❌ Failed to create .prod directory")
		return result, errors.Errorf("failed to create .prod directory: %w", err)
	}

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
