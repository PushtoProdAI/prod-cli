package workflowext

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"path"
	"time"

	"github.com/cschleiden/go-workflows/backend"
	"github.com/cschleiden/go-workflows/backend/monoprocess"
	"github.com/cschleiden/go-workflows/backend/sqlite"
	"github.com/cschleiden/go-workflows/client"
	"github.com/cschleiden/go-workflows/diag"
	"github.com/cschleiden/go-workflows/registry"
	"github.com/cschleiden/go-workflows/worker"
	"github.com/cschleiden/go-workflows/workflow"
	"github.com/go-errors/errors"
)

// DefaultActivityOptions sets our own default values which flexes the ones by go-workflows.
var DefaultActivityOptions = workflow.ActivityOptions{
	RetryOptions: workflow.RetryOptions{
		MaxAttempts:        10,
		BackoffCoefficient: 1,
		FirstRetryInterval: time.Second * 5,
		MaxRetryInterval:   time.Second * 20,
	},
}

// InitWorkflows initializes go-workflows.
//
// By default, the returned Provider is configured with a sqlite backend wrapped
// in a monoprocess backend. It then runs the worker in the same process as the
// backend.
//
// WorkflowsConfig is used to override these default values and configure the
// returned Provider appropriately.
//
// Please make sure to call Provider.Shutdown before the application exits
// to ensure that all workflow and activity tasks are completed.
func InitWorkflows(ctx context.Context, cfg WorkflowsConfig, mux *http.ServeMux, workflows ...Registerer) (*Provider, error) {
	sqliteBackend := sqlite.NewInMemoryBackend(sqlite.WithBackendOptions(cfg.backendOptions()...))

	if cfg.SQLitePath != "" {
		err := os.MkdirAll(cfg.sqliteDir(), 0o750) //nolint:gomnd //Permission bits are customarily written in this form.
		if err != nil {
			return nil, errors.Errorf("failed to create workflow sqlite dir: %w", err)
		}

		// Set up backend.
		sqliteBackend = sqlite.NewSqliteBackend(
			cfg.sqlitePath(),
			sqlite.WithBackendOptions(cfg.backendOptions()...),
			sqlite.WithMaxOpenConnections(10),
			sqlite.WithSQLiteOption("_pragma", "busy_timeout(10000)"),
			sqlite.WithSQLiteOption("_pragma", "journal_mode(WAL)"),
			sqlite.WithSQLiteOption("_txlock", "immediate"),
			sqlite.WithSQLiteOption("_pragma", "synchronous(NORMAL)"),
		)
	}

	// Monoprocess backend ensures the worker is working fast in a single process environment
	// (i.e. worker and backend need to run in the same process)
	monoprocessBackend := monoprocess.NewMonoprocessBackend(sqliteBackend)

	// Run worker.
	w := worker.New(monoprocessBackend, cfg.workerOptions())
	for _, wf := range workflows {
		err := wf.Register(w)
		if err != nil {
			return nil, errors.Errorf("failed to register workflow: %w", err)
		}
	}
	go func() {
		err := w.Start(ctx)
		if err != nil {
			cfg.logger().Error("workflow worker stopped", "error", err)
		}
	}()

	diagPath := cfg.diagEndpoint() + "/"
	mux.Handle(diagPath, http.StripPrefix(cfg.diagEndpoint(), diag.NewServeMux(monoprocessBackend)))

	return &Provider{
		Backend: monoprocessBackend,
		Client:  client.New(monoprocessBackend),
		Worker:  w,
	}, nil
}

type Provider struct {
	Backend backend.Backend
	Client  *client.Client
	Worker  *worker.Worker
}

func (p *Provider) Shutdown(_ context.Context) error {
	err := p.Worker.WaitForCompletion()
	if err != nil {
		return errors.Errorf("failed to wait for workflow worker completion: %w", err)
	}
	return nil
}

type Registry interface {
	RegisterActivity(any, ...registry.RegisterOption) error
	RegisterWorkflow(workflow.Workflow, ...registry.RegisterOption) error
}

type Registerer interface {
	Register(Registry) error
}

type WorkflowsConfig struct {
	// SQLitePath points to the sqlite database file used by workers. Defaults
	// to ./workflows_data/data.db.
	SQLitePath string
	// Logger is used for logging in workflows. Defaults to slog.Default.
	Logger *slog.Logger
	// BackendOptions can be used to configure the backend.
	BackendOptions []backend.BackendOption
	// WorkerOptions can be used to configure the wokrer.
	WorkerOptions *worker.Options
	// DiagEndpoint represents the path on which the go-workflow diagnostics
	// handler will be registered. Defaults to /workflows.
	DiagEndpoint string
}

func (cfg WorkflowsConfig) sqliteDir() string {
	const defaultDir = "./workflows_data"
	if cfg.SQLitePath == "" {
		return defaultDir
	}
	return path.Dir(cfg.SQLitePath)
}

func (cfg WorkflowsConfig) sqlitePath() string {
	if cfg.SQLitePath == "" {
		return cfg.sqliteDir() + "/data.db"
	}
	return cfg.SQLitePath
}

func (cfg WorkflowsConfig) logger() *slog.Logger {
	if cfg.Logger == nil {
		return slog.Default()
	}
	return cfg.Logger
}

func (cfg WorkflowsConfig) backendOptions() []backend.BackendOption {
	opts := []backend.BackendOption{
		backend.WithLogger(cfg.logger()),
	}
	return append(opts, cfg.BackendOptions...)
}

func (cfg WorkflowsConfig) workerOptions() *worker.Options {
	if cfg.WorkerOptions == nil {
		opts := worker.DefaultOptions
		opts.WorkflowPollers = 1
		opts.ActivityPollers = 1
		return &opts
	}
	return cfg.WorkerOptions
}

func (cfg WorkflowsConfig) diagEndpoint() string {
	const defaultEndpoint = "/workflows"
	if cfg.DiagEndpoint == "" {
		return defaultEndpoint
	}
	return cfg.DiagEndpoint
}
