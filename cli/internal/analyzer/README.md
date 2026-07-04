# Project Analyzers

This package provides static analyzers for detecting project types, dependencies, and service requirements. Currently supports Node.js and Python projects.

## Overview

The analyzer system uses a plugin architecture where each analyzer implements the `Analyzer` interface:

```go
type Analyzer interface {
    CanHandle() (bool, error)
    Analyze() (*ProjectSpec, error)
}
```

## Supported Analyzers

### Node.js Analyzer (`node.go`)

Detects Node.js projects and extracts dependency information from `package.json`.

#### Detection Criteria
- Presence of `package.json` file

#### Supported Package Managers
- npm (default)
- yarn (detected via `yarn.lock`)
- pnpm (detected via `pnpm-lock.yaml`)

#### Service Mappings
The Node analyzer maps common npm packages to service requirements:

**Databases:**
- `pg`, `postgres` → PostgreSQL
- `mysql`, `mysql2` → MySQL
- `mongodb`, `mongoose` → MongoDB
- `sqlite3` → SQLite

**Cache/Session:**
- `redis`, `ioredis`, `connect-redis` → Redis
- `memcached` → Memcached

**Queue Systems:**
- `bull`, `bull-queue`, `kue` → Redis-based queues
- `agenda` → MongoDB-based queues
- `amqplib` → RabbitMQ

**Search:**
- `@elastic/elasticsearch`, `elasticsearch` → Elasticsearch

**Email:**
- `nodemailer` → SMTP
- `sendgrid` → SendGrid

**Storage:**
- `aws-sdk`, `@aws-sdk/client-s3` → AWS S3

**Monitoring:**
- `newrelic` → New Relic
- `@sentry/node` → Sentry

#### Usage Example

```go
package main

import (
    "fmt"
    "log"
    "github.com/pushtoprodai/prod-cli/internal/analyzer"
)

func main() {
    // Get analyzer for a project directory
    analyzer, err := analyzer.GetAnalyzer("./my-node-project")
    if err != nil {
        log.Fatal(err)
    }

    // Analyze the project
    spec, err := analyzer.Analyze()
    if err != nil {
        log.Fatal(err)
    }

    fmt.Printf("Project: %s\n", spec.Name)
    fmt.Printf("Language: %s\n", spec.Language)
    fmt.Printf("Services: %+v\n", spec.ServiceRequirements)
}
```

### Python Analyzer

Detects Python projects and extracts dependency information from multiple package formats.

#### Detection Criteria
- Presence of Python-specific files:
  - `requirements.txt`
  - `Pipfile`
  - `pyproject.toml`
  - `setup.py`
  - `.python-version`
  - `runtime.txt`
- Presence of `.py` files in the project

#### Supported Package Managers
- **pip** (default) - via `requirements.txt`
- **poetry** - via `pyproject.toml`
- **pipenv** - via `Pipfile`

#### Runtime Detection
The Python analyzer detects runtime information from multiple sources:

1. **`.python-version`** - Direct version specification
2. **`runtime.txt`** - Heroku-style runtime specification (e.g., `python-3.9.0`)
3. **`pyproject.toml`** - Poetry project configuration
4. **`Pipfile`** - Pipenv configuration

#### Dependency Parsing

**requirements.txt:**
```
flask==2.0.1
redis>=4.0.0
psycopg2-binary==2.9.0
```

**Pipfile:**
```toml
[[source]]
url = "https://pypi.org/simple"
verify_ssl = true
name = "pypi"

[packages]
flask = "*"
redis = "*"
```

**pyproject.toml (Poetry):**
```toml
[tool.poetry]
name = "my-project"
version = "0.1.0"

[tool.poetry.dependencies]
flask = "*"
redis = "*"
```

#### Framework Detection
The Python analyzer detects web frameworks through:

1. **Dependencies** - Checks for framework packages in dependencies
2. **File patterns** - Looks for framework-specific files:
   - `manage.py` → Django
   - `app.py` with Flask/FastAPI imports → Flask/FastAPI

**Supported Frameworks:**
- Django
- Flask
- FastAPI

#### Service Mappings

**Databases:**
- `psycopg2`, `psycopg2-binary`, `asyncpg` → PostgreSQL
- `pymysql`, `mysql-connector-python` → MySQL
- `pymongo`, `motor` → MongoDB
- `sqlite3` → SQLite

**Cache/Session:**
- `redis`, `aioredis`, `django-redis` → Redis

