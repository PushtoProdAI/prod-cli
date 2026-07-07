package workflowext

import (
	"context"
	"database/sql"
	"fmt"
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
	_ "modernc.org/sqlite" // registers the "sqlite" driver (same one go-workflows uses)
)

// workflowRetention is how long a finished workflow instance is kept in the durable db
// before startup GC removes it — long enough to resume/inspect a very recent deploy,
// short enough that the single-user db stays tiny.
const workflowRetention = 24 * time.Hour

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
	// db is the file-backed sqlite handle we own (nil for the in-memory backend);
	// the Provider closes it on Shutdown.
	var db *sql.DB
	var sqliteBackend backend.Backend

	if cfg.SQLitePath != "" {
		if err := os.MkdirAll(cfg.sqliteDir(), 0o750); err != nil { //nolint:gomnd //Permission bits are customarily written in this form.
			return nil, errors.Errorf("failed to create workflow sqlite dir: %w", err)
		}

		// Open the sqlite DB ourselves so we can tune it for concurrent durable
		// workflows: WAL journaling, a 10s busy timeout, immediate txlock, and a
		// small connection pool. Upstream go-workflows opens sqlite with
		// SetMaxOpenConns(1) and no WAL; these settings — previously supplied by a
		// personal fork's WithMaxOpenConnections/WithSQLiteOption — prevent
		// "database is locked" under the monoprocess worker. We pass the tuned DB to
		// NewSqliteBackendWithDB, which lets us use upstream directly (no fork).
		// modernc.org/sqlite is the same driver upstream registers.
		dsn := fmt.Sprintf(
			"file:%s?_txlock=immediate&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)",
			cfg.sqlitePath(),
		)
		var err error
		db, err = sql.Open("sqlite", dsn)
		if err != nil {
			return nil, errors.Errorf("failed to open workflow sqlite db: %w", err)
		}
		db.SetMaxOpenConns(10)

		// WithApplyMigrations(true) preserves NewSqliteBackend's default (the
		// WithDB constructor defaults to false). NewSqliteBackendWithDB does NOT
		// own/close db, so the Provider does (see Shutdown).
		sqliteBackend = sqlite.NewSqliteBackendWithDB(
			db,
			sqlite.WithBackendOptions(cfg.backendOptions()...),
			sqlite.WithApplyMigrations(true),
		)

		// GC on startup: drop workflow instances that finished more than the retention
		// window ago, so the durable db doesn't grow without bound across CLI runs.
		// Best-effort — a GC failure must never block a deploy. (Instances finished more
		// recently are kept as a resume/debug buffer.)
		if err := sqliteBackend.RemoveWorkflowInstances(ctx, backend.RemoveFinishedBefore(time.Now().Add(-workflowRetention))); err != nil {
			cfg.logger().Warn("failed to GC old workflow instances", "error", err)
		}
	} else {
		sqliteBackend = sqlite.NewInMemoryBackend(sqlite.WithBackendOptions(cfg.backendOptions()...))
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
	// Start launches the worker's poller/dispatcher goroutines and returns; it does
	// not block. Call it synchronously so its internal WaitGroup.Add happens-before
	// any later Shutdown → WaitForCompletion (a concurrent Add/Wait is a data race).
	if err := w.Start(ctx); err != nil {
		return nil, errors.Errorf("failed to start workflow worker: %w", err)
	}

	diagPath := cfg.diagEndpoint() + "/"
	mux.Handle(diagPath, http.StripPrefix(cfg.diagEndpoint(), diag.NewServeMux(monoprocessBackend)))

	return &Provider{
		Backend: monoprocessBackend,
		Client:  client.New(monoprocessBackend),
		Worker:  w,
		db:      db,
	}, nil
}

type Provider struct {
	Backend backend.Backend
	Client  *client.Client
	Worker  *worker.Worker
	db      *sql.DB // the file-backed sqlite DB we own; nil for in-memory
}

func (p *Provider) Shutdown(_ context.Context) error {
	err := p.Worker.WaitForCompletion()
	// NewSqliteBackendWithDB doesn't own the connection, so release the pool and
	// checkpoint the WAL here. Prefer surfacing the worker error if both fail.
	if p.db != nil {
		if cerr := p.db.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}
	if err != nil {
		return errors.Errorf("failed to shut down workflows: %w", err)
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
