# DeepFlow P0 MVP: Design Spec

**Date:** 2026-11-07
**Source:** DeepFlow PRD v1.0
**Scope:** PRD Milestone 3 (all P0 requirements), web-first on desktop viewports.

## 1. Goals and non-goals

**Goal:** a learner can start a pursuit, work in an adaptive bento workspace governed by a real Orchestrator, Evaluator and Cell Agents, finish a session, and return later to retrieval questions and a Living Document that kept growing.

**Out of scope (P1/P2):** drag-to-resize and custom layouts, concept map cell, Notes and Resources cell, reference ingestion, general simulation framework, Independent Thinking Space, native desktop shell, AI-generated executable components, multi-user support.

## 2. Architecture

A single Go binary, `deepflow`, serves a compiled React frontend (embedded with `go:embed`) and the API on `127.0.0.1:7878`.

```
Browser (React)  --REST-->  HTTP API --> Harness
                 <--SSE---  Event stream <-- Harness
Harness: Bus -> Evaluator -> Orchestrator -> Cell Agents -> Provider
Store:   SQLite at ~/.deepflow/deepflow.db (override: DEEPFLOW_HOME)
```

- Backend: Go 1.24, standard library `net/http`, `modernc.org/sqlite` (pure Go), `github.com/anthropics/anthropic-sdk-go`.
- Frontend: React, TypeScript and Vite. Node is needed at build time only. No state library or UI kit: React context plus a typed reducer driven by server events.
- The server binds to loopback only and rejects requests whose `Host` header is not loopback, so other sites cannot reach local learning data through DNS rebinding.

### Layout

```
cmd/deepflow/main.go        entry point and configuration
internal/domain/            shared types: cells, events, objects, evidence
internal/store/             SQLite schema, migrations, repositories
internal/harness/           bus, context, runtime, scheduler
internal/evaluator/         event filter, evaluation call, output validation
internal/orchestrator/      policy rules, decisions, adaptation window, undo
internal/agents/            one file per cell type plus the Agent contract
internal/expr/              safe expression language (parser, evaluator)
internal/provider/          Provider interface, claude.go, mock.go
internal/api/               REST handlers, SSE, static files
web/src/                    React app: workspace, cells/, api, state
web/src/lib/expr.ts         browser evaluator for the same expression language
```

Each package has one purpose and depends only on `domain` and the interfaces it consumes. `provider` knows nothing about cells. `orchestrator` never renders. The renderer makes no pedagogical decisions.

## 3. Data model

**Durable tables**

| Table | Key fields |
|---|---|
| `pursuits` | id, title, description, created_at, updated_at |
| `sessions` | id, pursuit_id, inquiry, started_at, ended_at, summary, workspace_snapshot (JSON), retrieval_done |
| `messages` | id, session_id, role, content, created_at |
| `concept_objects` | id, pursuit_id, type (concept, hypothesis, insight, connection, question), content, related_ids (JSON), authorship (ai, learner, edited, reference), confidence (low, medium, high), status (active, challenged, retired), version, updated_at |
| `object_versions` | object_id, version, content, authorship, changed_at |
| `evidence` | id, pursuit_id, session_id, concept, learner_action, tier (weak, moderate, strong, durable), correctness (nullable), reasoning (nullable), rationale, uncertainty, source_event_id, created_at |
| `events` | id, session_id, type, origin, payload (JSON), context_version, dedupe_key (unique), created_at |
| `artifacts` | id, session_id, cell_id, cell_type, content (JSON), learner_authored, created_at |
| `drafts` | session_id, cell_id, content, updated_at |
| `settings` | key, value |

**Runtime state** (`CellInstance`, `AgentTask`, `WorkspaceState`) lives in memory. After every structural change and every local cell state change, the workspace is serialized to `sessions.workspace_snapshot`, so a restart restores it. Agent instances are recreated from the snapshot; in-flight tasks are not resumed.

## 4. Harness

### 4.1 Event bus

Typed `WorkspaceEvent`s are persisted first, then delivered. A unique `dedupe_key` makes duplicate delivery a no-op. Event types:

`dialogue.user_message`, `dialogue.agent_message`, `practice.submitted`, `practice.evaluated`, `thought.response_submitted`, `viz.conclusion_submitted`, `viz.parameter_changed`, `concept.explored`, `document.object_edited`, `document.object_challenged`, `retrieval.answered`, `cell.request`, `user.pin`, `user.unpin`, `user.remove`, `user.refresh`, `user.undo`, `user.reassess`, `orchestrator.decision`, `context.updated`.

`viz.parameter_changed` is debounced by the client and recorded as an observation. It never triggers evaluation.

### 4.2 Shared Learning Context

