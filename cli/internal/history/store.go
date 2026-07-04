// Package history is prod's local deployment history — the single-binary
// replacement for the hosted deployment-logger. Records live in a JSON file
// under ~/.prod so history and `prod` history queries work with no backend.
package history

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-errors/errors"
)

// Record is one deployment operation (a deploy or rollback).
type Record struct {
	ID            string         `json:"id"`
	OperationType string         `json:"operationType"` // "deploy" | "rollback"
	ResourceName  string         `json:"resourceName"`
	Platform      string         `json:"platform"`
	Language      string         `json:"language"`
	Status        string         `json:"status"` // "started" | "success" | "failed"
	StartedAt     time.Time      `json:"startedAt"`
	CompletedAt   *time.Time     `json:"completedAt,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

// Store is a JSON-file-backed history store, safe for concurrent use within the
// process. It is deliberately simple: a single-user CLI writes a handful of
// records per session, so a file the user can read and diff beats a database.
type Store struct {
	path string
	mu   sync.Mutex
}

// DefaultPath returns ~/.prod/history.json.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.WrapPrefix(err, "failed to get home directory", 0)
	}
	return filepath.Join(home, ".prod", "history.json"), nil
}

// NewStore opens (creating the directory for) the default history store.
func NewStore() (*Store, error) {
	path, err := DefaultPath()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, errors.WrapPrefix(err, "failed to create state directory", 0)
	}
	return &Store{path: path}, nil
}

// Open returns a store backed by an explicit path (used in tests).
func Open(path string) *Store { return &Store{path: path} }

// Add appends a record.
func (s *Store) Add(r Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.load()
	if err != nil {
		return err
	}
	return s.save(append(records, r))
}

// Update sets the status/completion of the record with the given id and merges
// in any metadata. It is a no-op error if the id is unknown.
func (s *Store) Update(id, status string, completedAt time.Time, metadata map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.load()
	if err != nil {
		return err
	}
	for i := range records {
		if records[i].ID != id {
			continue
		}
		records[i].Status = status
		records[i].CompletedAt = &completedAt
		if len(metadata) > 0 {
			if records[i].Metadata == nil {
				records[i].Metadata = make(map[string]any, len(metadata))
			}
			for k, v := range metadata {
				records[i].Metadata[k] = v
			}
		}
		return s.save(records)
	}
	return errors.Errorf("history record %q not found", id)
}

// List returns records most-recent-first, up to limit (limit <= 0 returns all).
func (s *Store) List(limit int) ([]Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.load()
	if err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(records))
	for i := len(records) - 1; i >= 0; i-- {
		out = append(out, records[i])
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

// load reads all records; a missing or empty file is an empty history.
func (s *Store) load() ([]Record, error) {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.WrapPrefix(err, "failed to read history", 0)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var records []Record
	if err := json.Unmarshal(data, &records); err != nil {
		// A corrupt/hand-edited file must not wedge history forever. Quarantine it
		// and continue with an empty history so writes and reads recover.
		quarantine := s.path + ".corrupt"
		if renameErr := os.Rename(s.path, quarantine); renameErr != nil {
			slog.Warn("failed to quarantine corrupt history file", "path", s.path, "error", renameErr)
		} else {
			slog.Warn("history file was unparseable; quarantined and reset", "from", s.path, "to", quarantine)
		}
		return nil, nil
	}
	return records, nil
}

// save writes records atomically (temp file + rename).
func (s *Store) save(records []Record) error {
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return errors.WrapPrefix(err, "failed to encode history", 0)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return errors.WrapPrefix(err, "failed to write history", 0)
	}
	return os.Rename(tmp, s.path)
}
