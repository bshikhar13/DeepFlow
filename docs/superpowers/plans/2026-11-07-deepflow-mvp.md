# DeepFlow P0 MVP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the DeepFlow P0 MVP: a local Go binary serving an adaptive bento learning workspace with a real Orchestrator, Evaluator, Cell Agents and persistent Living Document.

**Architecture:** One Go process owns the harness and SQLite store and serves an embedded React app over REST and SSE. All model access goes through a `Provider` interface with Claude and Mock implementations. Structural decisions come only from a pure `Decide` function.

**Tech Stack:** Go 1.24, `net/http`, `modernc.org/sqlite`, `github.com/anthropics/anthropic-sdk-go`; React, TypeScript, Vite, Vitest.

**Spec:** `docs/superpowers/specs/2026-11-07-deepflow-mvp-design.md`. Read it first: this plan points to its sections rather than repeating them.

## Global Constraints

- Server binds `127.0.0.1:7878` only and rejects non-loopback `Host` headers.
- Data lives at `~/.deepflow/deepflow.db`, overridable with `DEEPFLOW_HOME`.
- Default model `claude-opus-5-5`, overridable with `DEEPFLOW_MODEL`. `DEEPFLOW_PROVIDER=mock` forces Mock; no `ANTHROPIC_API_KEY` also selects Mock.
- Workspace holds 6 to 9 cells outside Focus Mode, seven by default.
- Defaults: adaptation window 5 minutes (`DEEPFLOW_ADAPT_WINDOW`), 3 concurrent model calls, 40 calls per agent per session (Dialogue exempt), 90s task timeout, 2 retries, 1.5s evaluation coalescing, 60s content-update limit per cell, 20s busy lease, 800 ms draft debounce, 200 ms replacement fade.
- Agents supply data only. No model output is ever executed. The expression language in spec section 8 is the only computation a model can define.
- Understanding never shows mastery percentages. Exposure never produces evidence.
- Learner-authored content is never silently deleted.
- Mock content is labeled "Mock" in the UI.
- This folder is not a git repository and commits happen only on the user's request, so tasks have no commit steps. Each task ends with its test command green.

## Review Focus

1. Blank, whitespace-only or oversized learner input (over 20,000 characters) gets a clear 400 and the learner's text stays in the input; no event or evaluation occurs. Pinned in Task 8.
2. Model output that is malformed, truncated or a refusal leaves prior content on screen, marks only that cell `failed` or skips that evaluation, and never crashes. Pinned in Tasks 4 and 6.
3. Two tabs editing the same document object: the stale `PATCH` gets a 409 and doesn't overwrite newer text. Pinned in Task 8.
4. Server killed mid-stream: the learner's message survives, and the partial assistant text is kept and marked interrupted on reload. Pinned in Task 8.
5. Deleting a pursuit with a live session stops its runtime, and late results can't recreate rows. Pinned in Task 9.

---

### Task 1: Scaffold, domain types and store

**Files:**
- Create: `go.mod` (module `deepflow`), `Makefile`, `.gitignore`, `cmd/deepflow/main.go`
- Create: `internal/domain/{cell.go,event.go,learning.go}`
- Create: `internal/store/{store.go,schema.sql,pursuits.go,sessions.go,messages.go,objects.go,evidence.go,events.go,artifacts.go,drafts.go,settings.go}`
- Test: `internal/store/store_test.go`

**Interfaces:**
- Produces in `domain`:
  - `CellType` constants: `CellDialogue, CellConcept, CellVisualization, CellPractice, CellThought, CellUnderstanding, CellDocument`.
  - `CellStatus` constants matching the spec 4.3 lifecycle plus `StatusFailed`.
  - `Layout{Col, Row, ColSpan, RowSpan int}`.
  - `Cell{ID, Type, AgentInstanceID, Objective string; Status CellStatus; Pinned, Busy bool; Layout Layout; Content, LocalState json.RawMessage; ContextVersion, UserRevision int64}`.
  - `WorkspaceState{SessionID string; Cells []Cell; FocusedCell string; Revision int64; Pending []Decision}`.
  - `Event{ID, SessionID, Type, Origin, DedupeKey string; Payload json.RawMessage; ContextVersion int64; CreatedAt time.Time}` with `EventType` constants for every type in spec 4.1.
  - `Action` constants for the ten PRD actions; `Decision{Action Action; CellID string; CellType CellType; Objective, Reason string; Unsolicited bool}`.
  - `ConceptObject`, `Evidence`, `Artifact`, `Pursuit`, `Session`, `Message` with the spec section 3 fields; `Message` adds `Interrupted bool`.