**Web Frameworks:**
- `django` → Django
- `flask` → Flask
- `fastapi`, `uvicorn` → FastAPI
- `gunicorn` → WSGI

**ORMs:**
- `sqlalchemy` → SQLAlchemy
- `django-orm` → Django ORM
- `peewee` → Peewee

**Search:**
- `elasticsearch` → Elasticsearch
- `opensearch-py` → OpenSearch

**Email:**
- `django-email` → Django email
- `sendgrid` → SendGrid

**Storage:**
- `boto3` → AWS S3
- `google-cloud-storage` → Google Cloud Storage

**Monitoring:**
- `newrelic` → New Relic
- `sentry-sdk` → Sentry

#### Usage Example

```go
package main

import (
    "fmt"
    "log"
    "github.com/pushtoprodai/prod-cli/internal/analyzer"
)

func main() {
    // Get analyzer for a Python project
    analyzer, err := analyzer.GetAnalyzer("./my-python-project")
    if err != nil {
        log.Fatal(err)
    }

    // Analyze the project
    spec, err := analyzer.Analyze()
    if err != nil {
        log.Fatal(err)
    }

    fmt.Printf("Project: %s\n", spec.Name)
    fmt.Printf("Language: %s\n", spec.Language)
    fmt.Printf("Services: %+v\n", spec.ServiceRequirements)
}
```

## Project Structure

```
internal/analyzer/
├── analyzer.go          # Main analyzer interface and registry
├── node.go             # Node.js analyzer implementation
├── python.go           # Python analyzer main struct and interface
├── python_runtime.go   # Python runtime detection
├── python_dependencies.go # Python dependency parsing
├── python_framework.go # Python framework detection
├── python_services.go  # Python service mappings
├── python_utils.go     # Python utility functions
├── node_test.go        # Node.js analyzer tests
└── python_test.go      # Python analyzer tests
```

## Adding New Analyzers

To add support for a new language or framework:

1. **Create a new analyzer file** (e.g., `go.go` for Go projects)
2. **Implement the `Analyzer` interface:**
   ```go
   type GoAnalyzer struct {
       ProjectFS fs.FS
   }

   func NewGoAnalyzer(projectFS fs.FS) Analyzer {
       return &GoAnalyzer{ProjectFS: projectFS}
   }

   func (g *GoAnalyzer) CanHandle() (bool, error) {
       // Check for go.mod, go.sum, etc.
   }

   func (g *GoAnalyzer) Analyze() (*ProjectSpec, error) {
       // Extract dependencies and detect services
   }
   ```

3. **Register the analyzer** in `analyzer.go`:
   ```go
   var analyzers = []func(fs.FS) Analyzer{
       NewNodeAnalyzer,
       NewPythonAnalyzer,
       NewGoAnalyzer, // Add your new analyzer
   }
   ```

4. **Add tests** for your analyzer

## Output Format

All analyzers return a `ProjectSpec` with the following structure:

```go
type ProjectSpec struct {
    Name                string
    Language            string
    ServiceRequirements []ServiceRequirement
}

type ServiceRequirement struct {
    Type     string // database, cache, storage, monitoring, etc.
    Provider string // postgresql, redis, s3, etc.
}
```

## Testing

Run the analyzer tests:

```bash
go test ./internal/analyzer -v
```

The test suite includes:
- Project detection tests
- Dependency parsing tests
- Framework detection tests
- Service requirement extraction tests
- Edge cases and error handling

## Best Practices

1. **Graceful Degradation** - Analyzers should handle missing files gracefully
2. **Comprehensive Testing** - Test all supported formats and edge cases
3. **Clear Documentation** - Document supported formats and limitations
4. **Performance** - Keep analysis fast and efficient
5. **Extensibility** - Design for easy addition of new service mappings

## Limitations

### Node.js Analyzer
- Only analyzes `package.json` dependencies (not devDependencies by default)
- Limited to npm ecosystem packages
- No analysis of actual code imports

### Python Analyzer
- Basic regex parsing for `setup.py`
- No AST analysis for import detection
- Limited to common package formats
- No analysis of virtual environment configuration

## Future Enhancements

- **AST Analysis** - Parse actual code imports for more accurate detection
- **More Package Managers** - Support for additional package managers
- **Configuration Files** - Analysis of framework configuration files
- **Environment Variables** - Detection of required environment variables
- **Deployment Requirements** - Analysis of deployment-specific requirements 