One versioned struct per session: pursuit, inquiry, recent interactions, concepts, hypotheses, demonstrated understanding, misconceptions, open questions, cell responsibilities and relevant document objects. Only the Evaluator's output and explicit learner actions can change it, and each change increments `version`. Agents receive a role-specific projection, not the whole struct (for example, the Visualization agent does not receive full dialogue history).

### 4.3 Cell Agent contract

```go
type Agent interface {
    Type() domain.CellType
    Init(ctx context.Context, in AgentInput) (AgentOutput, error)
    Handle(ctx context.Context, in AgentInput, ev domain.Event) (AgentOutput, error)
    Update(ctx context.Context, in AgentInput, directive Directive) (AgentOutput, error)
    Retain(state json.RawMessage) []domain.Artifact
}
```

- `AgentOutput` holds new cell content, local state, events to publish and `CellRequest`s to the Orchestrator. It has no path to modify other cells.
- The runtime owns one inbox goroutine per live cell, started when the cell initializes and stopped at termination. It holds no model connection while idle.
- Lifecycle states: `proposed`, `initializing`, `active`, `waiting`, `updating`, `suspended`, `retiring`, `terminated`, plus a `failed` display state from which the learner can retry.

### 4.4 Stale-result protection

Every task records `agent_instance_id`, `input_context_version` and the cell's `user_revision`. When a result arrives, the runtime applies these rules:

1. Instance terminated or retiring: drop the result.
2. Cell `user_revision` changed since the task started: drop the result.
3. A newer task for the same cell already completed: drop the result.

An older context version alone does not drop a result, since most cells stay useful, but the agent is re-queued if it was told to adapt to the newer context.

### 4.5 Resource governance

A semaphore caps concurrent model calls (default 3), with dialogue given priority. Each agent has a per-session call budget (default 40, dialogue exempt). Each task has a timeout (default 3 minutes) and a cancelable context. Provider errors that are retryable get up to 2 retries with backoff. When a budget is exhausted, the cell keeps its content and says it can't update further this session. Calls and tokens are logged per session and exposed at `GET /api/sessions/{id}/metrics`.

### 4.6 Failure isolation

Each agent call runs with panic recovery. On failure the cell enters `failed`, keeps its last good content, and shows a Retry control. No other cell is affected. If a provider is unavailable, stored material remains readable and the dialogue input preserves the learner's text.

## 5. Evaluator

**Triggers:** `dialogue.user_message`, `practice.submitted`, `thought.response_submitted`, `viz.conclusion_submitted`, `concept.explored`, `retrieval.answered`, `document.object_challenged`, `user.reassess`, and relevant `cell.request`s. Triggering events within a short window (default 1.5s) coalesce into one evaluation.

**Output** (validated against a JSON schema, invalid output discarded and logged):

```json
{
  "intent": {"summary": "...", "kind": "foundation|contradiction|solve|tangent|connect|reconstruct", "shifted": false},
  "concepts": ["..."],
  "observations": ["facts directly seen"],
  "inferences": [{"claim": "...", "confidence": "low|medium|high"}],
  "evidence": [{"concept": "...", "tier": "weak|moderate|strong|durable", "correctness": "correct|partial|incorrect|na", "reasoning": "strong|adequate|weak|na", "rationale": "...", "uncertainty": "..."}],
  "gaps": ["..."],
  "open_questions": ["..."],
  "crystallize": [{"type": "insight|hypothesis|question|concept|connection", "content": "...", "authorship": "learner|ai"}],
  "readiness": "explain|harder|another_representation|apply|reconstruct",
  "recommendations": [{"action": "UPDATE|DEEPEN|SIMPLIFY|SPAWN|REPLACE|RETIRE", "cell_id": "...", "cell_type": "...", "objective": "...", "reason": "...", "priority": "low|medium|high"}]
}
```

**Evidence guard (deterministic, in Go, after the model responds):**

- Evidence must point to a learner-authored source event. Otherwise it is discarded, so exposure can never count as understanding.
- `durable` requires a `retrieval.answered` source from a later session. Otherwise it is downgraded to `strong`.
- `strong` requires learner reasoning applied to an unfamiliar scenario. Evidence from anything other than an evaluated submission or a substantive explanation is capped at `moderate`.

The Evaluator writes to context, evidence and the Living Document store. It has no access to workspace structure.

## 6. Orchestrator

**Inputs:** Evaluator recommendations, `cell.request`s, user actions, current workspace.

The model proposes through recommendations; `Decide` is a deterministic function that makes the final call and can be tested without a model:

```go
func Decide(ws WorkspaceState, recs []Recommendation, now time.Time, policy Policy) []Decision
```

**Rules, in order:**

