package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/go-errors/errors"
)

// FrameworkHandler defines the unified interface for all framework-specific operations
// This interface supports both JavaScript and Python frameworks
type FrameworkHandler interface {
	// GetName returns the framework name (e.g., "Remix", "django")
	GetName() string

	// HandleConfig updates framework configuration files for a platform
	HandleConfig(projectPath string, platform Platform) ([]DiffLine, string, error)

	// RestoreConfigFromBackup restores configuration from backup
	RestoreConfigFromBackup(ctx context.Context, plan DeployPlan) ([]DiffLine, error)

	// GetConfigFilenames returns possible config filenames for this framework (in preference order)
	GetConfigFilenames() []string

	// PrepareDeployment applies framework-specific deployment configuration (start commands, env vars)
	PrepareDeployment(plan DeployPlan) DeployPlan

	// Language-specific methods with default implementations in BaseFrameworkHandler:

	// PatchPackageJSON applies framework-specific package.json changes (JavaScript only)
	PatchPackageJSON(origPackageJson []byte, platform Platform) ([]byte, bool, error)

	// HandlePlatformSpecificFiles handles platform-specific file operations (JavaScript only, e.g., .npmrc for Remix)
	HandlePlatformSpecificFiles(projectPath string, platform Platform) error
}

// BaseFrameworkHandler provides common functionality for all framework handlers
type BaseFrameworkHandler struct{}

// PrepareDeployment provides a default implementation that returns the plan unchanged
func (b *BaseFrameworkHandler) PrepareDeployment(plan DeployPlan) DeployPlan {
	return plan
}

// PatchPackageJSON provides a default no-op implementation (JavaScript frameworks override this)
func (b *BaseFrameworkHandler) PatchPackageJSON(origPackageJson []byte, platform Platform) ([]byte, bool, error) {
	return origPackageJson, false, nil
}

// HandlePlatformSpecificFiles provides a default no-op implementation (JavaScript frameworks override this)
func (b *BaseFrameworkHandler) HandlePlatformSpecificFiles(projectPath string, platform Platform) error {
	return nil
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

// createBackup creates a timestamped backup of a file in the .prod directory
func (b *BaseFrameworkHandler) createBackup(projectPath, filename string, content []byte) error {
	prodDir := filepath.Join(projectPath, ".prod")
	if err := os.MkdirAll(prodDir, 0755); err != nil {
		return errors.Errorf("failed to create .prod directory: %w", err)
	}

	backupPath := filepath.Join(prodDir, fmt.Sprintf("%s.%s.bak", filepath.Base(filename), time.Now().Format("20060102-150405")))
	if err := os.WriteFile(backupPath, content, 0644); err != nil {
		return errors.Errorf("failed to write backup: %w", err)
	}

	return nil
}
