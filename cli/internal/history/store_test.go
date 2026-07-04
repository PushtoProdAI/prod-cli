package history

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStoreAddUpdateList(t *testing.T) {
	s := Open(filepath.Join(t.TempDir(), "history.json"))

	// Empty store lists nothing.
	got, err := s.List(0)
	if err != nil {
		t.Fatalf("List on empty store: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("empty store returned %d records", len(got))
	}

	start := time.Now()
	for _, id := range []string{"a", "b", "c"} {
		if err := s.Add(Record{ID: id, Platform: "fly", Status: "started", StartedAt: start}); err != nil {
			t.Fatalf("Add(%s): %v", id, err)
		}
	}

	// Most-recent-first ordering.
	got, err = s.List(0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 || got[0].ID != "c" || got[2].ID != "a" {
		t.Fatalf("List order wrong: %+v", got)
	}

	// Limit.
	got, err = s.List(2)
	if err != nil {
		t.Fatalf("List(2): %v", err)
	}
	if len(got) != 2 || got[0].ID != "c" || got[1].ID != "b" {
		t.Fatalf("List(2) wrong: %+v", got)
	}

	// Update merges metadata and sets completion.
	done := start.Add(time.Minute)
	if err := s.Update("b", "success", done, map[string]any{"url": "https://b.fly.dev"}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ = s.List(0)
	for _, r := range got {
		if r.ID != "b" {
			continue
		}
		if r.Status != "success" {
			t.Errorf("status = %q, want success", r.Status)
		}
		if r.CompletedAt == nil || !r.CompletedAt.Equal(done) {
			t.Errorf("completedAt = %v, want %v", r.CompletedAt, done)
		}
		if r.Metadata["url"] != "https://b.fly.dev" {
			t.Errorf("metadata url = %v", r.Metadata["url"])
		}
	}

	if err := s.Update("missing", "success", done, nil); err == nil {
		t.Error("Update on unknown id should error")
	}
}

func TestStorePersistsAcrossOpens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	if err := Open(path).Add(Record{ID: "x", Status: "started", StartedAt: time.Now()}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	got, err := Open(path).List(0)
	if err != nil {
		t.Fatalf("List after reopen: %v", err)
	}
	if len(got) != 1 || got[0].ID != "x" {
		t.Fatalf("did not persist: %+v", got)
	}
}
