package output

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// updateGolden regenerates the checked-in golden files instead of comparing.
// Run: go test ./internal/output/... -update
var updateGolden = flag.Bool("update", false, "update golden files")

// TestJSONEventStreamGolden pins the ENTIRE JSON event stream against a checked-in
// snapshot, so any change to a field name, order, presence, or value in any event is
// a deliberate, reviewed diff to the golden — not a silent break of the machine
// contract (docs/protocol.md). Timestamps are scrubbed (they're time.Now()).
//
// To intentionally change the contract: bump EventVersion, then `-update` this golden
// in the same PR.
func TestJSONEventStreamGolden(t *testing.T) {
	raw := captureStdout(t, func() { exerciseJSONEvents(NewJSONWriter()) })
	got := normalizeEventStream(t, raw)

	goldenPath := filepath.Join("testdata", "events.golden.jsonl")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated %s", goldenPath)
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden (run with -update to create it): %v", err)
	}
	if got != string(want) {
		t.Errorf("JSON event stream drifted from golden.\n--- got ---\n%s\n--- want ---\n%s\n"+
			"If this change is intentional, bump EventVersion and re-run with -update.", got, want)
	}
}

// normalizeEventStream decodes each JSONL line, scrubs the volatile timestamp to a
// fixed placeholder, and re-encodes with sorted keys — so the golden is deterministic
// and diffs read cleanly.
func normalizeEventStream(t *testing.T, raw string) string {
	t.Helper()
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("non-JSON event line %q: %v", line, err)
		}
		if _, ok := ev["timestamp"]; ok {
			ev["timestamp"] = "<scrubbed>"
		}
		lines = append(lines, marshalSorted(t, ev))
	}
	return strings.Join(lines, "\n") + "\n"
}

// marshalSorted marshals a map with keys in sorted order for a stable snapshot.
func marshalSorted(t *testing.T, m map[string]any) string {
	t.Helper()
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		vb, err := json.Marshal(m[k])
		if err != nil {
			t.Fatalf("marshal %q: %v", k, err)
		}
		b.Write(kb)
		b.WriteByte(':')
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.String()
}
