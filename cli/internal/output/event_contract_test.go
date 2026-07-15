package output

import (
	"bufio"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// exerciseJSONEvents drives every JSON event type once.
func exerciseJSONEvents(w *JSONWriter) {
	_, _ = w.Write([]byte("log line\n"))
	w.SendStatus("building", "Building")
	w.SendStatusComplete("building", "Built")
	w.SendDeploymentStart("aws", "/p")
	w.SendDeploymentComplete("fly", "success", "https://x.fly.dev", "", "op-9", "myapp", 4200)
	w.SendPlanApprovalRequest(map[string]any{"action": "deploy", "platform": "fly"})
	w.SendEnvVarPrompt("KEY", "", "Enter")
	w.SendDoctorResult("LLM", "ok", "OpenAI", "")
}

// TestEveryEventCarriesVersion pins that every event — including the map-built
// plan_approval_request — carries event_version == EventVersion. This is what lets
// a consumer negotiate the contract.
func TestEveryEventCarriesVersion(t *testing.T) {
	ev := decodeEventsByType(t, exerciseJSONEvents)
	if len(ev) == 0 {
		t.Fatal("no events emitted")
	}
	for typ, e := range ev {
		v, ok := e["event_version"].(float64)
		if !ok {
			t.Errorf("event %q missing event_version, got %v", typ, e["event_version"])
			continue
		}
		if int(v) != EventVersion {
			t.Errorf("event %q has event_version %v, want %d", typ, v, EventVersion)
		}
	}
}

// TestEventTimestampsAreUniform pins that every event's timestamp parses as
// RFC3339Nano — one format across all events (the map-built events previously used
// plain RFC3339).
func TestEventTimestampsAreUniform(t *testing.T) {
	ev := decodeEventsByType(t, exerciseJSONEvents)
	for typ, e := range ev {
		ts, ok := e["timestamp"].(string)
		if !ok {
			t.Errorf("event %q missing timestamp string, got %v", typ, e["timestamp"])
			continue
		}
		if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
			t.Errorf("event %q timestamp %q not RFC3339Nano: %v", typ, ts, err)
		}
	}
}

// TestPlanApprovalDoesNotMutateCaller pins that emitting a plan event does not add
// envelope keys to the caller's map — a prior version mutated it in place, which
// corrupted the plan object the rest of the deploy still read from.
func TestPlanApprovalDoesNotMutateCaller(t *testing.T) {
	plan := map[string]any{"action": "deploy", "platform": "fly", "summary": "s"}
	before := len(plan)
	_ = captureStdout(t, func() { NewJSONWriter().SendPlanApprovalRequest(plan) })
	if len(plan) != before {
		t.Errorf("caller's plan map was mutated: len %d -> %d (%v)", before, len(plan), plan)
	}
	for _, k := range []string{"type", "timestamp", "event_version"} {
		if _, injected := plan[k]; injected {
			t.Errorf("caller's plan map had %q injected into it", k)
		}
	}
}

// decodeEventsByType runs fn against a fresh JSONWriter and returns the decoded
// events keyed by their "type". It is the harness for pinning the JSON wire
// contract (docs/protocol.md) so a refactor of the emitters can't silently
// change a field name, drop a field, or alter a value.
func decodeEventsByType(t *testing.T, fn func(w *JSONWriter)) map[string]map[string]any {
	t.Helper()
	out := captureStdout(t, func() { fn(NewJSONWriter()) })
	byType := map[string]map[string]any{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("JSON writer emitted a non-JSON line: %q (%v)", line, err)
		}
		if typ, ok := ev["type"].(string); ok {
			byType[typ] = ev
		}
	}
	return byType
}

// TestJSONEventFieldContract pins the field names and values of every JSON event
// that carries structured fields. These are the machine contract other tools and
// agents depend on; changing any of them is a breaking change (bump EventVersion).
func TestJSONEventFieldContract(t *testing.T) {
	ev := decodeEventsByType(t, func(w *JSONWriter) {
		_, _ = w.Write([]byte("a raw log line\n"))
		w.SendStatus("building", "Building image")
		w.SendStatusComplete("building", "Built")
		w.SendDeploymentStart("aws", "/path/to/project")
		w.SendDeploymentComplete("fly", "success", "https://x.fly.dev", "", "op-9", "myapp", 4200)
		w.SendDeploymentComplete("fly", "failed", "", "boom", "op-10", "myapp", 100)
		w.SendEnvVarPrompt("DATABASE_URL", "postgres://d", "Enter it")
		w.SendDoctorResult("Docker", "fail", "not running", "install docker")
		w.SendDoctorResult("LLM", "ok", "OpenAI", "")
	})

	if e := ev["log"]; e["message"] != "a raw log line\n" {
		t.Errorf("log event: %v", e)
	}
	if e := ev["status"]; e["status"] != "building" || e["message"] != "Building image" {
		t.Errorf("status event: %v", e)
	}
	if e := ev["status_complete"]; e["status"] != "building" || e["message"] != "Built" {
		t.Errorf("status_complete event: %v", e)
	}
	if e := ev["deployment_start"]; e["platform"] != "aws" || e["project_path"] != "/path/to/project" {
		t.Errorf("deployment_start event: %v", e)
	}
	if e := ev["env_var_prompt"]; e["variable_name"] != "DATABASE_URL" || e["default_value"] != "postgres://d" || e["message"] != "Enter it" {
		t.Errorf("env_var_prompt event: %v", e)
	}
	// doctor_result: the last-emitted "doctor_result" wins in the by-type map — the
	// passing LLM check, which must OMIT fix (fix is only present on a failure).
	if e := ev["doctor_result"]; e["check"] != "LLM" || e["status"] != "ok" || e["detail"] != "OpenAI" {
		t.Errorf("doctor_result event: %v", e)
	}
	if _, hasFix := ev["doctor_result"]["fix"]; hasFix {
		t.Errorf("doctor_result: fix must be omitted when the check passes, got %v", ev["doctor_result"]["fix"])
	}
}

// TestDeploymentCompleteFieldContract pins the deployment_complete field set for a
// successful deploy (url present) — the terminal event the MCP substrate parses.
func TestDeploymentCompleteFieldContract(t *testing.T) {
	ev := decodeEventsByType(t, func(w *JSONWriter) {
		w.SendDeploymentComplete("fly", "success", "https://x.fly.dev", "", "op-9", "myapp", 4200)
	})
	e := ev["deployment_complete"]
	if e["platform"] != "fly" || e["status"] != "success" || e["url"] != "https://x.fly.dev" {
		t.Errorf("deployment_complete core fields: %v", e)
	}
	if e["id"] != "op-9" || e["name"] != "myapp" {
		t.Errorf("deployment_complete correlation fields: %v", e)
	}
	if e["duration_ms"].(float64) != 4200 {
		t.Errorf("deployment_complete duration_ms: %v", e["duration_ms"])
	}
	// error must be omitted on success.
	if _, has := e["error"]; has {
		t.Errorf("deployment_complete: error must be omitted on success, got %v", e["error"])
	}
}
