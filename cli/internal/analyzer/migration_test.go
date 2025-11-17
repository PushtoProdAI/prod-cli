package analyzer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilterConfiguredMigrationTools(t *testing.T) {
	tests := []struct {
		name           string
		detectedTools  []string
		migrationFiles []string
		setupFunc      func(rootPath string)
		expected       []string
	}{
		{
			name:           "alembic in deps but no config - should be filtered out",
			detectedTools:  []string{"alembic", "sqlalchemy"},
			migrationFiles: []string{},
			setupFunc:      func(rootPath string) {},
			expected:       []string{},
		},
		{
			name:          "alembic with alembic.ini - should be included",
			detectedTools: []string{"alembic", "sqlalchemy"},
			migrationFiles: []string{
				"alembic.ini",
			},
			setupFunc: func(rootPath string) {
				os.WriteFile(filepath.Join(rootPath, "alembic.ini"), []byte("[alembic]"), 0644)
			},
			expected: []string{"alembic"},
		},
		{
			name:          "alembic with versions directory - should be included",
			detectedTools: []string{"alembic"},
			migrationFiles: []string{
				"alembic/versions",
			},
			setupFunc: func(rootPath string) {
				os.MkdirAll(filepath.Join(rootPath, "alembic/versions"), 0755)
			},
			expected: []string{"alembic"},
		},
		{
			name:           "django with manage.py but no migrations dir - should be filtered out",
			detectedTools:  []string{"django"},
			migrationFiles: []string{},
			setupFunc: func(rootPath string) {
				os.WriteFile(filepath.Join(rootPath, "manage.py"), []byte("#!/usr/bin/env python"), 0755)
			},
			expected: []string{},
		},
		{
			name:          "django with manage.py and migrations - should be included",
			detectedTools: []string{"django"},
			migrationFiles: []string{
				"migrations",
			},
			setupFunc: func(rootPath string) {
				os.WriteFile(filepath.Join(rootPath, "manage.py"), []byte("#!/usr/bin/env python"), 0755)
				os.MkdirAll(filepath.Join(rootPath, "migrations"), 0755)
			},
			expected: []string{"django"},
		},
		{
			name:          "django with manage.py and app-level migrations - should be included",
			detectedTools: []string{"django"},
			migrationFiles: []string{
				"calendar/migrations",
				"users/migrations",
			},
			setupFunc: func(rootPath string) {
				os.WriteFile(filepath.Join(rootPath, "manage.py"), []byte("#!/usr/bin/env python"), 0755)
				os.MkdirAll(filepath.Join(rootPath, "calendar/migrations"), 0755)
				os.WriteFile(filepath.Join(rootPath, "calendar/migrations/__init__.py"), []byte(""), 0644)
				os.MkdirAll(filepath.Join(rootPath, "users/migrations"), 0755)
				os.WriteFile(filepath.Join(rootPath, "users/migrations/__init__.py"), []byte(""), 0644)
			},
			expected: []string{"django"},
		},
		{
			name:           "prisma in deps but no schema - should be filtered out",
			detectedTools:  []string{"prisma", "@prisma/client"},
			migrationFiles: []string{},
			setupFunc:      func(rootPath string) {},
			expected:       []string{},
		},
		{
			name:          "prisma with schema.prisma - should be included",
			detectedTools: []string{"prisma"},
			migrationFiles: []string{
				"prisma/schema.prisma",
			},
			setupFunc: func(rootPath string) {
				os.MkdirAll(filepath.Join(rootPath, "prisma"), 0755)
				os.WriteFile(filepath.Join(rootPath, "prisma/schema.prisma"), []byte("datasource db {}"), 0644)
			},
			expected: []string{"prisma"},
		},
		{
			name:          "prisma with migrations directory - should be included",
			detectedTools: []string{"prisma"},
			migrationFiles: []string{
				"prisma/migrations",
			},
			setupFunc: func(rootPath string) {
				os.MkdirAll(filepath.Join(rootPath, "prisma/migrations"), 0755)
			},
			expected: []string{"prisma"},
		},
		{
			name:           "typeorm without config or migrations - should be filtered out",
			detectedTools:  []string{"typeorm"},
			migrationFiles: []string{},
			setupFunc:      func(rootPath string) {},
			expected:       []string{},
		},
		{
			name:          "typeorm with ormconfig.json - should be included",
			detectedTools: []string{"typeorm"},
			migrationFiles: []string{
				"migrations",
			},
			setupFunc: func(rootPath string) {
				os.WriteFile(filepath.Join(rootPath, "ormconfig.json"), []byte("{}"), 0644)
			},
			expected: []string{"typeorm"},
		},
		{
			name:          "sqlalchemy alone without alembic setup - should be filtered out",
			detectedTools: []string{"sqlalchemy"},
			migrationFiles: []string{
				"migrations",
			},
			setupFunc: func(rootPath string) {
				os.MkdirAll(filepath.Join(rootPath, "migrations"), 0755)
			},
			expected: []string{},
		},
		{
			name:           "drizzle without config - should be filtered out",
			detectedTools:  []string{"drizzle-orm", "drizzle-kit"},
			migrationFiles: []string{},
			setupFunc:      func(rootPath string) {},
			expected:       []string{},
		},
		{
			name:          "drizzle with config - should be included",
			detectedTools: []string{"drizzle-orm"},
			migrationFiles: []string{
				"drizzle.config.ts",
			},
			setupFunc: func(rootPath string) {
				os.WriteFile(filepath.Join(rootPath, "drizzle.config.ts"), []byte("export default {}"), 0644)
			},
			expected: []string{"drizzle-orm"},
		},
		{
			name:          "drizzle with migrations directory - should be included",
			detectedTools: []string{"drizzle-kit"},
			migrationFiles: []string{
				"migrations",
			},
			setupFunc: func(rootPath string) {
				os.MkdirAll(filepath.Join(rootPath, "migrations"), 0755)
				os.WriteFile(filepath.Join(rootPath, "drizzle.config.ts"), []byte("export default {}"), 0644)
			},
			expected: []string{"drizzle-kit"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary directory for test
			tmpDir, err := os.MkdirTemp("", "migration-filter-test-*")
			require.NoError(t, err)
			defer os.RemoveAll(tmpDir)

			// Setup test files/directories
			tt.setupFunc(tmpDir)

			// Run the filter
			result := FilterConfiguredMigrationTools(tt.detectedTools, tt.migrationFiles, tmpDir)

			// Verify result
			assert.ElementsMatch(t, tt.expected, result, "Filtered tools should match expected")
		})
	}
}

