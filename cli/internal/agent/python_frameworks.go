package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-errors/errors"
	"github.com/meroxa/prod/cli/internal/deployment"
	"github.com/pmezard/go-difflib/difflib"
)

// DjangoHandler implements FrameworkHandler for Django projects
type DjangoHandler struct {
	BaseFrameworkHandler
}

// GetName returns the framework name
func (h *DjangoHandler) GetName() string {
	return "django"
}

// GetConfigFilenames returns possible Django settings file locations
func (h *DjangoHandler) GetConfigFilenames() []string {
	// Common Django settings file patterns
	return []string{
		"settings.py",
		"*/settings.py",
		"*/settings/base.py",
		"*/settings/production.py",
		"config/settings.py",
		"config/settings/base.py",
		"config/settings/production.py",
	}
}

// findDjangoSettings locates the Django settings file
func (h *DjangoHandler) findDjangoSettings(projectPath string) (string, string, error) {
	// First, try to find from manage.py
	managePath := filepath.Join(projectPath, "manage.py")
	if content, err := os.ReadFile(managePath); err == nil {
		// Extract DJANGO_SETTINGS_MODULE value
		re := regexp.MustCompile(`DJANGO_SETTINGS_MODULE['"],\s*['"]([^'"]+)['"]`)
		if matches := re.FindSubmatch(content); len(matches) > 1 {
			settingsModule := string(matches[1])
			// Convert module path to file path (e.g., "myproject.settings" -> "myproject/settings.py")
			settingsPath := strings.ReplaceAll(settingsModule, ".", string(os.PathSeparator)) + ".py"
			fullPath := filepath.Join(projectPath, settingsPath)
			if _, err := os.Stat(fullPath); err == nil {
				return fullPath, settingsModule, nil
			}
		}
	}

	// Fallback: search for settings files using glob patterns
	for _, pattern := range h.GetConfigFilenames() {
		if strings.Contains(pattern, "*") {
			// Handle glob patterns
			matches, err := filepath.Glob(filepath.Join(projectPath, pattern))
			if err == nil && len(matches) > 0 {
				// Prefer files with "base" or "production" in the name
				for _, match := range matches {
					if strings.Contains(match, "base") || strings.Contains(match, "production") {
						return match, "", nil
					}
				}
				// Return first match
				return matches[0], "", nil
			}
		} else {
			fullPath := filepath.Join(projectPath, pattern)
			if _, err := os.Stat(fullPath); err == nil {
				return fullPath, "", nil
			}
		}
	}

	return "", "", errors.New("Django settings.py not found")
}

// getDomainPatterns returns platform-specific domain wildcard patterns for ALLOWED_HOSTS
func (h *DjangoHandler) getDomainPatterns(platform Platform) []string {
	// Use Django's wildcard syntax: leading dot matches domain and all subdomains
	switch platform {
	case FlyIO:
		return []string{".fly.dev"}
	case Heroku:
		return []string{".herokuapp.com"}
	case Netlify:
		return []string{".netlify.app"}
	case Vercel:
		return []string{".vercel.app"}
	case Render:
		return []string{".onrender.com"}
	case AWS:
		return []string{".awsapprunner.com"}
	default:
		return []string{}
	}
}

// getCsrfOrigins returns CSRF_TRUSTED_ORIGINS wildcard values
func (h *DjangoHandler) getCsrfOrigins(platform Platform) []string {
	// Use Django 4.0+ wildcard syntax: https://*.domain.com
	switch platform {
	case FlyIO:
		return []string{"https://*.fly.dev"}
	case Heroku:
		return []string{"https://*.herokuapp.com"}
	case Netlify:
		return []string{"https://*.netlify.app"}
	case Vercel:
		return []string{"https://*.vercel.app"}
	case Render:
		return []string{"https://*.onrender.com"}
	case AWS:
		return []string{"https://*.awsapprunner.com"}
	default:
		return []string{}
	}
}

