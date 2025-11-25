package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetDomainPatterns(t *testing.T) {
	handler := &DjangoHandler{}

	tests := []struct {
		name     string
		platform Platform
		want     []string
	}{
		{
			name:     "FlyIO platform",
			platform: FlyIO,
			want:     []string{".fly.dev"},
		},
		{
			name:     "Heroku platform",
			platform: Heroku,
			want:     []string{".herokuapp.com"},
		},
		{
			name:     "Netlify platform",
			platform: Netlify,
			want:     []string{".netlify.app"},
		},
		{
			name:     "Vercel platform",
			platform: Vercel,
			want:     []string{".vercel.app"},
		},
		{
			name:     "Render platform",
			platform: Render,
			want:     []string{".onrender.com"},
		},
		{
			name:     "AWS platform",
			platform: AWS,
			want:     []string{".awsapprunner.com"},
		},
		{
			name:     "Unknown platform",
			platform: UnknownPlatform,
			want:     []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := handler.getDomainPatterns(tt.platform)
			if len(got) != len(tt.want) {
				t.Errorf("getDomainPatterns() length = %d, want %d", len(got), len(tt.want))
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("getDomainPatterns()[%d] = %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestGetCsrfOrigins(t *testing.T) {
	handler := &DjangoHandler{}

	tests := []struct {
		name     string
		platform Platform
		want     []string
	}{
		{
			name:     "FlyIO platform",
			platform: FlyIO,
			want:     []string{"https://*.fly.dev"},
		},
		{
			name:     "Heroku platform",
			platform: Heroku,
			want:     []string{"https://*.herokuapp.com"},
		},
		{
			name:     "Netlify platform",
			platform: Netlify,
			want:     []string{"https://*.netlify.app"},
		},
		{
			name:     "Vercel platform",
			platform: Vercel,
			want:     []string{"https://*.vercel.app"},
		},
		{
			name:     "Render platform",
			platform: Render,
			want:     []string{"https://*.onrender.com"},
		},
		{
			name:     "AWS platform",
			platform: AWS,
			want:     []string{"https://*.awsapprunner.com"},
		},
		{
			name:     "Unknown platform",
			platform: UnknownPlatform,
			want:     []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := handler.getCsrfOrigins(tt.platform)
			if len(got) != len(tt.want) {
				t.Errorf("getCsrfOrigins() length = %d, want %d", len(got), len(tt.want))
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("getCsrfOrigins()[%d] = %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestFormatStringList(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			name:  "Single item",
			input: []string{".fly.dev"},
			want:  []string{"'.fly.dev'"},
		},
		{
			name:  "Multiple items",
			input: []string{".fly.dev", ".herokuapp.com"},
			want:  []string{"'.fly.dev'", "'.herokuapp.com'"},
		},
		{
			name:  "Empty list",
			input: []string{},
			want:  []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatStringList(tt.input)
			if len(got) != len(tt.want) {
				t.Errorf("formatStringList() length = %d, want %d", len(got), len(tt.want))
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("formatStringList()[%d] = %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestUpdateSettingsFile(t *testing.T) {
	handler := &DjangoHandler{}

	tests := []struct {
		name     string
		input    string
		platform Platform
		want     []string // strings that should be present in output
		notWant  []string // strings that should NOT be present
	}{
		{
			name: "Replace existing ALLOWED_HOSTS",
			input: `import os

DEBUG = True

ALLOWED_HOSTS = ['localhost', '127.0.0.1']

INSTALLED_APPS = [
    'django.contrib.admin',
]`,
			platform: FlyIO,
			want: []string{
				"ALLOWED_HOSTS = ['.fly.dev']",
				"CSRF_TRUSTED_ORIGINS = ['https://*.fly.dev']",
			},
			notWant: []string{
				"'localhost'",
				"'127.0.0.1'",
			},
		},
		{
			name: "Add ALLOWED_HOSTS after DEBUG",
			input: `import os

DEBUG = True

INSTALLED_APPS = [
    'django.contrib.admin',
]`,
			platform: Heroku,
			want: []string{
				"DEBUG = True",
				"# Added by prod CLI for deployment",
				"ALLOWED_HOSTS = ['.herokuapp.com']",
				"CSRF_TRUSTED_ORIGINS = ['https://*.herokuapp.com']",
			},
		},
		{
			name: "Add ALLOWED_HOSTS at end when no DEBUG",
			input: `import os

INSTALLED_APPS = [
    'django.contrib.admin',
]

MIDDLEWARE = [
    'django.middleware.security.SecurityMiddleware',
]`,
			platform: Netlify,
			want: []string{
				"# Added by prod CLI for deployment",
				"ALLOWED_HOSTS = ['.netlify.app']",
			},
		},
		{
			name: "Replace existing CSRF_TRUSTED_ORIGINS",
			input: `DEBUG = True

ALLOWED_HOSTS = ['*']

CSRF_TRUSTED_ORIGINS = ['https://example.com']`,
			platform: Vercel,
			want: []string{
				"ALLOWED_HOSTS = ['.vercel.app']",
				"CSRF_TRUSTED_ORIGINS = ['https://*.vercel.app']",
			},
			notWant: []string{
				"'https://example.com'",
			},
		},
		{
			name: "Handle settings with comments",
			input: `# Django settings
DEBUG = True

# Production hosts
ALLOWED_HOSTS = []  # Add your hosts here`,
			platform: Render,
			want: []string{
				"ALLOWED_HOSTS = ['.onrender.com']",
				"CSRF_TRUSTED_ORIGINS = ['https://*.onrender.com']",
			},
		},
		{
			name: "Handle multi-line ALLOWED_HOSTS",
			input: `DEBUG = True

ALLOWED_HOSTS = [
    'localhost',
    '127.0.0.1',
    'example.com'
]`,
			platform: AWS,
			want: []string{
				"ALLOWED_HOSTS = ['.awsapprunner.com']",
				"CSRF_TRUSTED_ORIGINS = ['https://*.awsapprunner.com']",
			},
			notWant: []string{
				"'localhost'",
				"'example.com'",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp file
			tmpDir := t.TempDir()
			tmpFile := filepath.Join(tmpDir, "settings.py")
			if err := os.WriteFile(tmpFile, []byte(tt.input), 0644); err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}

			// Update settings
			original, updated, err := handler.updateSettingsFile(tmpFile, tt.platform)
			if err != nil {
				t.Fatalf("updateSettingsFile() error = %v", err)
			}

			// Check original matches input
			if string(original) != tt.input {
				t.Errorf("Original content doesn't match input")
			}

			updatedStr := string(updated)

			// Check wanted strings are present
			for _, want := range tt.want {
				if !strings.Contains(updatedStr, want) {
					t.Errorf("Expected output to contain %q\nGot:\n%s", want, updatedStr)
				}
			}

			// Check unwanted strings are NOT present
			for _, notWant := range tt.notWant {
				if strings.Contains(updatedStr, notWant) {
					t.Errorf("Expected output to NOT contain %q\nGot:\n%s", notWant, updatedStr)
				}
			}
		})
	}
}

func TestFindDjangoSettings(t *testing.T) {
	handler := &DjangoHandler{}

	tests := []struct {
		name       string
		setupFiles map[string]string // path -> content
		wantPath   string            // relative path from project root
		wantModule string
		wantErr    bool
	}{
		{
			name: "Find from manage.py DJANGO_SETTINGS_MODULE",
			setupFiles: map[string]string{
				"manage.py": `#!/usr/bin/env python
import os
import sys

if __name__ == "__main__":
    os.environ.setdefault("DJANGO_SETTINGS_MODULE", "myproject.settings")
    from django.core.management import execute_from_command_line
    execute_from_command_line(sys.argv)
`,
				"myproject/settings.py": "# Django settings",
			},
			wantPath:   "myproject/settings.py",
			wantModule: "myproject.settings",
			wantErr:    false,
		},
		{
			name: "Find from manage.py with single quotes",
			setupFiles: map[string]string{
				"manage.py": `#!/usr/bin/env python
import os
if __name__ == '__main__':
    os.environ.setdefault('DJANGO_SETTINGS_MODULE', 'config.settings')
    from django.core.management import execute_from_command_line
`,
				"config/settings.py": "# Django settings",
			},
			wantPath:   "config/settings.py",
			wantModule: "config.settings",
			wantErr:    false,
		},
		{
			name: "Fallback to glob - find base settings",
			setupFiles: map[string]string{
				"myproject/settings/base.py": "# Django settings",
			},
			wantPath: "myproject/settings/base.py",
			wantErr:  false,
		},
		{
			name: "Fallback to glob - find production settings",
			setupFiles: map[string]string{
				"myproject/settings/production.py": "# Django settings",
			},
			wantPath: "myproject/settings/production.py",
			wantErr:  false,
		},
		{
			name: "Fallback to glob - simple settings.py",
			setupFiles: map[string]string{
				"settings.py": "# Django settings",
			},
			wantPath: "settings.py",
			wantErr:  false,
		},
		{
			name:       "No settings file found",
			setupFiles: map[string]string{},
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp project directory
			tmpDir := t.TempDir()

			// Create all setup files
			for path, content := range tt.setupFiles {
				fullPath := filepath.Join(tmpDir, path)
				if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
					t.Fatalf("Failed to create directory: %v", err)
				}
				if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
					t.Fatalf("Failed to create file %s: %v", path, err)
				}
			}

			// Find settings
			gotPath, gotModule, err := handler.findDjangoSettings(tmpDir)

			if (err != nil) != tt.wantErr {
				t.Errorf("findDjangoSettings() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				return
			}

			// Convert absolute path to relative for comparison
			relPath, err := filepath.Rel(tmpDir, gotPath)
			if err != nil {
				t.Fatalf("Failed to get relative path: %v", err)
			}

			if relPath != tt.wantPath {
				t.Errorf("findDjangoSettings() path = %v, want %v", relPath, tt.wantPath)
			}

			if tt.wantModule != "" && gotModule != tt.wantModule {
				t.Errorf("findDjangoSettings() module = %v, want %v", gotModule, tt.wantModule)
			}
		})
	}
}

func TestDjangoHandlerGetName(t *testing.T) {
	handler := &DjangoHandler{}
	if got := handler.GetName(); got != "django" {
		t.Errorf("GetName() = %v, want %v", got, "django")
	}
}

func TestDjangoHandlerGetConfigFilenames(t *testing.T) {
	handler := &DjangoHandler{}
	filenames := handler.GetConfigFilenames()

	// Check that we have some common patterns
	expectedPatterns := []string{
		"settings.py",
		"*/settings.py",
		"config/settings.py",
	}

	for _, expected := range expectedPatterns {
		found := false
		for _, filename := range filenames {
			if filename == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected config filename pattern %q not found in %v", expected, filenames)
		}
	}
}

func TestGetRequiredEnvVars(t *testing.T) {
	handler := &DjangoHandler{}

	tests := []struct {
		name           string
		platform       Platform
		wantAllowedKey string
		wantCsrfKey    string
	}{
		{
			name:           "FlyIO platform",
			platform:       FlyIO,
			wantAllowedKey: "DJANGO_ALLOWED_HOSTS",
			wantCsrfKey:    "DJANGO_CSRF_TRUSTED_ORIGINS",
		},
		{
			name:           "Heroku platform",
			platform:       Heroku,
			wantAllowedKey: "DJANGO_ALLOWED_HOSTS",
			wantCsrfKey:    "DJANGO_CSRF_TRUSTED_ORIGINS",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := handler.GetRequiredEnvVars(tt.platform)

			// Check that expected keys exist
			if _, ok := got[tt.wantAllowedKey]; !ok {
				t.Errorf("Expected env var %q not found", tt.wantAllowedKey)
			}

			if _, ok := got[tt.wantCsrfKey]; !ok {
				t.Errorf("Expected env var %q not found", tt.wantCsrfKey)
			}

			// Check that values are not empty
			if got[tt.wantAllowedKey] == "" {
				t.Errorf("Expected non-empty value for %q", tt.wantAllowedKey)
			}

			if got[tt.wantCsrfKey] == "" {
				t.Errorf("Expected non-empty value for %q", tt.wantCsrfKey)
			}
		})
	}
}

func TestHandleConfig(t *testing.T) {
	handler := &DjangoHandler{}

	tests := []struct {
		name         string
		settingsFile string
		platform     Platform
		wantDiff     bool
		wantContains []string
	}{
		{
			name: "Successful configuration update",
			settingsFile: `DEBUG = True

ALLOWED_HOSTS = []

INSTALLED_APPS = [
    'django.contrib.admin',
]`,
			platform: FlyIO,
			wantDiff: true,
			wantContains: []string{
				"ALLOWED_HOSTS = ['.fly.dev']",
				"CSRF_TRUSTED_ORIGINS = ['https://*.fly.dev']",
			},
		},
		{
			name: "Update for Heroku platform",
			settingsFile: `DEBUG = True

ALLOWED_HOSTS = ['localhost']`,
			platform: Heroku,
			wantDiff: true,
			wantContains: []string{
				"ALLOWED_HOSTS = ['.herokuapp.com']",
				"CSRF_TRUSTED_ORIGINS = ['https://*.herokuapp.com']",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp project directory with settings
			tmpDir := t.TempDir()
			settingsPath := filepath.Join(tmpDir, "settings.py")
			if err := os.WriteFile(settingsPath, []byte(tt.settingsFile), 0644); err != nil {
				t.Fatalf("Failed to create settings file: %v", err)
			}

			// Handle config
			diffs, configPath, err := handler.HandleConfig(tmpDir, tt.platform)
			if err != nil {
				t.Fatalf("HandleConfig() error = %v", err)
			}

			// Check that config path is correct
			if configPath != settingsPath {
				t.Errorf("HandleConfig() configPath = %v, want %v", configPath, settingsPath)
			}

			// Check if diff was generated
			if tt.wantDiff && len(diffs) == 0 {
				t.Error("Expected diff to be generated, got empty diff")
			}

			// Check that backup was created (.prod directory should exist)
			prodDir := filepath.Join(tmpDir, ".prod")
			if _, err := os.Stat(prodDir); os.IsNotExist(err) {
				t.Error("Expected .prod directory to be created")
			} else {
				// Check that at least one backup file exists
				files, err := os.ReadDir(prodDir)
				if err != nil {
					t.Fatalf("Failed to read .prod directory: %v", err)
				}
				hasBackup := false
				for _, file := range files {
					if strings.HasPrefix(file.Name(), "settings.py.") && strings.HasSuffix(file.Name(), ".bak") {
						hasBackup = true
						break
					}
				}
				if !hasBackup {
					t.Error("Expected backup file to be created in .prod directory")
				}
			}

			// Check updated file content
			updatedContent, err := os.ReadFile(settingsPath)
			if err != nil {
				t.Fatalf("Failed to read updated settings: %v", err)
			}
			updatedStr := string(updatedContent)

			for _, want := range tt.wantContains {
				if !strings.Contains(updatedStr, want) {
					t.Errorf("Expected updated file to contain %q\nGot:\n%s", want, updatedStr)
				}
			}
		})
	}
}

func TestHandleConfigNoSettingsFile(t *testing.T) {
	handler := &DjangoHandler{}

	// Create empty temp directory
	tmpDir := t.TempDir()

	// Should return error when no settings file found
	_, _, err := handler.HandleConfig(tmpDir, FlyIO)
	if err == nil {
		t.Error("Expected error when settings file not found")
	}
}
