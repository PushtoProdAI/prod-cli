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
	"github.com/go-errors/errors"
	"github.com/meroxa/prod/cli/cmd/root"
	"github.com/meroxa/prod/cli/internal/agent"
	"github.com/meroxa/prod/cli/internal/auth"
	be "github.com/meroxa/prod/cli/internal/backend"
	"github.com/meroxa/prod/cli/internal/config"
	"github.com/meroxa/prod/cli/internal/deployment/flyio"
	"github.com/meroxa/prod/cli/internal/deployment/render"
	prod_error "github.com/meroxa/prod/cli/internal/error"
	prod_log "github.com/meroxa/prod/cli/internal/log"
	"github.com/meroxa/prod/cli/internal/output"
	"github.com/meroxa/prod/cli/internal/workflowext"
)

func main() {
	// Unset MallocStackLogging to prevent macOS malloc debug messages from interfering with TUI
	os.Unsetenv("MallocStackLogging")

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
		BackendOptions: []backend.BackendOption{backend.WithContextPropagator(agent.SessionPropagator)},
	}
	ctx, cancel := context.WithCancel(context.Background())

	// Determine writer type based on environment
	writerType := output.WriterTypeTUI
	if os.Getenv("PROD_CONSOLE_MODE") == "true" {
		writerType = output.WriterTypeConsole
	}
	if os.Getenv("PROD_JSON_MODE") == "true" {
		writerType = output.WriterTypeJSON
	}

	// Create a writer that can be updated later for TUI mode
	var statusWriter output.StatusWriter
	if writerType == output.WriterTypeConsole {
		statusWriter = output.NewConsoleWriter()
	} else if writerType == output.WriterTypeJSON {
		statusWriter = output.NewJSONWriter()
	} else {
		// For TUI mode, create a proxy writer that starts with console writer
		// and will be updated to TeaWriter when TUI starts
		statusWriter = output.NewProxyWriter(output.NewConsoleWriter())
	}

	apiKey := os.Getenv("RENDER_API_KEY")
	// Create HTTP client for real API calls
	renderClient := render.NewHTTPRenderClient(apiKey, statusWriter)
	flyClient := flyio.NewFlyioClient(statusWriter)
	beClient := be.NewClient()
	provider, err := workflowext.InitWorkflows(ctx, cfg, mux, agent.NewWorkflows(renderClient, flyClient, beClient, statusWriter))
	if err != nil {
		log.Fatalf("failed to initialize workflows: %v", err)
	}

	defer func() {
		err := provider.Shutdown(context.Background())
		if err != nil {
			log.Fatalf("failed to shutdown workflows provider: %v", err)
		}
	}()

	// init sentry error reporting
	if err := prod_error.Initialize(); err != nil {
		slog.Error("could not initialize error reporting", "error", err)
	}
	defer prod_error.Flush()

	if config.DebugMode() {
		logdyHandler := prod_log.NewLogdyHandler(handler, true, mux)
		logger := slog.New(logdyHandler)
		slog.SetDefault(logger)
		go http.ListenAndServe(":8080", mux)
	}
	e := ecdysis.New()
	supabaseAuth, err := auth.NewSupabaseAuth(statusWriter)
	if err != nil {
		fmt.Println("failed to initialize auth:", err)
		log.Fatalf("failed to initialize auth: %v", err)
	}
	a := agent.NewAgent(provider.Client, supabaseAuth, false)
	rootCmd := &root.RootCommand{
		Agent:        a,
		StatusWriter: statusWriter,
		WriterType:   writerType,
	}
	// Set Agent and StatusWriter for the Run subcommand
	rootCmd.Run.Agent = a
	rootCmd.Run.StatusWriter = statusWriter

	cmd := e.MustBuildCobraCommand(rootCmd)
	if err := cmd.Execute(); err != nil {
		log.Fatal(err)
	}
	// this will shout down the workflow provider gracefully
	cancel()
}

func initLogFile() (*os.File, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, errors.WrapPrefix(err, "failed to get home directory", 0)
	}

	dirPath := filepath.Join(homeDir, ".prod")

	err = os.MkdirAll(dirPath, 0o755)
	if err != nil {
		return nil, errors.WrapPrefix(err, "failed to create directory", 0)
	}

	filePath := filepath.Join(dirPath, "log.txt")

	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, errors.WrapPrefix(err, "failed to open/create file", 0)
	}

	return file, nil
}
