# prod machine protocol (v1)

**Status:** v1 documents the contract as it exists **today**. It is the freeze target for W0 in the
[agentic-ownership roadmap](./agentic-ownership-roadmap.md): W0 stamps `event_version`, unifies the
event emitters onto one canonical object, and adds golden snapshots so this document and the code
can't drift. Nothing here changes behavior — it writes down what's already emitted so agents can
depend on it.

prod exposes **two machine surfaces**, and they are layered:

1. **The JSON event stream** — `PROD_JSON_MODE=true` makes `prod run` emit newline-delimited JSON
   (JSON Lines) to stdout, one event per line. This is the substrate.
2. **The MCP tools** — `prod mcp` serves 9 typed tools over stdio. The mutating tools
   (`deploy`/`rollback`/`destroy`) run the real work in a **subprocess** with `PROD_JSON_MODE=true`
   and parse the event stream above. So the MCP contract sits *on top of* the JSON contract.

> **Consumer rule:** treat additive fields as non-breaking. New event types and new optional fields
> can appear at any time; do not fail on unknown keys. A breaking change (renamed/removed field,
> changed type or semantics) will bump `event_version` (JSON) / the MCP server version.

---

## Surface 1 — the JSON event stream

Enable with `PROD_JSON_MODE=true`. Each line is a JSON object with a `type` discriminator. Emitted
by `internal/output/writer.go` (`JSONWriter`).

### The envelope

Every event carries a common envelope: **`type`** (discriminator), **`event_version`** (integer,
currently `1`), and **`timestamp`** (RFC3339Nano). Landed in W0.1 — every event, including the
map-built `plan_approval_request`, now carries all three, and timestamps are one uniform format.

### Remaining v1 inconsistency (a later W0 sub-task)

- **`plan_approval_request` spreads the plan fields at the top level** rather than nesting them under
  a `plan` key. Read plan fields off the event root for now; a follow-up nests them (it needs the
  `SendPlanApprovalRequest` signature to change, so it's sequenced after the golden snapshots).

### Event catalog

| `type` | Fields | Meaning |
|--------|--------|---------|
| `log` | `message`, `timestamp` | Raw log line (anything written to the writer's `io.Writer`). |
| `status` | `status`, `message`, `timestamp` | A progress step (`status` is a short state token). |
| `status_complete` | `status`, `message`, `timestamp` | A progress step finished. |
| `deployment_start` | `platform`, `project_path`, `timestamp` | A deploy began. |
| `deployment_complete` | `platform`, `status`, `duration_ms`, `timestamp`, `url?`, `error?`, `id?`, `name?` | Terminal event. `status` ∈ `success`\|`failed`. `id`/`name` identify the exact deployment. |
| `plan_approval_request` | *(plan fields at root)* + `timestamp` | The plan awaiting approval (action, platform, shape, cost, services). |
| `env_var_prompt` | `variable_name`, `default_value`, `message`, `timestamp` | prod needs a value for an env var. |
| `doctor_result` | `check`, `status`, `detail`, `timestamp`, `fix?` | One `prod doctor` check result. |

**Correlation.** `deployment_complete` already carries `id` (the local history record id) and
`name`. This is the handle to tie a deploy to a later `status`/`rollback`/`destroy`. **Gap (W0):** the
MCP `deploy` tool output does *not* echo this `id` yet — W0 threads it into `deployOutput` so an
agent can correlate the loop without re-guessing by app name.

**What's missing today (filled by later workstreams):**
- No structured **verify** result — liveness is collapsed into `deployment_complete.status`. (W2)
- No structured **diagnosis** — failures surface as a flat `error` string; the remediation list the
  summarizer produces is **dropped** before the event. (W2)

---

## Surface 2 — the MCP tools

`prod mcp` serves these over stdio. Input/output are typed Go structs (JSON Schema is derived from
them by the MCP SDK), defined in `internal/mcpserver/{server,status,tools}.go`. All schemas are
stable structs but **carry no version field yet** — W0 adds a schema-golden so changes are caught.

### The approval gate (read this before calling a mutating tool)

`deploy`, `rollback`, `destroy` are **destructive and cost money**. They share one safety model:

- `confirm=false` (default) → **preview only.** Returns the plan and (for deploy) the estimated cost.
  Changes nothing.
- `confirm=true` → executes — but `deploy` additionally requires `planDigest`, the value returned by
  a prior `confirm=false` preview of the **same** `prompt`+`path`. The digest is salted per process,
  so an agent can't fabricate it: it must preview first (and show the human the plan). Mismatch
  returns `status: "preview-required"`. This is a structural nudge; **explicit human approval is the
  real gate.**

### Tool catalog

**Mutating (gated):**

| Tool | Input | Output |
|------|-------|--------|
| `deploy` | `prompt`, `confirm?`, `path?`, `planDigest?` | `deployed`, `status` (`preview`\|`success`\|`failed`\|`preview-required`), `url?`, `error?`, `plan?`, `planDigest?` |
| `rollback` | `platform` (required), `confirm?`, `path?` | `rolledBack`, `status`, `error?`, `plan?` |
| `destroy` | `platform` (required), `confirm?`, `path?` | `destroyed`, `status`, `error?`, `plan?` |

**Read-only:**

| Tool | Input | Output |
|------|-------|--------|
| `list_deploys` | `limit?` (default 20) | `mode` (`local`\|`managed`), `deployments[]` (`id`, `operationType`, `resourceName`, `platform`, `language`, `status`, `url?`, `startedAt`, `completedAt?`) |
| `status` | `app` | `found`, `platform?`, `shape?`, `status?`, `liveUrl?`, `live` (`live`\|`not-live`\|`unknown`), `canRollback`, `note?` |
| `deep_link` | `app` | `found`, `liveUrl?`, `consoleUrl?`, `note?` |
| `logs` | `app` | `found`, `logsCmd?` (the CLI command, **not** log bytes), `consoleUrl?`, `note?` |
| `analyze_project` | `path?` | `name`, `language`, `buildCommand?`, `startCommand?`, `services[]` (`type`, `provider`) |
| `doctor` | *(none)* | `llm` (`provider?`, `model?`, `ready`, `detail?`), `dockerAvailable`, `ready` |

**Planned tools (roadmap, not yet present):** `status` gains a `reprobe` bool + `deployment_id` and
returns the structured `VerifyResult` (W2); `recall` returns last-good config / last failure / a diff
(W4). No standalone `verify` or `heal` tool is planned — see the roadmap for why.

---

## The recommended agent loop

The contract is designed for **propose → approve → deploy → verify → (diagnose → retry)**:

1. `doctor` — is prod ready? (LLM configured, Docker if needed.)
2. `deploy` with `confirm=false` — get the plan + cost + `planDigest`. **Show the human.**
3. On human approval: `deploy` with `confirm=true` + `planDigest`.
4. `status` (post-W2: with `reprobe`) — machine-checkable proof it's live.
5. On failure: read the structured diagnosis (post-W2), and if `retryable`, re-`deploy` with a fix.

`retryable` (post-W2) is the branch signal; `blame` is advisory context. Never treat `confirm=true`
as safe without a human having seen the `confirm=false` plan.

---

## Versioning policy

- **Additive** (new event type, new optional field, new tool) → non-breaking, no version bump.
  Consumers must ignore unknown fields/types.
- **Breaking** (rename/remove a field, change a type or a documented semantic, change a `status`
  enum value) → bumps `event_version` (JSON stream) and the MCP server version, and updates the
  golden snapshots in the same PR.
- This document is the human-readable contract; the golden tests (W0) are the machine-enforced one.
  They must agree.
