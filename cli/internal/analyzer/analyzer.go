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
	MigrationCommand    string
	EnvVars             []EnvVarCandidate
	Routes              []RouteCandidate
	BuildOutput         BuildOutputCandidate
	LaunchContext       LaunchContext
	MigrationContext    MigrationContext
	// DetectedShape is the deploy shape inferred from code ("mcp-server" | "worker"), or ""
	// when the code isn't conclusive. When set it's a strong prior the planner lets win over
	// the LLM's guess. See DetectAgentShape.
	DetectedShape string
}

type ServiceRequirement struct {
	Type     string `json:"type"`
	Provider string `json:"provider"`
}

type projectFS struct {
	fs.FS
	rootPath string
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

var analyzers = []func(projectFS) Analyzer{
	NewNodeAnalyzer,
	NewPythonAnalyzer,
	NewGoAnalyzer,
	NewRubyAnalyzer,
	NewRustAnalyzer,
	// TODO add more analyzers here
}

func GetAnalyzer(projectPath string) (Analyzer, error) {
	pFS := os.DirFS(projectPath)

	for _, newAnalyzer := range analyzers {
		analyzer := newAnalyzer(projectFS{FS: pFS, rootPath: projectPath})
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
