package workflowext

import (
	"context"
	"database/sql"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// The sqlite workflow backend must be tuned for concurrent durable workflows —
// WAL journaling + a busy timeout + a small connection pool. That tuning was the
// entire reason a personal go-workflows fork existed; we now build the DB
// ourselves and pass it to upstream's NewSqliteBackendWithDB. This regression
// test proves the tuning (and migrations) survive the move to upstream, so a
// future upstream bump can't silently drop back to the single-connection,
// non-WAL default that caused "database is locked".
func TestSqliteBackendTuning(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "wf.sqlite")

	ctx, cancel := context.WithCancel(context.Background())
	provider, err := InitWorkflows(ctx, WorkflowsConfig{SQLitePath: dbPath}, http.NewServeMux())
	if err != nil {
		t.Fatalf("InitWorkflows: %v", err)
	}
	cancel()
	_ = provider.Shutdown(context.Background())

	// busy_timeout so this read waits for any WAL lock the worker is still
	// releasing rather than failing fast with SQLITE_BUSY.
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	// WAL is persisted in the DB header once enabled, so a fresh connection sees it.
	var journalMode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if strings.ToLower(journalMode) != "wal" {
		t.Errorf("journal_mode = %q, want wal — the sqlite concurrency tuning was lost", journalMode)
	}

	// WithApplyMigrations(true) must have created the go-workflows schema.
	var tables int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&tables); err != nil {
		t.Fatalf("query schema: %v", err)
	}
	if tables == 0 {
		t.Error("no tables created — go-workflows migrations did not run")
	}
}