// updateSettingsFile modifies the Django settings file
func (h *DjangoHandler) updateSettingsFile(settingsPath string, platform Platform) ([]byte, []byte, error) {
	originalContent, err := os.ReadFile(settingsPath)
	if err != nil {
		return nil, nil, errors.Errorf("failed to read settings file: %w", err)
	}

	contentStr := string(originalContent)

	// Modify the settings file directly with platform-specific wildcards
	domainPatterns := h.getDomainPatterns(platform)
	csrfOrigins := h.getCsrfOrigins(platform)

	// Update ALLOWED_HOSTS
	allowedHostsStr := "ALLOWED_HOSTS = [" + strings.Join(formatStringList(domainPatterns), ", ") + "]"

	// Check if ALLOWED_HOSTS exists (handle both single-line and multi-line)
	allowedHostsRe := regexp.MustCompile(`(?ms)^ALLOWED_HOSTS\s*=\s*\[.*?\]`)
	if allowedHostsRe.MatchString(contentStr) {
		// Replace existing ALLOWED_HOSTS
		contentStr = allowedHostsRe.ReplaceAllString(contentStr, allowedHostsStr)
	} else {
		// Add ALLOWED_HOSTS after DEBUG setting or at end
		debugRe := regexp.MustCompile(`(?m)^DEBUG\s*=.*$`)
		if debugRe.MatchString(contentStr) {
			contentStr = debugRe.ReplaceAllStringFunc(contentStr, func(match string) string {
				return match + "\n\n# Added by prod CLI for deployment\n" + allowedHostsStr
			})
		} else {
			// Add at the end
			contentStr += "\n\n# Added by prod CLI for deployment\n" + allowedHostsStr + "\n"
		}
	}

	// Update CSRF_TRUSTED_ORIGINS (Django 4.0+)
	csrfOriginsStr := "CSRF_TRUSTED_ORIGINS = [" + strings.Join(formatStringList(csrfOrigins), ", ") + "]"

	// Check if CSRF_TRUSTED_ORIGINS exists (handle both single-line and multi-line)
	csrfRe := regexp.MustCompile(`(?ms)^CSRF_TRUSTED_ORIGINS\s*=\s*\[.*?\]`)
	if csrfRe.MatchString(contentStr) {
		// Replace existing CSRF_TRUSTED_ORIGINS
		contentStr = csrfRe.ReplaceAllString(contentStr, csrfOriginsStr)
	} else {
		// Add CSRF_TRUSTED_ORIGINS after ALLOWED_HOSTS
		contentStr = allowedHostsRe.ReplaceAllStringFunc(contentStr, func(match string) string {
			return match + "\n" + csrfOriginsStr
		})
	}

	return originalContent, []byte(contentStr), nil
}

// formatStringList formats a list of strings for Python
func formatStringList(items []string) []string {
	result := make([]string, len(items))
	for i, item := range items {
		result[i] = fmt.Sprintf("'%s'", item)
	}
	return result
}

// HandleConfig updates Django configuration for deployment
func (h *DjangoHandler) HandleConfig(projectPath string, platform Platform) ([]DiffLine, string, error) {
	// Find Django settings file
	settingsPath, _, err := h.findDjangoSettings(projectPath)
	if err != nil {
		return nil, "", err
	}

	// Update settings file
	originalContent, updatedContent, err := h.updateSettingsFile(settingsPath, platform)
	if err != nil {
		return nil, "", err
	}

	// If content unchanged (using env vars), return empty diff
	if string(originalContent) == string(updatedContent) {
		return []DiffLine{}, settingsPath, nil
	}

	// Create backup
	if err := h.createBackup(projectPath, settingsPath, originalContent); err != nil {
		return nil, "", err
	}

	// Write updated settings
	if err := os.WriteFile(settingsPath, updatedContent, 0644); err != nil {
		return nil, "", errors.Errorf("failed to write updated settings: %w", err)
	}

	// Generate diff
	diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(string(originalContent)),
		B:        difflib.SplitLines(string(updatedContent)),
		FromFile: filepath.Base(settingsPath),
		ToFile:   filepath.Base(settingsPath),
		Context:  3,
	})
	if err != nil {
		return nil, "", errors.Errorf("failed to generate diff: %w", err)
	}

	return parseDiffString(diff), settingsPath, nil
}