- Produces in `store`: `Open(path string) (*Store, error)`; `(*Store).Close() error`; CRUD methods named `CreatePursuit`, `GetPursuit`, `ListPursuits`, `DeletePursuit` (transactional cascade), `CreateSession`, `GetSession`, `LatestSession`, `EndSession`, `SaveSnapshot(sessionID string, ws domain.WorkspaceState) error`, `AddMessage`, `UpdateMessage`, `ListMessages`, `UpsertObject`, `UpdateObjectContent(id, content, authorship string, expectedVersion int) (domain.ConceptObject, error)` returning `store.ErrVersionConflict` on mismatch, `ListObjects`, `ObjectVersions`, `AddEvidence`, `ListEvidence`, `AppendEvent(e domain.Event) (inserted bool, err error)`, `AddArtifact`, `ListArtifacts`, `SaveDraft`, `GetDrafts`, `GetSetting`, `SetSetting`.

- [ ] **Step 1: Write failing tests:** `TestPursuitRoundTrip`; `TestDeletePursuitCascades` (session, messages, objects, evidence, events, artifacts and drafts all gone); `TestUpdateObjectBumpsVersionAndKeepsHistory` (version 2, `ObjectVersions` returns both); `TestUpdateObjectConflict` (`expectedVersion` 1 when at 2 returns `ErrVersionConflict`); `TestAppendEventDedupes` (second insert with same `DedupeKey` returns `inserted == false`); `TestSnapshotRoundTrip`; `TestDataSurvivesReopen` (close, `Open`, read back).
- [ ] **Step 2: Run** `go test ./internal/store/`. Expected: FAIL, packages missing.
- [ ] **Step 3: Implement** domain types, embedded `schema.sql` per spec section 3, repositories. Use WAL mode and `foreign_keys=ON`; `ON DELETE CASCADE` from pursuits and sessions. `main.go` opens the store and exits cleanly for now. `Makefile` targets: `web` (`npm --prefix web ci && npm --prefix web run build`), `build` (depends on `web`, runs `go build -o deepflow ./cmd/deepflow`), `test` (`go test ./... && npm --prefix web test`).
- [ ] **Step 4: Run** `go test ./internal/store/ && go vet ./...`. Expected: PASS.

### Task 2: Safe expression language (Go)

**Files:**
- Create: `internal/expr/{expr.go,validate.go}`, `internal/expr/testdata/cases.json`
- Test: `internal/expr/expr_test.go`

**Interfaces:**
- Produces: `expr.Parse(src string, allowed []string) (*Expr, error)`; `(*Expr).Eval(vars map[string]float64) float64`; `expr.ValidateSpec(spec domain.VizSpec) error`.
- Produces in `domain`: `VizSpec`, `VizParam`, `VizAxis`, `VizSeries` matching the spec section 8 JSON.
- `cases.json` format: `{"valid":[{"src","vars","want"}],"invalid":[{"src","allowed"}]}`, with `want` of `null` for non-finite. Task 10 reads the same file.

- [ ] **Step 1: Write failing tests:** `TestConformanceCases` over `cases.json`, which must include: precedence (`2+3*4` = 14), right-associative power (`2^3^2` = 512), unary minus (`-2^2` = -4), each function in the spec's list, `1/0` is `null`, and invalid entries `process.exit()`, `constructor`, unknown identifier `y`, `1+`, and a 201-character string. `TestValidateSpecRejectsUnknownIdentifier`; `TestValidateSpecRejectsAllNonFinite` (`1/0` series); `TestValidateSpecAllowsPartialNonFinite` (`1/x`, x from 0 to 10); `TestValidateSpecRejectsBadSliderRange` (`min >= max`, `step <= 0`, `value` outside range); `TestValidateSpecRequiresBasis` (only `illustrative` accepted in MVP).
- [ ] **Step 2: Run** `go test ./internal/expr/`. Expected: FAIL.
- [ ] **Step 3: Implement** a recursive-descent parser over the spec section 8 grammar. `ValidateSpec` samples 50 points across each slider's range with other parameters at their `value`.
- [ ] **Step 4: Run** `go test ./internal/expr/`. Expected: PASS.

