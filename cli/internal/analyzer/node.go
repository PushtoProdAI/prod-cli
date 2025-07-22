package analyzer

import (
	"encoding/json"
	"errors"
	"io/fs"
)

type PackageJson struct {
	Name            string
	Scripts         map[string]string
	Dependencies    map[string]string
	DevDependencies map[string]string
}

type NodeAnalyzer struct {
	ProjectFS fs.FS
}

var NodeServiceMappings = map[string]ServiceRequirement{
	// Database drivers
	"pg":       ServicePostgres,
	"postgres": ServicePostgres,
	"mysql":    ServiceMySQL,
	"mysql2":   ServiceMySQL,
	"mongodb":  ServiceMongo,
	"mongoose": ServiceMongo,
	"sqlite3":  ServiceSQLite,

	// Cache/Session stores
	"redis":         ServiceRedis,
	"ioredis":       ServiceRedis,
	"connect-redis": ServiceRedis,
	"memcached":     {Type: "cache", Provider: "memcached"},

	// Queue systems
	"bull":       {Type: "queue", Provider: "redis"},
	"bull-queue": {Type: "queue", Provider: "redis"},
	"agenda":     {Type: "queue", Provider: "mongodb"},
	"kue":        {Type: "queue", Provider: "redis"},
	"amqplib":    {Type: "queue", Provider: "rabbitmq"},

	// Search engines
	"@elastic/elasticsearch": {Type: "search", Provider: "elasticsearch"},
	"elasticsearch":          {Type: "search", Provider: "elasticsearch"},

	// Email services
	"nodemailer": {Type: "email", Provider: "smtp"},
	"sendgrid":   {Type: "email", Provider: "sendgrid"},

	// File storage
	"aws-sdk":            {Type: "storage", Provider: "s3"},
	"@aws-sdk/client-s3": {Type: "storage", Provider: "s3"},

	// Monitoring
	"newrelic":     {Type: "monitoring", Provider: "newrelic"},
	"@sentry/node": {Type: "monitoring", Provider: "sentry"},
}

func NewNodeAnalyzer(projectFS fs.FS) Analyzer {
	return &NodeAnalyzer{
		ProjectFS: projectFS,
	}
}

func (n *NodeAnalyzer) CanHandle() (bool, error) {
	if _, err := fs.Stat(n.ProjectFS, "package.json"); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		} else {
			return false, err
		}
	}

	return true, nil
}

func (n *NodeAnalyzer) Analyze() (*ProjectSpec, error) {
	pkgJson, err := unmarshalPkgJson(n.ProjectFS)
	if err != nil {
		return nil, err
	}

	serviceRequirements, err := getDepsPkgJson(pkgJson)
	if err != nil {
		return nil, err
	}

	return &ProjectSpec{
		Name:                pkgJson.Name,
		Language:            "node",
		ServiceRequirements: serviceRequirements,
		// TODO Analyze for these
		BuildCommand: "npm run build",
		StartCommand: "npm run start",
	}, nil
}

func getDepsPkgJson(pkgJson *PackageJson) ([]ServiceRequirement, error) {
	var services []ServiceRequirement

	for dep := range pkgJson.Dependencies {
		if service, exists := NodeServiceMappings[dep]; exists {
			services = append(services, service)
		}
	}

	return services, nil
}

func unmarshalPkgJson(projectFS fs.FS) (*PackageJson, error) {
	data, err := fs.ReadFile(projectFS, "package.json")
	if err != nil {
		return nil, err
	}

	var pkg PackageJson

	if err = json.Unmarshal(data, &pkg); err != nil {
		return nil, err
	}

	return &pkg, nil
}