1. Explicit user actions apply immediately and take precedence over anything pending.
2. Dialogue is never retired, replaced or suspended automatically.
3. Pinned cells are never automatically retired or replaced.
4. Busy cells are never changed. A pending change waits until the cell is no longer busy.
5. At most one unsolicited structural decision per adaptation window (default 5 minutes, `DEEPFLOW_ADAPT_WINDOW`). Further ones go into the pending queue, keeping only the newest for each cell.
6. Content adaptation (`UPDATE`, `DEEPEN`, `SIMPLIFY`) is allowed per cell at most once per 60 seconds, and is not counted as structural.
7. A `SPAWN` of a type already present is rewritten as `UPDATE` of that cell, unless the recommendation explains a distinct objective.
8. The workspace keeps at least 6 populated cells: a `RETIRE` that would go below this becomes `KEEP`, and the workspace holds at most 9. A `SPAWN` at the limit is rejected unless paired with a retirement. A user removal may take the count below 6; the Orchestrator then proposes a replacement cell, which is the one case where a spawn is immediate.
9. `SUSPEND` and `RESUME` are applied when a session ends and resumes, and are available for over-budget agents.
10. Every structural decision records the previous layout and retained artifacts so it can be undone from a toast, for as long as that session lasts.

Every decision is persisted as an `orchestrator.decision` event with its reason, and shown in a small "why did this change?" activity panel.

**Retirement sequence:** mark `retiring` so results are dropped, call `Retain`, persist artifacts (learner-authored content is always retained), cancel in-flight tasks, emit the layout change, mark `terminated`.

## 7. Provider

```go
type Provider interface {
    Name() string
    Stream(ctx context.Context, req TextRequest, onDelta func(string)) (Usage, error)
    Structured(ctx context.Context, req StructuredRequest, out any) (Usage, error)
}
```

- **Claude:** official Go SDK. Model `claude-opus-5-5` (override `DEEPFLOW_MODEL`). Uses `output_config.format` for structured output and streaming for dialogue. Effort is set per role: `low` for the evaluator and cell content, `medium` for dialogue, tunable. Server-side refusal fallback enabled (`fallbacks: "default"`). `stop_reason` is checked before content is read, and a refusal surfaces as a recoverable cell failure.
- **OpenAI:** official Go SDK, Responses API. Model `gpt-6.1-sol` (override `DEEPFLOW_MODEL`). Strict structured output, same effort levels per role, `store: false`. Selected with `DEEPFLOW_PROVIDER=openai`, or by `OPENAI_API_KEY` when no Anthropic key is set.
- **Mock:** selected when no API key is present, or with `DEEPFLOW_PROVIDER=mock`. Deterministic, keyword-driven output covering every schema, with a built-in indexing storyline so that the PRD journey can be exercised. Mock content is always labeled "Mock" in the UI.
- **Disclosure:** the header shows the active provider. Before the first call to a remote provider, the app shows a one-time notice, per provider, naming who receives learning content, and it must be acknowledged. Until then, the app stays on stored data and does not make calls.

## 8. Cell catalog

Agents supply only JSON data for fixed React components. No executable code crosses this boundary.

| Cell | Content | Learner interaction | Default span (of 12) |
|---|---|---|---|
| Dialogue | Streamed messages | Free text, Stop | 5 columns, full height |
| Concept | Concept list, explanation, analogy, distinction, prerequisite links | Select concept, "go deeper" | 3 |
| Visualization | Chart spec | Sliders, prediction, conclusion | 4 |
| Practice | Challenge, hints | Free-form answer, ask hint, get next | 4 |
| Thought Experiment | Scenario, changed constraint, reveal | Prediction with reasoning, then reveal | 4 |
| Understanding | Evidence-backed insights, developing hypotheses, gaps, uncertainty | Open evidence, reassess | 3 |
| Living Document | Objects grouped by type | Edit, challenge, reopen in dialogue, keep question, see evidence | 5 |

`Refresh` on any cell requests alternative content. Grid row placement uses CSS Grid with `grid-auto-flow: dense`, so the grid reflows with no gaps.

**Visualization spec:**

```json
{
  "title": "...", "question": "...",
  "params": [{"id": "selectivity", "label": "...", "min": 0, "max": 100, "step": 1, "value": 10, "unit": "%"}],
  "x": {"param": "selectivity", "label": "..."},
  "y": {"label": "..."},
  "series": [{"id": "scan", "label": "Full scan", "expr": "pages"}, {"id": "idx", "label": "Index", "expr": "depth + rows * selectivity / 100"}],
  "assumptions": ["..."],
  "basis": "illustrative"
}
```

