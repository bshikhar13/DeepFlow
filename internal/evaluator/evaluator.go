// Package evaluator interprets learner activity. It records evidence and
// shared-context changes, and issues recommendations; it cannot touch the
// workspace itself.
package evaluator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"deepflow/internal/domain"
	"deepflow/internal/harness"
	"deepflow/internal/provider"
	"deepflow/internal/store"
)

const (
	firstTokenLimit = 32000
	retryTokenLimit = 64000
)

// Config wires an Evaluator to its session.
type Config struct {
	SessionID string
	PursuitID string
	Coalesce  time.Duration
	Timeout   time.Duration
	Provider  provider.Provider
	Store     *store.Store
	Context   *harness.ContextStore
	Bus       *harness.Bus
	OnResult  func(Output)
}

// Evaluator runs event-driven evaluations for one session, one at a time.
type Evaluator struct {
	cfg     Config
	mu      sync.Mutex
	pending []domain.Event
	signal  chan struct{}
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// New creates an Evaluator.
func New(cfg Config) *Evaluator {
	if cfg.Coalesce == 0 {
		cfg.Coalesce = 1500 * time.Millisecond
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Evaluator{cfg: cfg, signal: make(chan struct{}, 1), ctx: ctx, cancel: cancel}
}

func isTrigger(t domain.EventType) bool {
	switch t {
	case domain.EvUserMessage, domain.EvThoughtSubmitted, domain.EvQuizBatch,
		domain.EvConceptExplored, domain.EvRetrievalAnswered, domain.EvObjectChallenged, domain.EvUserReassess, domain.EvCellRequest:
		return true
	}
	return false
}

// Start begins consuming events and returns a function that stops the
// Evaluator. Nothing is written after stop returns.
func (e *Evaluator) Start() (stop func()) {
	unsub := e.cfg.Bus.Subscribe(e.onEvent)
	e.wg.Add(1)
	go e.loop()
	var once sync.Once
	return func() {
		once.Do(func() {
			unsub()
			e.cancel()
			e.wg.Wait()
		})
	}
}

func (e *Evaluator) onEvent(ev domain.Event) {
	if ev.SessionID != e.cfg.SessionID || !isTrigger(ev.Type) {
		return
	}
	e.mu.Lock()
	e.pending = append(e.pending, ev)
	e.mu.Unlock()
	select {
	case e.signal <- struct{}{}:
	default:
	}
}

func (e *Evaluator) loop() {
	defer e.wg.Done()
	for {
		select {
		case <-e.ctx.Done():
			return
		case <-e.signal:
		}
		select {
		case <-e.ctx.Done():
			return
		case <-time.After(e.cfg.Coalesce):
		}
		e.mu.Lock()
		batch := e.pending
		e.pending = nil
		e.mu.Unlock()
		if len(batch) > 0 {
			e.evaluate(batch)
		}
	}
}

func (e *Evaluator) evaluate(batch []domain.Event) {
	ctx, cancel := context.WithTimeout(e.ctx, e.cfg.Timeout)
	defer cancel()
	var out Output
	req := provider.StructuredRequest{
		Role: provider.RoleEvaluator, Name: "evaluator.assess", System: system, Prompt: e.prompt(batch), Schema: schema, MaxTokens: firstTokenLimit,
	}
	_, err := e.cfg.Provider.Structured(ctx, req, &out)
	if errors.Is(err, provider.ErrTruncated) && e.ctx.Err() == nil {
		// A cut-off evaluation is lost entirely, so one retry with more room and a
		// firmer brief is worth the cost.
		log.Printf("evaluator: retrying: %v", err)
		out = Output{}
		req.MaxTokens = retryTokenLimit
		req.System = system + "\n\nBe extremely brief: at most 3 items in any list, and under 15 words per item."
		_, err = e.cfg.Provider.Structured(ctx, req, &out)
	}
	if err != nil {
		if e.ctx.Err() == nil {
			log.Printf("evaluator: %v", err)
		}
		return
	}
	if e.ctx.Err() != nil {
		return
	}
	out.Recommendations = validRecommendations(out.Recommendations)
	if err := e.apply(out, batch); err != nil {
		log.Printf("evaluator: apply: %v", err)
		return
	}
	if e.ctx.Err() == nil && e.cfg.OnResult != nil {
		e.cfg.OnResult(out)
	}
}

func (e *Evaluator) prompt(batch []domain.Event) string {
	c := e.cfg.Context.Snapshot()
	var b strings.Builder
	fmt.Fprintf(&b, "Topic: %s\n", c.PursuitTitle)
	if c.Inquiry != "" {
		fmt.Fprintf(&b, "Central inquiry: %s\n", c.Inquiry)
	}
	state, _ := json.Marshal(map[string]any{
		"intent": c.Intent, "concepts": c.Concepts, "hypotheses": c.Hypotheses, "misconceptions": c.Misconceptions,
		"gaps": c.Gaps, "openQuestions": c.OpenQuestions, "demonstrated": c.Demonstrated, "recent": c.Recent, "cells": c.Cells,
	})
	fmt.Fprintf(&b, "Current learner state: %s\n\nNew events:\n", state)
	texts := make([]string, 0, len(batch))
	for _, ev := range batch {
		fmt.Fprintf(&b, "- id=%s type=%s\n", ev.ID, ev.Type)
		texts = append(texts, eventText(ev))
	}
	fmt.Fprintf(&b, "\nLearner's words, in order:\n<trigger>%s</trigger>", strings.Join(texts, "\n"))
	return b.String()
}

func (e *Evaluator) apply(out Output, batch []domain.Event) error {
	var learnerSource *domain.Event
	for i := range batch {
		if batch[i].Type.LearnerAuthored() {
			learnerSource = &batch[i]
		}
	}
	sourceFor := func(id string) *domain.Event {
		for i := range batch {
			if batch[i].ID == id {
				return &batch[i]
			}
		}
		if learnerSource != nil {
			return learnerSource
		}
		return &batch[len(batch)-1]
	}

	var items []harness.DemonstratedItem
	best := ""
	for _, claim := range out.Evidence {
		src := sourceFor(claim.SourceEventID)
		c, ok := Guard(claim, *src)
		if !ok {
			continue
		}
		rec := domain.Evidence{
			PursuitID: e.cfg.PursuitID, SessionID: e.cfg.SessionID, Concept: c.Concept, LearnerAction: string(src.Type),
			Tier: domain.EvidenceTier(c.Tier), Correctness: c.Correctness, Reasoning: c.Reasoning,
			Rationale: c.Rationale, Uncertainty: c.Uncertainty, SourceEventID: src.ID,
		}
		if err := e.cfg.Store.AddEvidence(&rec); err != nil {
			return err
		}
		best = strongerTier(best, c.Tier)
		if rec.Tier != domain.TierWeak {
			items = append(items, harness.DemonstratedItem{Text: c.Concept + ": " + c.Rationale, Tier: c.Tier, Uncertainty: c.Uncertainty, EvidenceIDs: []string{rec.ID}})
		}
	}

	if err := e.crystallize(out.Crystallize, learnerSource, best); err != nil {
		return err
	}
	objects, err := e.cfg.Store.ListObjects(e.cfg.PursuitID)
	if err != nil {
		return err
	}

	version := e.cfg.Context.Apply(func(c *harness.LearningContext) {
		if out.Intent.Summary != "" {
			c.Intent = out.Intent.Summary
		}
		c.Concepts = union(c.Concepts, out.Concepts)
		c.Gaps = union(c.Gaps, out.Gaps)
		c.OpenQuestions = union(c.OpenQuestions, out.OpenQuestions)
		c.Readiness = out.Readiness
		for _, inf := range out.Inferences {
			if inf.Misconception && inf.Confidence != "low" {
				c.Misconceptions = union(c.Misconceptions, []string{inf.Claim})
			}
		}
		for _, cr := range out.Crystallize {
			if cr.Type == string(domain.ObjHypothesis) {
				c.Hypotheses = union(c.Hypotheses, []string{cr.Content})
			}
		}
		c.Demonstrated = append(c.Demonstrated, items...)
		c.Objects = objects
	})
	return e.cfg.Bus.Publish(domain.Event{
		SessionID: e.cfg.SessionID, Type: domain.EvContextUpdated, Origin: "evaluator", ContextVersion: version,
		Payload: mustJSON(map[string]any{"shifted": out.Intent.Shifted, "intent": out.Intent.Summary, "observations": out.Observations}),
	})
}

func (e *Evaluator) crystallize(crystals []Crystal, learnerSource *domain.Event, best string) error {
	if len(crystals) == 0 {
		return nil
	}
	existing, err := e.cfg.Store.ListObjects(e.cfg.PursuitID)
	if err != nil {
		return err
	}
	for _, cr := range crystals {
		content := strings.TrimSpace(cr.Content)
		if content == "" {
			continue
		}
		dup := false
		for _, o := range existing {
			if normalize(o.Content) == normalize(content) {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		obj := domain.ConceptObject{PursuitID: e.cfg.PursuitID, Type: domain.ObjectType(cr.Type), Content: content, Authorship: domain.AuthorAI, Confidence: "low"}
		if learnerSource != nil {
			obj.SourceEventID = learnerSource.ID
			if cr.Authorship == string(domain.AuthorLearner) {
				obj.Authorship = domain.AuthorLearner
				obj.Confidence = confidenceFor(best)
			}
		}
		if err := e.cfg.Store.UpsertObject(&obj); err != nil {
			return err
		}
		existing = append(existing, obj)
	}
	return nil
}

func confidenceFor(tier string) string {
	switch domain.EvidenceTier(tier) {
	case domain.TierStrong, domain.TierDurable:
		return "high"
	case domain.TierModerate:
		return "medium"
	}
	return "low"
}

func strongerTier(a, b string) string {
	rank := map[string]int{"weak": 1, "moderate": 2, "strong": 3, "durable": 4}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

func validRecommendations(in []Recommendation) []Recommendation {
	out := make([]Recommendation, 0, len(in))
	for _, r := range in {
		if r.CellType != "" && !domain.KnownCellType(r.CellType) {
			continue
		}
		switch r.Action {
		case domain.ActUpdate, domain.ActDeepen, domain.ActSimplify, domain.ActSpawn, domain.ActReplace, domain.ActRetire:
			out = append(out, r)
		}
	}
	return out
}

func normalize(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

func union(a, b []string) []string {
	seen := map[string]bool{}
	for _, s := range a {
		seen[normalize(s)] = true
	}
	for _, s := range b {
		if s = strings.TrimSpace(s); s != "" && !seen[normalize(s)] {
			seen[normalize(s)] = true
			a = append(a, s)
		}
	}
	return a
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