### Task 3: Provider interface, Mock and Claude

**Files:**
- Create: `internal/provider/{provider.go,mock.go,claude.go}`
- Test: `internal/provider/{mock_test.go,claude_test.go}`

**Interfaces:**
- Produces:
  ```go
  type Role string // RoleDialogue, RoleEvaluator, RoleCell, RoleConsolidate
  type TextRequest struct { Role Role; System string; Messages []Turn; MaxTokens int }
  type StructuredRequest struct { Role Role; Name, System, Prompt string; Schema map[string]any; MaxTokens int }
  type Usage struct { InputTokens, OutputTokens int64 }
  type Provider interface {
      Name() string
      Stream(ctx context.Context, req TextRequest, onDelta func(string)) (Usage, error)
      Structured(ctx context.Context, req StructuredRequest, out any) (Usage, error)
  }
  var ErrRefused, ErrMalformed, ErrTruncated error
  func FromEnv() Provider
  ```
- `Mock` adds `Script` hooks: `Fail(name string, err error)` and `Delay(name string, d time.Duration)` for tests in later tasks.

- [ ] **Step 1: Read** the Go SDK guide at the claude-api skill's `go/claude-api/README.md` and `streaming.md` before writing `claude.go`; do not guess bindings.
- [ ] **Step 2: Write failing tests:** `TestMockDeterministic`; `TestMockHonorsFail`; `TestMockHonorsContextCancel`. Using `httptest` as the API base URL: `TestClaudeRequestShape` (model `claude-opus-5-5`, `output_config.format` present, no `thinking`, `temperature` or forced `tool_choice`, effort `low` for `RoleEvaluator` and `RoleCell`, `medium` for `RoleDialogue`, the fallback beta header and `fallbacks: "default"`); `TestClaudeRefusalReturnsErrRefused`; `TestClaudeMaxTokensReturnsErrTruncated`; `TestClaudeInvalidJSONReturnsErrMalformed`; `TestFromEnvSelectsMockWithoutKey`; `TestFromEnvHonorsModelOverride`.
- [ ] **Step 3: Implement.** Claude checks `stop_reason` before reading content. Mock returns schema-valid JSON keyed on `req.Name` (names from Tasks 5, 6 and 9) and keyword-driven dialogue, following the indexing storyline: the phrase "always faster" yields a misconception observation; "full scan" or "selectivity" with reasoning yields moderate-to-strong evidence and an insight; "optimizer" or "query planner" yields `intent.shifted = true` and a `REPLACE` of the Concept cell with a planner-focused Visualization objective. Off-storyline topics produce generic, topic-named content.
- [ ] **Step 4: Run** `go test ./internal/provider/`. Expected: PASS.

### Task 4: Harness runtime

**Files:**
- Create: `internal/harness/{bus.go,context.go,runtime.go,scheduler.go,agent.go}`
- Test: `internal/harness/{bus_test.go,context_test.go,runtime_test.go}`

**Interfaces:**
- Consumes: `store.Store`, `domain.*`, `provider.Provider`.
- Produces:
  ```go
  type Bus struct{...}; func NewBus(s *store.Store) *Bus
  func (*Bus) Publish(e domain.Event) error   // persist first, skip duplicates, then fan out
  func (*Bus) Subscribe(fn func(domain.Event)) (cancel func())

  type LearningContext struct { Version int64; PursuitTitle, Inquiry string; Recent []Interaction; Concepts, Hypotheses, Misconceptions, OpenQuestions []string; Demonstrated []DemonstratedItem; Cells []CellResponsibility; Objects []domain.ConceptObject }
  type ContextStore struct{...}
  func (*ContextStore) Apply(fn func(*LearningContext)) int64 // bumps Version
  func (*ContextStore) Project(t domain.CellType) ContextView

  type AgentInput struct { Cell domain.Cell; Context ContextView; Provider provider.Provider }
  type AgentOutput struct { Content, LocalState json.RawMessage; Publish []domain.Event; Requests []CellRequest; Artifacts []domain.Artifact }
  type CellRequest struct { Kind, Reason string; CellType domain.CellType; Objective string }
  type Directive struct { Kind, Objective string }
  type Agent interface { /* exactly as spec 4.3 */ }
  type AgentFactory func(domain.CellType) Agent

  type Runtime struct{...}
  func NewRuntime(cfg RuntimeConfig, b *Bus, c *ContextStore, f AgentFactory, sink Sink) *Runtime
  func (*Runtime) Start(cell domain.Cell)                     // begins Init on the cell's inbox goroutine
  func (*Runtime) Dispatch(cellID string, ev domain.Event)
  func (*Runtime) Direct(cellID string, d Directive)
  func (*Runtime) BumpUserRevision(cellID string)
  func (*Runtime) SetSuspended(cellID string, suspended bool)
  func (*Runtime) Retire(cellID string) []domain.Artifact     // spec section 6 retirement sequence
  func (*Runtime) Stop()                                      // cancels all tasks; later results dropped
  type Sink interface { CellUpdated(domain.Cell); CellStatus(cellID string, s domain.CellStatus); Delta(cellID, text string); Request(cellID string, r CellRequest) }
  ```

