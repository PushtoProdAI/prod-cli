package analyzer

import (
	"errors"
	"io/fs"
	"os"
)

type Analyzer interface {
	CanHandle() (bool, error)
	Analyze() (*ProjectSpec, error)
}

type ProjectSpec struct {
	Name                string
	Language            string
	ServiceRequirements []ServiceRequirement
	BuildCommand        string
	StartCommand        string
}

type ServiceRequirement struct {
	Type     string `json:"type"`
	Provider string `json:"provider"`
}

const (
	TypeDatabase   = "database"
	TypeCache      = "cache"
	TypeStorage    = "storage"
	TypeMonitoring = "monitoring"
)

const (
	ProviderPostgres = "postgresql"
	ProviderMySQL    = "mysql"
	ProviderMongo    = "mongodb"
	ProviderSQLite   = "sqlite"
	ProviderRedis    = "redis"
)

var (
	ServicePostgres ServiceRequirement = ServiceRequirement{Type: TypeDatabase, Provider: ProviderPostgres}
	ServiceMySQL    ServiceRequirement = ServiceRequirement{Type: TypeDatabase, Provider: ProviderMySQL}
	ServiceMongo    ServiceRequirement = ServiceRequirement{Type: TypeDatabase, Provider: ProviderMongo}
	ServiceSQLite   ServiceRequirement = ServiceRequirement{Type: TypeDatabase, Provider: ProviderSQLite}
	ServiceRedis    ServiceRequirement = ServiceRequirement{Type: TypeCache, Provider: ProviderRedis}
)

var analyzers = []func(fs.FS) Analyzer{
	NewNodeAnalyzer,
	NewPythonAnalyzer,
	// TODO add more analyzers here
}

func GetAnalyzer(projectPath string) (Analyzer, error) {
	projectFS := os.DirFS(projectPath)

	for _, newAnalyzer := range analyzers {
		analyzer := newAnalyzer(projectFS)
		canHandle, err := analyzer.CanHandle()
		if err != nil {
			return nil, err
		}

		if canHandle {
			return analyzer, nil
		}
	}

	return nil, errors.New("no supported project type found")
}
