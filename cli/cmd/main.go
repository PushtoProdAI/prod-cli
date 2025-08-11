package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/conduitio/ecdysis"
	"github.com/cschleiden/go-workflows/backend"
	"github.com/meroxa/prod/cli/cmd/root"
	"github.com/meroxa/prod/cli/internal/agent"
	be "github.com/meroxa/prod/cli/internal/backend"
	"github.com/meroxa/prod/cli/internal/deployment/render"
	"github.com/meroxa/prod/cli/internal/output"
	"github.com/meroxa/prod/cli/internal/workflowext"
)

func main() {
	logFile, err := initLogFile()
	if err != nil {
		log.Fatalf("failed to initialize log file: %v", err)
	}
	defer logFile.Close()
	handler := slog.NewTextHandler(logFile, nil)
	logger := slog.New(handler)
	slog.SetDefault(logger)
	os.Setenv("BAML_LOG", "error")

	mux := http.NewServeMux()

	cfg := workflowext.WorkflowsConfig{
		Logger:         logger,
		BackendOptions: []backend.BackendOption{},
	}
	ctx, cancel := context.WithCancel(context.Background())

	// Create a writer based on environment or configuration
	// You can easily swap this to output.WriterTypeConsole for simple console output
	writerType := output.WriterTypeTUI
	if os.Getenv("PROD_CONSOLE_MODE") == "true" {
		writerType = output.WriterTypeConsole
	}
	unifiedWriter := output.NewWriter(writerType)

	apiKey := os.Getenv("RENDER_API_KEY")
	// Create HTTP client for real API calls
	renderClient := render.NewHTTPRenderClient(apiKey, unifiedWriter)
	beClient := be.NewClient()
	provider, err := workflowext.InitWorkflows(ctx, cfg, mux, agent.NewWorkflows(renderClient, beClient, unifiedWriter))
	if err != nil {
		log.Fatalf("failed to initialize workflows: %v", err)
	}

	defer func() {
		err := provider.Shutdown(context.Background())
		if err != nil {
			log.Fatalf("failed to shutdown workflows provider: %v", err)
		}
	}()

	debugEndpoint := os.Getenv("PROD_DEBUG")
	if debugEndpoint != "" {
		go http.ListenAndServe(":8080", mux)
	}
	e := ecdysis.New()
	a := agent.NewAgent(provider.Client, true)
	cmd := e.MustBuildCobraCommand(&root.RootCommand{Agent: a, UnifiedWriter: unifiedWriter})
	if err := cmd.Execute(); err != nil {
		log.Fatal(err)
	}
	// this will shout down the workflow provider gracefully
	cancel()
}

func initLogFile() (*os.File, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	dirPath := filepath.Join(homeDir, ".prod")

	err = os.MkdirAll(dirPath, 0755)
	if err != nil {
		return nil, fmt.Errorf("failed to create directory: %w", err)
	}

	filePath := filepath.Join(dirPath, "log.txt")

	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open/create file: %w", err)
	}

	return file, nil
}