- [ ] **Step 1: Write failing tests:** `TestBusPersistsBeforeDeliver`; `TestBusDropsDuplicates`; `TestContextProjectionOmitsDialogueForViz`; `TestContextVersionMonotonic`; `TestDeltaFromRetiredInstanceDropped`; `TestResultDroppedAfterRetire` (Mock delay, `Retire` mid-task, no `CellUpdated`); `TestResultDroppedAfterUserRevisionChange`; `TestOlderTaskCannotOverwriteNewer`; `TestPanicMarksOnlyThatCellFailed` (other cell still answers); `TestProviderErrorKeepsLastContent` (for each of `ErrMalformed`, `ErrTruncated`, `ErrRefused`); `TestSemaphoreCapsConcurrency` (never over 3 in flight); `TestDialogueNotQueuedBehindCells`; `TestBudgetExhaustedKeepsContent` (41st call not made; Dialogue exempt); `TestTaskTimeoutMarksFailed`; `TestSuspendedCellQueuesNoWork`.
- [ ] **Step 2: Run** `go test -race ./internal/harness/`. Expected: FAIL.
- [ ] **Step 3: Implement.** One goroutine per live cell reading a buffered inbox; every task runs with `recover`, timeout context and the three stale-result rules in spec 4.4. The scheduler is a two-priority semaphore (Dialogue first).
- [ ] **Step 4: Run** `go test -race ./internal/harness/`. Expected: PASS.

### Task 5: Cell Agents

**Files:**
- Create: `internal/agents/{factory.go,dialogue.go,concept.go,visualization.go,practice.go,thought.go,understanding.go,document.go,prompts.go,schemas.go}`
- Test: `internal/agents/agents_test.go`

**Interfaces:**
- Consumes: `harness.Agent`, `AgentInput`, `AgentOutput`; `expr.ValidateSpec`; `provider.Provider`.
- Produces: `agents.New(t domain.CellType) harness.Agent`; structured request names `concept.content`, `viz.spec`, `practice.challenge`, `practice.evaluate`, `thought.scenario`, `thought.respond`, `understanding.synthesize`.
- Produces the cell content JSON shapes the frontend depends on, as exported Go structs: `ConceptContent{Title, Definition, FirstPrinciples, Analogy, Distinction string; Prerequisites, Related []string}`; `PracticeContent{Prompt string; Hints []string; Feedback *PracticeFeedback}` with `PracticeFeedback{Correctness, Reasoning, Explanation string}`; `ThoughtContent{Scenario, Constraint, Question, Reveal string}` where `Reveal` is empty until the learner submits; `UnderstandingContent{Insights []UnderstandingItem; Hypotheses, Gaps []string}` with `UnderstandingItem{Text, Tier, Uncertainty string; EvidenceIDs []string}`; `DocumentContent{ObjectCount int}` (objects load from the API); `DialogueContent{Streaming bool}` (messages load from the API); `domain.VizSpec`.
- Local-state actions accepted through `Handle`: `practice.submitted`, `practice.hint`, `practice.next`, `thought.response_submitted`, `viz.conclusion_submitted`, `concept.explored`, `dialogue.user_message`.

