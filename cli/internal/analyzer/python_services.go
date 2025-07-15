package analyzer

import "fmt"

var PythonServiceMappings = map[string]ServiceRequirement{
	// Database drivers
	"psycopg2":               ServicePostgres,
	"psycopg2-binary":        ServicePostgres,
	"asyncpg":                ServicePostgres,
	"pymysql":                ServiceMySQL,
	"mysql-connector-python": ServiceMySQL,
	"pymongo":                ServiceMongo,
	"motor":                  ServiceMongo,
	"sqlite3":                ServiceSQLite,

	// Cache/Session stores
	"redis":        ServiceRedis,
	"aioredis":     ServiceRedis,
	"django-redis": ServiceRedis,

	// Web frameworks
	"django":   {Type: "framework", Provider: "django"},
	"flask":    {Type: "framework", Provider: "flask"},
	"fastapi":  {Type: "framework", Provider: "fastapi"},
	"uvicorn":  {Type: "framework", Provider: "fastapi"},
	"gunicorn": {Type: "framework", Provider: "wsgi"},

	// Database ORMs
	"sqlalchemy": {Type: "orm", Provider: "sqlalchemy"},
	"django-orm": {Type: "orm", Provider: "django"},
	"peewee":     {Type: "orm", Provider: "peewee"},

	// Search engines
	"elasticsearch": {Type: "search", Provider: "elasticsearch"},
	"opensearch-py": {Type: "search", Provider: "opensearch"},

	// Email services
	"django-email": {Type: "email", Provider: "django"},
	"sendgrid":     {Type: "email", Provider: "sendgrid"},

	// File storage
	"boto3":                {Type: "storage", Provider: "s3"},
	"google-cloud-storage": {Type: "storage", Provider: "gcs"},

	// Monitoring
	"newrelic":   {Type: "monitoring", Provider: "newrelic"},
	"sentry-sdk": {Type: "monitoring", Provider: "sentry"},
}

func (p *PythonAnalyzer) extractServiceRequirements(dependencies []Dependency) ([]ServiceRequirement, error) {
	var services []ServiceRequirement
	seen := make(map[string]bool)

	for _, dep := range dependencies {
		if service, exists := PythonServiceMappings[dep.Name]; exists {
			key := fmt.Sprintf("%s-%s", service.Type, service.Provider)
			if !seen[key] {
				services = append(services, service)
				seen[key] = true
			}
		}
	}

	return services, nil
}
