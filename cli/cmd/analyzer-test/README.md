# Project Analyzer - Programmatic Usage

This directory contains examples of how to use the project analyzer programmatically in Go.

## Examples

### 1. Simple Single Project Analysis (`analyzer-simple/main.go`)

Analyze a single project and display results:

```bash
go run cmd/analyzer-simple/main.go <project-path>
```

Example:
```bash
go run cmd/analyzer-simple/main.go ../test-projects/flask-app
```

### 2. Batch Analysis (`analyzer-test/main.go`)

Analyze multiple test projects and compare results:

```bash
go run cmd/analyzer-test/main.go
```

This will analyze all test projects in the `../test-projects/` directory.

## Programmatic Usage

Here's how to use the analyzer in your own Go code:

```go
package main

import (
    "fmt"
    "log"
    
    "github.com/meroxa/prod/cli/internal/analyzer"
)

func main() {
    projectPath := "./my-project"
    
    // Get analyzer for the project
    analyzerPtr, err := analyzer.GetAnalyzer(projectPath)
    if err != nil {
        log.Fatalf("Failed to get analyzer: %v", err)
    }
    
    // Analyze the project
    spec, err := (*analyzerPtr).Analyze()
    if err != nil {
        log.Fatalf("Failed to analyze project: %v", err)
    }
    
    // Use the results
    fmt.Printf("Project: %s\n", spec.Name)
    fmt.Printf("Language: %s\n", spec.Language)
    
    for _, service := range spec.ServiceRequirements {
        fmt.Printf("Service: %s (%s)\n", service.Type, service.Provider)
    }
}
```

## Test Projects

The analyzer comes with several test projects to demonstrate different scenarios:

- **flask-app**: Flask web application with PostgreSQL and Redis
- **django-app**: Django application with multiple services
- **fastapi-app**: FastAPI application with async dependencies
- **poetry-project**: Poetry-managed project with pyproject.toml
- **pipenv-project**: Pipenv-managed project with Pipfile
- **node-app**: Node.js application with various dependencies

## Output Format

The analyzer returns a `ProjectSpec` struct with:

```go
type ProjectSpec struct {
    Name                string
    Language            string
    ServiceRequirements []ServiceRequirement
}

type ServiceRequirement struct {
    Type     string  // e.g., "database", "cache", "framework"
    Provider string  // e.g., "postgresql", "redis", "flask"
}
```

## Supported Languages

Currently supported:
- **Python**: requirements.txt, Pipfile, pyproject.toml
- **Node.js**: package.json

## Service Detection

The analyzer can detect various service requirements:

- **Databases**: PostgreSQL, MySQL, MongoDB, SQLite
- **Caches**: Redis, Memcached
- **Frameworks**: Flask, Django, FastAPI (Python), Express (Node.js)
- **Storage**: AWS S3
- **Search**: Elasticsearch
- **Email**: SendGrid, SMTP
- **Monitoring**: New Relic, Sentry
- **Queues**: Redis-based queues, RabbitMQ

## Error Handling

The analyzer gracefully handles:
- Missing project files
- Unsupported project types
- Malformed dependency files
- File system errors

Always check for errors when calling `GetAnalyzer()` and `Analyze()` methods. 