func TestFilterConfiguredMigrationTools_HockeyScoresApp(t *testing.T) {
	// Simulate the hockey-scores-app scenario:
	// - Has alembic in requirements.txt
	// - Has NO alembic.ini
	// - Has NO alembic/versions directory
	// - Uses Base.metadata.create_all() for schema creation

	tmpDir, err := os.MkdirTemp("", "hockey-app-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Simulate the detected tools (alembic was in requirements.txt)
	detectedTools := []string{"alembic", "sqlalchemy"}

	// No migration files found
	migrationFiles := []string{}

	// Run the filter
	result := FilterConfiguredMigrationTools(detectedTools, migrationFiles, tmpDir)

	// Should NOT include alembic since there's no config or versions directory
	assert.Empty(t, result, "Should not suggest migration command when alembic is installed but not configured")
}

func TestFindMigrationFiles_CustomMigrations(t *testing.T) {
	tests := []struct {
		name          string
		setupFiles    []string
		expectedFiles []string
	}{
		{
			name: "detects custom migrate.ts file",
			setupFiles: []string{
				"src/server/db/migrate.ts",
				"src/server/db/schema.sql",
			},
			expectedFiles: []string{
				"src/server/db/migrate.ts",
				"src/server/db/schema.sql",
				"src/server/db",
			},
		},
		{
			name: "detects db/migrate.js and schema.sql",
			setupFiles: []string{
				"db/migrate.js",
				"db/schema.sql",
			},
			expectedFiles: []string{
				"db/migrate.js",
				"db/schema.sql",
			},
		},
		{
			name: "detects root level migrate files",
			setupFiles: []string{
				"migrate.ts",
				"schema.sql",
			},
			expectedFiles: []string{
				"migrate.ts",
				"schema.sql",
			},
		},
		{
			name: "detects Django app-level migrations",
			setupFiles: []string{
				"calendar/migrations/__init__.py",
				"users/migrations/__init__.py",
			},
			expectedFiles: []string{
				"calendar/migrations",
				"users/migrations",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir, err := os.MkdirTemp("", "custom-migration-test-*")
			require.NoError(t, err)
			defer os.RemoveAll(tmpDir)

			// Create test files
			for _, file := range tt.setupFiles {
				fullPath := filepath.Join(tmpDir, file)
				dir := filepath.Dir(fullPath)
				os.MkdirAll(dir, 0755)

				if filepath.Ext(file) == "" {
					// It's a directory
					os.MkdirAll(fullPath, 0755)
				} else {
					// It's a file
					os.WriteFile(fullPath, []byte("test content"), 0644)
				}
			}

			// Find migration files
			found, err := FindMigrationFiles(tmpDir)
			require.NoError(t, err)

			// Convert to relative paths for comparison
			var foundRelative []string
			for _, f := range found {
				rel, _ := filepath.Rel(tmpDir, f)
				foundRelative = append(foundRelative, rel)
			}

			// Check that all expected files were found
			for _, expected := range tt.expectedFiles {
				assert.Contains(t, foundRelative, expected, "Should find %s", expected)
			}
		})
	}
}

func TestExtractMigrationScripts_CustomScripts(t *testing.T) {
	scripts := map[string]string{
		"db:migrate":   "node dist/server/db/migrate.js",
		"start":        "node dist/server/index.js",
		"build":        "tsc",
		"migrate:up":   "tsx src/db/migrate.ts",
		"schema:apply": "psql < schema.sql",
	}

	result := ExtractMigrationScripts(scripts)

	// Should extract migration-related scripts
	assert.Contains(t, result, "db:migrate", "Should extract db:migrate")
	assert.Contains(t, result, "migrate:up", "Should extract migrate:up")
	assert.Contains(t, result, "schema:apply", "Should extract schema:apply")

	// Should NOT extract non-migration scripts
	assert.NotContains(t, result, "start", "Should not extract start")
	assert.NotContains(t, result, "build", "Should not extract build")

	// Verify the commands are correct
	assert.Equal(t, "node dist/server/db/migrate.js", result["db:migrate"])
	assert.Equal(t, "tsx src/db/migrate.ts", result["migrate:up"])
}

func TestHockeyScoresApp_WithAlembicConfigured(t *testing.T) {
	// Test with the actual hockey-scores-app path after alembic was added
	projectPath := "/Users/nstehr/code/demo_projects/hockey-scores-app"

	// Skip if path doesn't exist (for CI/CD)
	if _, err := os.Stat(projectPath); os.IsNotExist(err) {
		t.Skip("Hockey scores app not found, skipping integration test")
	}

	// Test FindMigrationFiles
	files, err := FindMigrationFiles(projectPath)
	require.NoError(t, err)

	t.Logf("Found %d migration files:", len(files))
	for _, f := range files {
		t.Logf("  - %s", f)
	}

	// Should find alembic.ini and alembic/versions
	foundAlembicIni := false
	foundVersions := false
	for _, f := range files {
		if strings.Contains(f, "alembic.ini") {
			foundAlembicIni = true
		}
		if strings.Contains(f, "alembic/versions") {
			foundVersions = true
		}
	}

	assert.True(t, foundAlembicIni, "Should find alembic.ini")
	assert.True(t, foundVersions, "Should find alembic/versions")

	// Test ORM detection (simulating requirements.txt parsing)
	deps := []string{"alembic", "sqlalchemy", "fastapi", "psycopg2-binary"}
	ormTools := DetectORMTools(deps, "python")

	t.Logf("Detected ORM tools: %v", ormTools)
	assert.Contains(t, ormTools, "alembic")

	// Test filtering - this is the critical part
	filtered := FilterConfiguredMigrationTools(ormTools, files, projectPath)

	t.Logf("Filtered tools: %v", filtered)
	assert.Contains(t, filtered, "alembic", "Alembic should be included when alembic.ini and alembic/versions exist")
	assert.NotEmpty(t, filtered, "Should return non-empty list when alembic is properly configured")
}
