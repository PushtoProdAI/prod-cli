package analyzer

import (
	"os"
	"path/filepath"
	"strings"
)

// MigrationContext contains information about database migration tools and files
type MigrationContext struct {
	MigrationFiles []string          // Paths to migration files
	ORMTools       []string          // Detected ORM/migration tools
	ConfigFiles    map[string]string // Config file name -> content snippet
	PackageScripts map[string]string // Script name -> command (from package.json)
}

// Common migration tool patterns for different languages
var migrationToolPatterns = map[string][]string{
	"node": {
		"prisma", "typeorm", "sequelize", "knex", "mikro-orm",
		"@prisma/client", "typeorm", "sequelize", "knex",
		"db-migrate", "node-pg-migrate", "umzug",
	},
	"python": {
		"django", "alembic", "flask-migrate", "sqlalchemy",
		"peewee", "orator", "tortoise-orm", "yoyo-migrations",
	},
	"ruby": {
		"rails", "activerecord", "sequel", "rom-sql",
		"hanami-model", "data_mapper",
	},
}

// Common migration directory patterns
var migrationDirPatterns = []string{
	"migrations",
	"db/migrate",
	"db/migrations",
	"database/migrations",
	"prisma/migrations",
	"src/migrations",
	"alembic/versions",
}

// Common migration config files
var migrationConfigFiles = []string{
	"alembic.ini",
	"knexfile.js",
	"knexfile.ts",
	"ormconfig.json",
	"ormconfig.js",
	"typeorm.config.ts",
	"sequelize.config.js",
	"database.json",
	"prisma/schema.prisma",
	"mikro-orm.config.ts",
	"mikro-orm.config.js",
}

// FindMigrationFiles searches for migration-related files in the project
func FindMigrationFiles(root string) ([]string, error) {
	var migrationFiles []string

	// Check for common migration directories
	for _, pattern := range migrationDirPatterns {
		path := filepath.Join(root, pattern)
		if exists, _ := pathExists(path); exists {
			migrationFiles = append(migrationFiles, path)
		}
	}

	// Check for migration config files
	for _, configFile := range migrationConfigFiles {
		path := filepath.Join(root, configFile)
		if exists, _ := pathExists(path); exists {
			migrationFiles = append(migrationFiles, path)
		}
	}

	return migrationFiles, nil
}

// DetectORMTools detects ORM and migration tools based on dependencies
func DetectORMTools(dependencies []string, language string) []string {
	var tools []string
	toolSet := make(map[string]bool)

	patterns, exists := migrationToolPatterns[language]
	if !exists {
		return tools
	}

	for _, dep := range dependencies {
		depLower := strings.ToLower(dep)
		for _, pattern := range patterns {
			if strings.Contains(depLower, pattern) {
				if !toolSet[pattern] {
					tools = append(tools, pattern)
					toolSet[pattern] = true
				}
			}
		}
	}

	return tools
}

// ExtractMigrationScripts extracts migration-related scripts from package.json
func ExtractMigrationScripts(scripts map[string]string) map[string]string {
	migrationScripts := make(map[string]string)

	migrationKeywords := []string{
		"migrate", "migration", "db:", "database:",
		"typeorm", "prisma", "sequelize", "knex",
		"schema", "seed",
	}

	for name, command := range scripts {
		nameLower := strings.ToLower(name)
		for _, keyword := range migrationKeywords {
			if strings.Contains(nameLower, keyword) {
				migrationScripts[name] = command
				break
			}
		}
	}

	return migrationScripts
}

// Helper function to check if path exists
func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}