- `basis` is `illustrative` unless a value comes from a validated built-in model. MVP generated specs are always `illustrative`, and the UI labels them so.
- **Expression language:** numbers, declared parameter names, `+ - * / ^`, parentheses, and `min max abs sqrt log log2 log10 exp floor ceil`. No other identifiers. The parser rejects anything else, and expressions are capped at 200 characters.
- The server validates each spec by parsing every expression and sampling 50 points across each slider's range. A spec with an unknown identifier, or non-finite values across the entire range, is rejected and the agent retries once. If it fails again, the cell shows the failure state.
- Go and TypeScript share a table of conformance cases in `internal/expr/testdata/cases.json`, read by tests on both sides, so the two evaluators can't drift.
- Slider moves recalculate in the browser with no server call. The chart is a hand-written SVG line chart, with a data table as the non-visual alternative.

## 9. Workspace behavior

- **Busy signal:** the client sends `POST /cells/{id}/busy` when a cell gains focus or has a non-empty draft, with a 20-second lease it renews while the learner types. Lease expiry clears busy, so a lost tab can't freeze adaptation.
- **Drafts:** autosaved with a debounce of 800 ms to `drafts`.
- **Transitions:** an in-place update never moves or remounts a cell. Replacement uses a 200 ms fade, disabled under `prefers-reduced-motion`.
- **Focus Mode:** one cell fills the workspace; Esc returns to the grid. Other agents keep running.
- **Removal:** a cell with learner content confirms that the content is retained in the artifacts list before removal.
- **Accessibility:** cells are labeled regions, all controls are keyboard reachable, focus is visible, chart data is available as a table, and live regions announce workspace changes politely. Text size is adjustable, and light and dark themes are provided.

## 10. Continuity

- **New pursuit:** title and optional inquiry. The Orchestrator creates the seven default cells without any configuration. Dialogue is usable at once, and other cells show a clear loading state, then content.
- **Consolidation (`POST /sessions/{id}/end`):** one structured call produces a summary, insights, gaps and open questions, each saved as objects with provenance. Agents are suspended and the snapshot is stored. If the call fails, the session still ends with what is already stored and is flagged for consolidation on the next open.
- **Return:** opening a pursuit whose last session has ended starts a new session with up to 3 retrieval questions built from prior evidence. The learner answers or skips each one. Answers are evaluated (the only route to `durable`) and shape the initial workspace. An open session resumes as it was, with no retrieval step.
- **Recovery:** on startup, sessions with no `ended_at` stay resumable. Messages, objects, evidence and drafts are written as they happen.
- **Export and delete:** `GET /api/pursuits/{id}/export` returns JSON. `DELETE /api/pursuits/{id}` asks for confirmation in the UI and deletes all of that pursuit's data.

## 11. API

```
GET    /api/status                     provider, consent state
POST   /api/consent
GET    /api/pursuits
POST   /api/pursuits
GET    /api/pursuits/{id}
DELETE /api/pursuits/{id}
GET    /api/pursuits/{id}/export
POST   /api/pursuits/{id}/sessions     start or resume a session
GET    /api/sessions/{id}              full workspace state
GET    /api/sessions/{id}/stream       SSE; Last-Event-ID replay, full resync on gap
POST   /api/sessions/{id}/end
POST   /api/sessions/{id}/undo
POST   /api/sessions/{id}/reassess
GET    /api/sessions/{id}/metrics
POST   /api/sessions/{id}/retrieval/{qid}
POST   /api/cells/{id}/action          typed cell interaction
POST   /api/cells/{id}/busy
PUT    /api/cells/{id}/draft
POST   /api/cells/{id}/pin | /unpin | /remove | /refresh | /retry
GET    /api/pursuits/{id}/objects
PATCH  /api/objects/{id}
POST   /api/objects/{id}/challenge
GET    /api/objects/{id}/evidence
```

SSE events: `cell.created`, `cell.updated`, `cell.status`, `cell.removed`, `cell.delta` (streamed text), `layout.changed`, `document.changed`, `understanding.changed`, `decision.made`, `error`. Each has a monotonically increasing per-session sequence. A client that detects a gap refetches full state.

## 12. Testing

**Go tests with the Mock provider:**

- `Decide`: table tests for each rule in section 6.
- Stale results: results from retired instances, or after a user edit, are dropped.
- Retirement: artifacts are kept, in-flight tasks canceled, no later updates.
- Failure isolation: a panicking agent leaves other cells working.
- Evidence guard: exposure is rejected; `durable` is only reachable through later-session retrieval.
- Expression language: shared conformance cases, including rejection of unknown identifiers.
- Persistence: restart restores workspace, drafts and document.
- HTTP: session lifecycle end to end, and rejection of non-loopback `Host`.

**Claude provider:** request construction tested against a fake HTTP server. Real output quality can't be verified without an API key, and that must be stated when reporting results.

**Browser walkthrough:** the PRD section 17 journey on the Mock provider, including a server restart, checked against acceptance criteria AC-01 to AC-08.
