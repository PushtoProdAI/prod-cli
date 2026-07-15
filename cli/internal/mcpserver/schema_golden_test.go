package mcpserver

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

// updateGolden regenerates the checked-in golden instead of comparing.
// Run: go test ./internal/mcpserver/... -update
var updateGolden = flag.Bool("update", false, "update golden files")

// mustSchema derives the JSON Schema for a tool input/output type the same way the
// MCP SDK does (github.com/google/jsonschema-go), so the golden reflects exactly
// what agents see.
func mustSchema[T any](t *testing.T) *jsonschema.Schema {
	t.Helper()
	s, err := jsonschema.For[T](nil)
	if err != nil {
		t.Fatalf("derive schema for %T: %v", *new(T), err)
	}
	return s
}

type toolSchemaSnapshot struct {
	Name   string             `json:"name"`
	Input  *jsonschema.Schema `json:"input"`
	Output *jsonschema.Schema `json:"output"`
}

// TestToolSchemaGolden pins the JSON Schema of every MCP tool's input and output
// against a checked-in snapshot. An agent depends on these shapes; a change to any
// field, type, or requiredness is a breaking change to the tool contract and must be
// a deliberate, reviewed diff to the golden.
//
// To intentionally change a tool's schema: bump ContractVersion, then `-update` this
// golden in the same PR.
func TestToolSchemaGolden(t *testing.T) {
	// Order matches New()'s registration order; keep them in lockstep.
	snaps := []toolSchemaSnapshot{
		{"deploy", mustSchema[deployInput](t), mustSchema[deployOutput](t)},
		{"list_deploys", mustSchema[listDeploysInput](t), mustSchema[listDeploysOutput](t)},
		{"analyze_project", mustSchema[analyzeInput](t), mustSchema[analyzeOutput](t)},
		{"rollback", mustSchema[rollbackInput](t), mustSchema[rollbackOutput](t)},
		{"destroy", mustSchema[destroyInput](t), mustSchema[destroyOutput](t)},
		{"status", mustSchema[appInput](t), mustSchema[statusOutput](t)},
		{"deep_link", mustSchema[appInput](t), mustSchema[deepLinkOutput](t)},
		{"logs", mustSchema[appInput](t), mustSchema[logsOutput](t)},
		{"doctor", mustSchema[doctorInput](t), mustSchema[doctorOutput](t)},
	}

	got, err := json.MarshalIndent(snaps, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')

	goldenPath := filepath.Join("testdata", "tool_schemas.golden.json")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated %s", goldenPath)
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden (run with -update to create it): %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("MCP tool schemas drifted from golden. If intentional, bump ContractVersion "+
			"and re-run with -update.\n--- got ---\n%s", got)
	}
}
