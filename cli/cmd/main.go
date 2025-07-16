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
	os.Setenv("BAML_LOG", "warn")

	mux := http.NewServeMux()

	cfg := workflowext.WorkflowsConfig{
		Logger:         logger,
		BackendOptions: []backend.BackendOption{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	provider, err := workflowext.InitWorkflows(ctx, cfg, mux, agent.NewWorkflows())
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
	cmd := e.MustBuildCobraCommand(&root.RootCommand{Agent: a})
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