- [ ] **Step 1: Write failing tests:** `TestEachAgentInitProducesValidContent` (all seven types with Mock); `TestDialogueStreamsAndPublishesAgentMessage`; `TestVizRetriesOnceOnInvalidSpecThenFails` (Mock returns bad expression twice: exactly two calls, error returned); `TestPracticeScoresCorrectnessAndReasoningSeparately`; `TestPracticeHintDoesNotPublishEvidenceEvent`; `TestThoughtRevealHiddenUntilSubmit`; `TestRetainMarksLearnerAuthored` (Practice answer in local state yields `LearnerAuthored: true`); `TestUnderstandingHasNoPercentages` (schema and struct have no numeric score field); `TestConceptRequestsReplacementWhenFulfilled` (emits a `CellRequest`, changes nothing else).
- [ ] **Step 2: Run** `go test ./internal/agents/`. Expected: FAIL.
- [ ] **Step 3: Implement.** Prompts in `prompts.go` state each role's PRD purpose from spec section 8 and PRD section 12; they require challenging, learner-generative content and never withhold a direct answer when asked. Schemas use `additionalProperties: false` and full `required` lists. The Understanding agent reads evidence through `ContextView` and calls the model only when evidence has changed.
- [ ] **Step 4: Run** `go test ./internal/agents/`. Expected: PASS.

### Task 6: Evaluator and evidence guard

**Files:**
- Create: `internal/evaluator/{evaluator.go,schema.go,guard.go}`
- Test: `internal/evaluator/{evaluator_test.go,guard_test.go}`

**Interfaces:**
- Consumes: `Bus.Subscribe`, `ContextStore.Apply`, `store` evidence and objects, `provider.Provider`.
- Produces: `Output` struct matching spec section 5 JSON; `Recommendation{Action domain.Action; CellID string; CellType domain.CellType; Objective, Reason, Priority string}`; `evaluator.New(cfg, deps) *Evaluator`; `(*Evaluator).Start() (stop func())`; `OnResult func(Output, []domain.Event)` delivered to the Orchestrator; `Guard(ev EvidenceClaim, src domain.Event, currentSession string) (EvidenceClaim, bool)`; structured request name `evaluator.assess`.

- [ ] **Step 1: Write failing tests:** Guard: `TestGuardRejectsNonLearnerSource` (source `dialogue.agent_message` is dropped); `TestGuardDurableRequiresLaterSessionRetrieval` (same-session retrieval becomes `strong`; `practice.submitted` claiming durable becomes at most `strong`); `TestGuardCapsNonSubmissionAtModerate` (`concept.explored` is dropped as exposure; a short dialogue message claiming strong becomes `moderate`). Evaluator: `TestSliderDoesNotTrigger` (`viz.parameter_changed` makes no provider call); `TestScrollIdleNeverEvaluated`; `TestCoalescesBurst` (three events within 1.5s make one call); `TestMalformedOutputDiscarded` (no state change, no crash, later events still processed); `TestCrystallizePreservesAuthorship` (`ai` stays `ai`); `TestInsightWithoutLearnerEvidenceStaysLowConfidence`; `TestContextVersionBumpsAndEmitsContextUpdated`.
- [ ] **Step 2: Run** `go test ./internal/evaluator/`. Expected: FAIL.
- [ ] **Step 3: Implement.** Trigger set exactly as spec section 5. Prompt instructs separating observation from inference. Deduplicate crystallized objects by normalized content within a pursuit, updating rather than duplicating.
- [ ] **Step 4: Run** `go test ./internal/evaluator/`. Expected: PASS.

### Task 7: Orchestrator

**Files:**
- Create: `internal/orchestrator/{policy.go,decide.go,apply.go,layout.go,undo.go}`
- Test: `internal/orchestrator/{decide_test.go,apply_test.go,layout_test.go}`

