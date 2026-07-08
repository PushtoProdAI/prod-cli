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
		"drizzle-orm", "drizzle-kit",
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
	"src/server/db", // Custom migration directories
	"server/db",
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
	"drizzle.config.ts",
	"drizzle.config.js",
}

// Custom migration file patterns (for projects with custom migration scripts)
var customMigrationFiles = []string{
	"migrate.ts",
	"migrate.js",
	"migration.ts",
	"migration.js",
	"schema.sql",
	"migrations.sql",
	"db/migrate.ts",
	"db/migrate.js",
	"db/schema.sql",
	"src/db/migrate.ts",
	"src/db/migrate.js",
	"src/db/schema.sql",
	"src/server/db/migrate.ts",
	"src/server/db/migrate.js",
	"src/server/db/schema.sql",
	"server/db/migrate.ts",
	"server/db/migrate.js",
	"server/db/schema.sql",
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

	// Check for custom migration files
	for _, customFile := range customMigrationFiles {
		path := filepath.Join(root, customFile)
		if exists, _ := pathExists(path); exists {
			migrationFiles = append(migrationFiles, path)
		}
	}

	// Search for Django app-level migrations directories
	// Django apps typically have a migrations/ directory inside each app
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip common directories to avoid
		if d.IsDir() {
			name := d.Name()
			// Skip virtual environments, node_modules, hidden dirs, etc.
			if name == "venv" || name == ".venv" || name == "env" || name == ".env" ||
				name == "node_modules" || name == ".git" || name == "__pycache__" ||
				name == ".pytest_cache" || name == ".mypy_cache" || name == "site-packages" {
				return filepath.SkipDir
			}

			// Check if this is a migrations directory
			if name == "migrations" {
				// Verify it's a Django migrations directory by checking for __init__.py
				initPyPath := filepath.Join(path, "__init__.py")
				if exists, _ := pathExists(initPyPath); exists {
					migrationFiles = append(migrationFiles, path)
				}
			}
		}

		return nil
	})
	if err != nil {
		return migrationFiles, err
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

// FilterConfiguredMigrationTools filters detected ORM tools to only those with actual migration setup
func FilterConfiguredMigrationTools(detectedTools []string, migrationFiles []string, rootPath string) []string {
	var configuredTools []string

	// Build a map of what migration files/directories were found
	hasAlembicConfig := false
	hasAlembicVersions := false
	hasDjangoManage := false
	hasFlaskMigrate := false
	hasMigrationsDir := false
	hasRailsMigrations := false // Rails/ActiveRecord keeps migrations under db/migrate

	for _, mf := range migrationFiles {
		mfLower := strings.ToLower(mf)
		if strings.Contains(mfLower, "alembic.ini") {
			hasAlembicConfig = true
		}
		if strings.Contains(mfLower, "alembic/versions") {
			hasAlembicVersions = true
		}
		if strings.Contains(mfLower, "db/migrate") {
			hasRailsMigrations = true
		}
		if strings.Contains(mfLower, "migrations") {
			hasMigrationsDir = true
			// Check if it's Flask-Migrate specifically
			if strings.Contains(mfLower, "migrations/alembic.ini") {
				hasFlaskMigrate = true
			}
		}
	}

	// Check for Django manage.py separately
	managePyPath := filepath.Join(rootPath, "manage.py")
	if _, err := os.Stat(managePyPath); err == nil {
		hasDjangoManage = true
	}

	// Check for Node.js migration-specific files
	hasPrismaSchema := false
	hasTypeORMConfig := false
	hasKnexConfig := false
	hasSequelizeConfig := false

	// Check for specific config files
	prismaSchemaPath := filepath.Join(rootPath, "prisma/schema.prisma")
	if _, err := os.Stat(prismaSchemaPath); err == nil {
		hasPrismaSchema = true
	}

	typeormConfigs := []string{"ormconfig.json", "ormconfig.js", "typeorm.config.ts"}
	for _, config := range typeormConfigs {
		if _, err := os.Stat(filepath.Join(rootPath, config)); err == nil {
			hasTypeORMConfig = true
			break
		}
	}

	knexConfigs := []string{"knexfile.js", "knexfile.ts"}
	for _, config := range knexConfigs {
		if _, err := os.Stat(filepath.Join(rootPath, config)); err == nil {
			hasKnexConfig = true
			break
		}
	}

	sequelizeConfigs := []string{".sequelizerc", "config/config.json", "sequelize.config.js"}
	for _, config := range sequelizeConfigs {
		if _, err := os.Stat(filepath.Join(rootPath, config)); err == nil {
			hasSequelizeConfig = true
			break
		}
	}

	hasDrizzleConfig := false
	drizzleConfigs := []string{"drizzle.config.ts", "drizzle.config.js"}
	for _, config := range drizzleConfigs {
		if _, err := os.Stat(filepath.Join(rootPath, config)); err == nil {
			hasDrizzleConfig = true
			break
		}
	}

	// Filter tools based on actual configuration
	for _, tool := range detectedTools {
		toolLower := strings.ToLower(tool)
		shouldInclude := false

		switch {
		// Python tools
		case strings.Contains(toolLower, "alembic"):
			// Only include alembic if it has config or versions directory
			if hasAlembicConfig || hasAlembicVersions {
				shouldInclude = true
			}
		case strings.Contains(toolLower, "flask-migrate"):
			// Only include Flask-Migrate if it has the migrations directory with config
			if hasFlaskMigrate || (hasMigrationsDir && hasAlembicConfig) {
				shouldInclude = true
			}
		case strings.Contains(toolLower, "django"):
			// Only include Django if it has manage.py AND migrations directories
			if hasDjangoManage && hasMigrationsDir {
				shouldInclude = true
			}

		// Ruby tools
		case strings.Contains(toolLower, "rails") || strings.Contains(toolLower, "activerecord"):
			// Rails/ActiveRecord run migrations only when db/migrate exists
			if hasRailsMigrations {
				shouldInclude = true
			}
		case strings.Contains(toolLower, "sequel") || strings.Contains(toolLower, "rom-sql") ||
			strings.Contains(toolLower, "hanami-model") || strings.Contains(toolLower, "data_mapper"):
			if hasRailsMigrations || hasMigrationsDir {
				shouldInclude = true
			}

		// Node.js tools
		case strings.Contains(toolLower, "prisma"):
			// Only include Prisma if it has schema.prisma or prisma/migrations directory
			if hasPrismaSchema || strings.Contains(strings.Join(migrationFiles, ","), "prisma/migrations") {
				shouldInclude = true
			}
		case strings.Contains(toolLower, "typeorm"):
			// Only include TypeORM if it has config or migrations directory
			if hasTypeORMConfig || hasMigrationsDir {
				shouldInclude = true
			}
		case strings.Contains(toolLower, "knex"):
			// Only include Knex if it has knexfile or migrations
			if hasKnexConfig || hasMigrationsDir {
				shouldInclude = true
			}
		case strings.Contains(toolLower, "sequelize"):
			// Only include Sequelize if it has config or migrations
			if hasSequelizeConfig || hasMigrationsDir {
				shouldInclude = true
			}
		case strings.Contains(toolLower, "drizzle"):
			// Only include Drizzle if it has config file or migrations
			if hasDrizzleConfig || hasMigrationsDir {
				shouldInclude = true
			}

		default:
			// For SQLAlchemy alone (without alembic), or other generic tools,
			// don't include them unless there's evidence of migration files
			if strings.Contains(toolLower, "sqlalchemy") && !hasAlembicConfig && !hasAlembicVersions {
				shouldInclude = false
			} else if hasMigrationsDir {
				// If there are migration files, include the tool and let LLM validate
				shouldInclude = true
			} else {
				// No clear evidence, exclude it
				shouldInclude = false
			}
		}

		if shouldInclude {
			configuredTools = append(configuredTools, tool)
		}
	}

	return configuredTools
}