// RestoreConfigFromBackup restores Django settings from backup
func (h *DjangoHandler) RestoreConfigFromBackup(ctx context.Context, plan DeployPlan) ([]DiffLine, error) {
	settingsPath, _, err := h.findDjangoSettings(plan.Source)
	if err != nil {
		return nil, err
	}

	prodDir := filepath.Join(plan.Source, ".prod")
	backupPath := filepath.Join(prodDir, fmt.Sprintf("%s.bak", filepath.Base(settingsPath)))

	backupContent, err := os.ReadFile(backupPath)
	if err != nil {
		return nil, errors.Errorf("failed to read backup: %w", err)
	}

	currentContent, err := os.ReadFile(settingsPath)
	if err != nil {
		return nil, errors.Errorf("failed to read current settings: %w", err)
	}

	if err := os.WriteFile(settingsPath, backupContent, 0644); err != nil {
		return nil, errors.Errorf("failed to restore settings: %w", err)
	}

	// Generate diff
	diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(string(currentContent)),
		B:        difflib.SplitLines(string(backupContent)),
		FromFile: filepath.Base(settingsPath),
		ToFile:   filepath.Base(settingsPath),
		Context:  3,
	})
	if err != nil {
		return nil, errors.Errorf("failed to generate diff: %w", err)
	}

	return parseDiffString(diff), nil
}

// GetRequiredEnvVars returns environment variables for Django deployment
func (h *DjangoHandler) GetRequiredEnvVars(platform Platform) map[string]string {
	envVars := make(map[string]string)

	// Get domain patterns and origins
	domainPatterns := h.getDomainPatterns(platform)
	csrfOrigins := h.getCsrfOrigins(platform)

	// Set env vars (comma-separated values)
	if len(domainPatterns) > 0 {
		envVars["DJANGO_ALLOWED_HOSTS"] = strings.Join(domainPatterns, ",")
	}

	if len(csrfOrigins) > 0 {
		envVars["DJANGO_CSRF_TRUSTED_ORIGINS"] = strings.Join(csrfOrigins, ",")
	}

	return envVars
}

// PrepareDeployment sets Django framework vars in CollectedEnvVars
// This runs AFTER categorization, so it updates/overrides any existing values
func (h *DjangoHandler) PrepareDeployment(plan DeployPlan) DeployPlan {
	domainPatterns := h.getDomainPatterns(plan.Platform)
	csrfOrigins := h.getCsrfOrigins(plan.Platform)

	slog.Info("Django PrepareDeployment",
		"platform", plan.Platform,
		"domainPatterns", domainPatterns,
		"csrfOrigins", csrfOrigins)

	// Find Django vars in the project and set their values
	for _, envVar := range plan.Spec.EnvVars {
		var targetValue string

		if strings.Contains(envVar.VarName, "ALLOWED_HOSTS") {
			targetValue = strings.Join(domainPatterns, ",")
		} else if strings.Contains(envVar.VarName, "CSRF_TRUSTED_ORIGINS") {
			targetValue = strings.Join(csrfOrigins, ",")
		} else {
			continue
		}

		// Look for existing entry and update it
		found := false
		for i := range plan.CollectedEnvVars {
			if plan.CollectedEnvVars[i].Name == envVar.VarName {
				plan.CollectedEnvVars[i].Value = targetValue
				plan.CollectedEnvVars[i].Role = deployment.EnvRoleNotDBRelated
				found = true
				slog.Info("Django framework var configured", "name", envVar.VarName, "value", targetValue)
				break
			}
		}

		// If not found, add it
		if !found {
			plan.CollectedEnvVars = append(plan.CollectedEnvVars, deployment.EnvVar{
				Name:  envVar.VarName,
				Value: targetValue,
				Role:  deployment.EnvRoleNotDBRelated, // Framework vars are not backing service vars
			})
			slog.Info("Django framework var configured", "name", envVar.VarName, "value", targetValue)
		}
	}

	return plan
}