**Interfaces:**
- Consumes: `harness.Runtime`, `evaluator.Recommendation`, `domain.WorkspaceState`.
- Produces: `Policy{AdaptWindow, ContentCooldown time.Duration; MinCells, MaxCells int}` with `DefaultPolicy()`; `Decide(ws WorkspaceState, recs []Recommendation, lastStructural time.Time, lastUpdate map[string]time.Time, now time.Time, p Policy) (apply []domain.Decision, pending []domain.Decision)` (this extends the spec's sketch with the timing state that keeps it pure); `InitialCells(inquiry string) []domain.Cell`; `Reflow(cells []domain.Cell) []domain.Cell`; `Orchestrator` with `Handle(rec []Recommendation)`, `UserPin`, `UserUnpin`, `UserRemove`, `UserRefresh`, `SetBusy(cellID string, until time.Time)`, `Undo() error`, `Tick(now time.Time)` (promotes pending changes).

- [ ] **Step 1: Write failing tests:** one table case per rule in spec section 6: `dialogue_never_retired`, `pinned_not_replaced`, `busy_deferred_to_pending`, `one_structural_per_window` (two `REPLACE`s: one now, one pending), `pending_keeps_newest_per_cell`, `content_update_rate_limited`, `content_update_not_counted_structural`, `spawn_existing_type_becomes_update`, `spawn_same_type_distinct_objective_allowed`, `retire_below_min_becomes_keep`, `spawn_at_max_rejected`, `spawn_at_max_with_retire_allowed`. Also `TestInitialCellsSevenWithDialogueUsable`; `TestUserRemoveBelowMinSpawnsReplacementImmediately`; `TestUserPinOverridesPendingReplace`; `TestBusyLeaseExpires` (no renewal after 20s, pending applied at next `Tick`); `TestReplaceRetainsArtifactsAndTerminatesAgent`; `TestUndoRestoresPriorCellAndLayout`; `TestUndoAfterSecondChangeRevertsLatestOnly`; `TestReflowNoOverlapAndDialogueSpans5` for 6, 7, 8 and 9 cells; `TestDecisionPersistedWithReason`.
- [ ] **Step 2: Run** `go test ./internal/orchestrator/`. Expected: FAIL.
- [ ] **Step 3: Implement.** `Decide` must be pure, with no I/O or clock reads. Default spans per spec section 8; `Reflow` assigns the Dialogue cell a full-height left column and lays others in the remaining columns by default span. Every structural change snapshots via `store.SaveSnapshot`.
- [ ] **Step 4: Run** `go test ./internal/orchestrator/`. Expected: PASS.

### Task 8: Session service, HTTP API and SSE

**Files:**
- Create: `internal/session/{manager.go,live.go}`, `internal/api/{server.go,pursuits.go,sessions.go,cells.go,objects.go,sse.go,middleware.go}`, `internal/api/static.go`, `web/embed.go` (package `web`, `//go:embed all:dist`, because `go:embed` can't reach parent directories), `web/dist/.gitkeep`
- Modify: `cmd/deepflow/main.go`
- Test: `internal/session/manager_test.go`, `internal/api/api_test.go`

**Interfaces:**
- Consumes: everything above.
- Produces: `session.Manager` with `Start(pursuitID string) (*Live, error)` (resumes an open session or creates one), `Get(sessionID string) (*Live, bool)`, `Close(pursuitID string)`, `Shutdown()`. `Live` wires Bus, Context, Runtime, Evaluator and Orchestrator and implements `harness.Sink`, assigning a per-session sequence to each SSE event. `api.New(m *session.Manager, s *store.Store, p provider.Provider) http.Handler`. Routes and SSE event names exactly as spec section 11. Error body: `{"error":{"code":string,"message":string}}`.
- `POST /api/cells/{id}/action` body: `{"type":<event type>,"payload":{...},"client_id":string}`. `client_id` becomes the dedupe key.

- [ ] **Step 1: Write failing tests:** `TestRejectsNonLoopbackHost` (403); `TestCreatePursuitStartsSevenCells` (AC-01: 7 populated within test timeout, all with agent instance IDs); `TestBlankAndOversizedInputRejected` (whitespace and 20,001 characters give 400, no event row); `TestDuplicateClientIDIgnored`; `TestStaleObjectPatchConflict` (409 with current object); `TestKillMidStreamKeepsPartial` (Mock delay; shut down manager mid-delta; reopen; user message intact and partial assistant message has `Interrupted: true`); `TestRestartRestoresWorkspaceAndDrafts` (AC-07); `TestRemoveRetainsArtifact` (AC-08); `TestPinSurvivesAdaptation` (AC-02); `TestOneCellActionLeavesOthersUsable` (AC-03, with one Mock call delayed); `TestSSEReplayFromLastEventID`; `TestSSEGapSignalsResync`; `TestNoClaudeCallsBeforeConsent` (provider named `claude`, no consent: cell actions return 409 `consent_required` and no calls occur); `TestMetricsCountCalls`.
- [ ] **Step 2: Run** `go test -race ./internal/session/ ./internal/api/`. Expected: FAIL.
- [ ] **Step 3: Implement.** `static.go` serves `web.Dist` with SPA fallback to `index.html`, and shows a plain "run `make web`" page if the build is missing. `main.go` handles `SIGINT` with graceful shutdown and prints the URL. Use Go 1.22 `ServeMux` method patterns. A ticker calls `Orchestrator.Tick` every 5s.
- [ ] **Step 4: Run** `go test -race ./internal/session/ ./internal/api/`. Expected: PASS.

### Task 9: Continuity

**Files:**
- Create: `internal/session/{consolidate.go,retrieval.go,export.go}`, `internal/api/continuity.go`
- Test: `internal/session/continuity_test.go`

**Interfaces:**
- Produces: `(*Live).End(ctx) (domain.Session, error)`; structured request names `session.consolidate`, `session.retrieval`; `Manager.RetrievalQuestions(sessionID string) ([]RetrievalQuestion, error)` with `RetrievalQuestion{ID, Prompt, Concept string; Status string}`; `Manager.AnswerRetrieval(sessionID, qid, answer string, skip bool) error`; `Manager.Export(pursuitID string) (ExportBundle, error)`; `Manager.Delete(pursuitID string) error`. `sessions` gains `needs_consolidation`; `retrieval_questions` is stored as JSON on the session.

- [ ] **Step 1: Write failing tests:** `TestEndConsolidatesWithProvenance` (AC-06: objects saved with authorship and source event); `TestEndSucceedsWhenModelFails` (`needs_consolidation` true; retried on next open); `TestReturnSessionOffersUpToThreeRetrievalQuestions`; `TestResumeOpenSessionHasNoRetrieval`; `TestRetrievalAnswerCanProduceDurable` (later session gives durable tier); `TestRetrievalSkipRecordsNoEvidence`; `TestRetrievalInformsInitialWorkspace` (Mock planner storyline: incorrect answer gives a Concept objective on that concept); `TestFirstSessionNoRetrieval`; `TestDocumentPersistsAcrossSessions`; `TestExportIncludesAllEntities`; `TestDeleteLiveSessionStopsRuntime` (Mock delay in flight, delete, wait for delay to pass, zero rows for that pursuit and no panic).
- [ ] **Step 2: Run** `go test -race ./internal/session/`. Expected: FAIL.
- [ ] **Step 3: Implement** per spec section 10, adding `needs_consolidation` and `retrieval_questions` to `schema.sql`. Every persistence path in `Live` checks `Live.stopped` first, so nothing is written after a stop. `End` suspends all agents through `Runtime.SetSuspended`.
- [ ] **Step 4: Run** `go test -race ./...`. Expected: PASS.

### Task 10: Frontend foundation

**Files:**
- Create: `web/package.json`, `web/vite.config.ts`, `web/tsconfig.json`, `web/index.html`
- Create: `web/src/{main.tsx,App.tsx,api.ts,types.ts,state.ts,theme.css}`, `web/src/lib/{expr.ts,chart.ts}`
- Create: `web/src/views/{Home.tsx,Workspace.tsx,Retrieval.tsx,ConsentNotice.tsx}`, `web/src/components/{CellFrame.tsx,ActivityPanel.tsx,UndoToast.tsx,Header.tsx}`
- Test: `web/src/lib/expr.test.ts`, `web/src/state.test.ts`

**Interfaces:**
- Consumes: API and SSE from Task 8; `internal/expr/testdata/cases.json`.
- Produces: `api` client functions, one per route; `useSession(sessionId)` returning `{state, dispatch, connection}`; `reduce(state, serverEvent)` in `state.ts`; `evalExpr(src: string, vars: Record<string, number>): number`; `parseExpr`. `CellFrame` props: `{cell: Cell; title: string; onExpand, onPin, onRefresh, onRemove, onRetry; children}`. `CellRegistry: Record<CellType, React.FC<CellProps>>`, with `CellProps = {cell, session, actions}`, is filled in by Task 11.

- [ ] **Step 1: Write failing tests:** `expr.test.ts` runs every `cases.json` entry; `state.test.ts`: `cell.delta` appends only to the matching cell; `cell.removed` removes; a sequence gap sets `needsResync`; `cell.updated` for a cell with a newer `userRevision` keeps the local draft.
- [ ] **Step 2: Run** `npm --prefix web test`. Expected: FAIL.
- [ ] **Step 3: Implement.** Grid from `cell.layout` with CSS Grid (12 columns, `minmax(0,1fr)`); below 1024px show a "best on a larger window" notice with single-column flow. `Home` lists pursuits, creates one (title plus optional inquiry), and offers export and delete with confirmation. `Workspace` handles Focus Mode (Esc to exit, focus restored), the end-session action, a provider badge (Mock or Claude), consent notice, undo toast, activity panel ("why did this change?"), text-size control, theme toggle. Cells keep stable React keys so updates never remount. Vite proxies `/api` to `127.0.0.1:7878` in dev. Respect `prefers-reduced-motion`.
- [ ] **Step 4: Run** `npm --prefix web test && npm --prefix web run build`. Expected: PASS and `web/dist/index.html` exists.

### Task 11: Cell components

**Files:**
- Create: `web/src/cells/{registry.ts,Dialogue.tsx,Concept.tsx,Visualization.tsx,Practice.tsx,Thought.tsx,Understanding.tsx,Document.tsx,useDraft.ts,useBusy.ts,LineChart.tsx}`
- Test: `web/src/cells/cells.test.tsx`

**Interfaces:**
- Consumes: `CellProps`, content structs from Task 5, `evalExpr`, `api`.
- Produces: `CellRegistry` entries for all seven types; `useDraft(cellId)` (800 ms debounce, returns `{text, setText, clear}`); `useBusy(cellId, active)` (20s lease, 10s renew).

- [ ] **Step 1: Write failing tests:** `Visualization: slider change recalculates with no fetch`; `Visualization: shows Illustrative label and assumptions`; `Visualization: non-finite points leave a gap, not NaN path data`; `Visualization: data table alternative present`; `Practice: separate correctness and reasoning labels`; `Thought: reveal absent until submit`; `Understanding: renders no percent sign`; `Document: AI and learner authorship visibly distinct`; `Document: 409 on save shows conflict and keeps learner's text`; `Dialogue: failed send keeps text in input`; `Dialogue: interrupted message labeled`; `useBusy: stops renewing when inactive`.
- [ ] **Step 2: Run** `npm --prefix web test`. Expected: FAIL.
- [ ] **Step 3: Implement** with React Testing Library and jsdom. Dialogue: `Enter` to send, `Shift+Enter` for newline, Stop button, ARIA live region, renders Markdown via a small safe renderer (text nodes only, no `dangerouslySetInnerHTML`). Document: edit in place, challenge, "reopen in dialogue", show evidence. Concept: "go deeper" publishes `concept.explored`.
- [ ] **Step 4: Run** `npm --prefix web test && make build`. Expected: PASS and `./deepflow` binary exists.

### Task 12: End-to-end verification

**Files:**
- Create: `README.md` (install, run, env vars, data location, how Mock differs from Claude)
- Modify: whatever the walkthrough shows is broken.

- [ ] **Step 1: Run** `make test`. Expected: all green.
- [ ] **Step 2: Start** `DEEPFLOW_HOME=<scratchpad>/e2e ./deepflow` and drive the built-in browser at 1440×900 through PRD section 17: create "Database Systems" with the inquiry "Why can database indexes sometimes make queries slower?"; confirm seven cells (AC-01).
- [ ] **Step 3: Exercise:** send "Indexes always make searches faster"; move the slider and submit a conclusion; explain why a full-table scan can win; confirm Understanding and Living Document update, with authorship shown, and that slider moves alone added no evidence (AC-05, AC-06).
- [ ] **Step 4: Adapt:** send "How does a query optimizer decide?"; confirm one structural replacement, stable other cells, activity reason, working undo; pin a cell and confirm it's preserved; type in a draft and confirm that cell isn't changed (AC-02, AC-04, AC-08).
- [ ] **Step 5: Recover:** kill the server mid-session, restart, reopen; confirm workspace and drafts restored (AC-07). End session, reopen, answer retrieval; confirm durable evidence appears.
- [ ] **Step 6: Check** console errors at zero, keyboard navigation through cell controls, dark theme, Focus Mode. Fix anything found and rerun `make test`.
