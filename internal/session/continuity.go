package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
	"unicode/utf8"

	"deepflow/internal/domain"
	"deepflow/internal/provider"
	"deepflow/internal/store"
)

const maxRetrievalQuestions = 3

// RetrievalQuestion checks whether earlier learning lasted.
type RetrievalQuestion struct {
	ID      string `json:"id"`
	Prompt  string `json:"prompt"`
	Concept string `json:"concept"`
	Status  string `json:"status"` // open, answered, skipped
}

func stringList() map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
}

var consolidateSchema = map[string]any{
	"type": "object", "additionalProperties": false,
	"required": []string{"summary", "insights", "gaps", "open_questions"},
	"properties": map[string]any{
		"summary": map[string]any{"type": "string"}, "insights": stringList(), "gaps": stringList(), "open_questions": stringList(),
	},
}

var retrievalSchema = map[string]any{
	"type": "object", "additionalProperties": false, "required": []string{"questions"},
	"properties": map[string]any{"questions": map[string]any{"type": "array", "items": map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"prompt", "concept"},
		"properties": map[string]any{"prompt": map[string]any{"type": "string"}, "concept": map[string]any{"type": "string"}},
	}}},
}

const consolidateSystem = `You consolidate a school student's learning session. Summarize what they explored in one short sentence, under 20 words, written to "you". List up to 3 insights supported by what they wrote, gaps that remain and unresolved questions, each under 15 words. Don't credit the learner with anything they only read. Don't repeat items already in the Living Document.`

const retrievalSystem = `Write up to three short retrieval questions, under 15 words each, for a school student returning after a break, based on what they worked on. Each must make them recall or apply an idea from memory, not recognize it. Prefer ideas with evidence, plus one boundary case if possible. Do not include the answers.`

type consolidation struct {
	Summary       string   `json:"summary"`
	Insights      []string `json:"insights"`
	Gaps          []string `json:"gaps"`
	OpenQuestions []string `json:"open_questions"`
}

func consolidate(ctx context.Context, cfg Config, p provider.Provider, pursuit domain.Pursuit, s domain.Session) (string, error) {
	msgs, err := cfg.Store.ListMessages(s.ID)
	if err != nil {
		return "", err
	}
	objects, err := cfg.Store.ListObjects(pursuit.ID)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Topic: %s\nCentral inquiry: %s\n\nDialogue:\n", pursuit.Title, s.Inquiry)
	for _, m := range msgs {
		fmt.Fprintf(&b, "%s: %s\n", m.Role, m.Content)
	}
	b.WriteString("\nLiving Document already holds:\n")
	for _, o := range objects {
		fmt.Fprintf(&b, "- [%s] %s\n", o.Type, o.Content)
	}
	var out consolidation
	if _, err := p.Structured(ctx, provider.StructuredRequest{
		Role: provider.RoleConsolidate, Name: "session.consolidate", System: consolidateSystem, Prompt: b.String(), Schema: consolidateSchema,
	}, &out); err != nil {
		return "", err
	}
	source := ""
	events, err := cfg.Store.ListEvents(s.ID)
	if err != nil {
		return "", err
	}
	for _, e := range events {
		if e.Type.LearnerAuthored() {
			source = e.ID
		}
	}
	exists := map[string]bool{}
	for _, o := range objects {
		exists[strings.ToLower(strings.TrimSpace(o.Content))] = true
	}
	add := func(t domain.ObjectType, items []string) error {
		for _, item := range items {
			key := strings.ToLower(strings.TrimSpace(item))
			if key == "" || exists[key] {
				continue
			}
			exists[key] = true
			// These are AI formulations, so they never claim learner authorship.
			o := domain.ConceptObject{PursuitID: pursuit.ID, Type: t, Content: strings.TrimSpace(item), Authorship: domain.AuthorAI, Confidence: "low", SourceEventID: source}
			if err := cfg.Store.UpsertObject(&o); err != nil {
				return err
			}
		}
		return nil
	}
	if err := add(domain.ObjInsight, out.Insights); err != nil {
		return "", err
	}
	if err := add(domain.ObjQuestion, append(out.OpenQuestions, out.Gaps...)); err != nil {
		return "", err
	}
	return out.Summary, nil
}

// End consolidates and closes a session. A model failure doesn't stop it from
// ending; the session is flagged for consolidation when the pursuit next opens.
func (m *Manager) End(sessionID string) (domain.Session, error) {
	m.mu.Lock()
	l := m.live[sessionID]
	m.mu.Unlock()
	if l == nil {
		s, err := m.cfg.Store.GetSession(sessionID)
		if err != nil {
			return s, err
		}
		if s.Ended() {
			return s, nil
		}
		if err := m.cfg.Store.EndSession(sessionID, "", time.Now(), true); err != nil {
			return s, err
		}
		return m.cfg.Store.GetSession(sessionID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	s, err := m.cfg.Store.GetSession(sessionID)
	if err != nil {
		return s, err
	}
	summary, cerr := consolidate(ctx, m.cfg, l.gov, l.pursuit, s)
	if cerr != nil {
		log.Printf("session: consolidate %s: %v", sessionID, cerr)
	}
	for _, c := range l.Workspace().Cells {
		l.rt.SetSuspended(c.ID, true)
	}
	m.mu.Lock()
	delete(m.live, sessionID)
	m.mu.Unlock()
	if err := m.cfg.Store.SaveSnapshot(sessionID, l.Workspace()); err != nil {
		log.Printf("session: snapshot: %v", err)
	}
	l.Close()
	if err := m.cfg.Store.EndSession(sessionID, summary, time.Now(), cerr != nil); err != nil {
		return s, err
	}
	return m.cfg.Store.GetSession(sessionID)
}

// prepareReturn runs in the background when a new session follows an earlier
// one: it finishes any owed consolidation and writes retrieval questions.
func (l *Live) prepareReturn(prior domain.Session) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	go func() {
		select {
		case <-l.done:
			cancel()
		case <-ctx.Done():
		}
	}()
	if prior.NeedsConsolidation {
		summary, err := consolidate(ctx, l.cfg, l.gov, l.pursuit, prior)
		if err != nil {
			log.Printf("session: late consolidation %s: %v", prior.ID, err)
		} else if ctx.Err() == nil {
			if err := l.cfg.Store.ClearConsolidation(prior.ID, summary); err != nil {
				log.Printf("session: late consolidation %s: %v", prior.ID, err)
			}
			prior.Summary = summary
		}
	}
	objects, err := l.cfg.Store.ListObjects(l.pursuit.ID)
	if err != nil {
		return
	}
	evidence, err := l.cfg.Store.ListEvidence(l.pursuit.ID)
	if err != nil {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Topic: %s\nCentral inquiry last time: %s\nLast session summary: %s\n\nLiving Document:\n", l.pursuit.Title, prior.Inquiry, prior.Summary)
	for _, o := range objects {
		fmt.Fprintf(&b, "- [%s] %s\n", o.Type, o.Content)
	}
	b.WriteString("\nEvidence:\n")
	for _, e := range evidence {
		fmt.Fprintf(&b, "- %s (%s): %s\n", e.Concept, e.Tier, e.Rationale)
	}
	var out struct {
		Questions []struct {
			Prompt  string `json:"prompt"`
			Concept string `json:"concept"`
		} `json:"questions"`
	}
	if _, err := l.gov.Structured(ctx, provider.StructuredRequest{
		Role: provider.RoleCell, Name: "session.retrieval", System: retrievalSystem, Prompt: b.String(), Schema: retrievalSchema,
	}, &out); err != nil {
		log.Printf("session: retrieval questions: %v", err)
		return
	}
	qs := make([]RetrievalQuestion, 0, maxRetrievalQuestions)
	for _, q := range out.Questions {
		if len(qs) == maxRetrievalQuestions {
			break
		}
		if strings.TrimSpace(q.Prompt) != "" {
			qs = append(qs, RetrievalQuestion{ID: domain.NewID("rq"), Prompt: q.Prompt, Concept: q.Concept, Status: "open"})
		}
	}
	if ctx.Err() != nil {
		return
	}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.retrieval = append([]RetrievalQuestion{}, qs...) // qs is shared with subscribers below
	l.mu.Unlock()
	l.saveRetrieval(qs)
	l.hub.publish("retrieval.ready", qs)
}

func (l *Live) saveRetrieval(qs []RetrievalQuestion) {
	b, err := json.Marshal(qs)
	if err == nil {
		err = l.cfg.Store.SaveRetrievalQuestions(l.session.ID, b)
	}
	if err != nil {
		log.Printf("session: save retrieval: %v", err)
	}
}

func (l *Live) loadRetrieval() {
	if len(l.session.RetrievalQuestions) > 0 {
		_ = json.Unmarshal(l.session.RetrievalQuestions, &l.retrieval)
	}
}

// loadDecisions restores the activity log after a restart. The undo stack is
// runtime state, so restored entries can't be undone.
func (l *Live) loadDecisions() {
	events, err := l.cfg.Store.ListEvents(l.session.ID, domain.EvOrchestratorDecide)
	if err != nil {
		log.Printf("session: load decisions: %v", err)
		return
	}
	for _, e := range events {
		var d domain.Decision
		if json.Unmarshal(e.Payload, &d) == nil {
			l.decisions = append(l.decisions, Decided{Decision: d, At: e.CreatedAt})
		}
	}
}

// RetrievalQuestions returns a session's retrieval questions.
func (m *Manager) RetrievalQuestions(sessionID string) ([]RetrievalQuestion, error) {
	if l, ok := m.Get(sessionID); ok {
		l.mu.Lock()
		defer l.mu.Unlock()
		return append([]RetrievalQuestion{}, l.retrieval...), nil
	}
	s, err := m.cfg.Store.GetSession(sessionID)
	if err != nil {
		return nil, err
	}
	qs := []RetrievalQuestion{}
	if len(s.RetrievalQuestions) > 0 {
		_ = json.Unmarshal(s.RetrievalQuestions, &qs)
	}
	return qs, nil
}

// ErrNoQuestion is returned for an unknown or already-handled question.
var ErrNoQuestion = errors.New("no such open retrieval question")

// AnswerRetrieval records an answer or skip. Only answers become evaluation
// events, and they carry the session the idea came from so the Evaluator can
// recognize learning that lasted across a delay.
func (m *Manager) AnswerRetrieval(sessionID, questionID, answer string, skip bool) error {
	l, ok := m.Get(sessionID)
	if !ok {
		return ErrClosed
	}
	if !skip {
		if strings.TrimSpace(answer) == "" {
			return fmt.Errorf("%w: answer is empty", ErrInvalidInput)
		}
		if utf8.RuneCountInString(answer) > MaxInputRunes {
			return fmt.Errorf("%w: answer is longer than %d characters", ErrInvalidInput, MaxInputRunes)
		}
	}
	l.mu.Lock()
	var q *RetrievalQuestion
	for i := range l.retrieval {
		if l.retrieval[i].ID == questionID && l.retrieval[i].Status == "open" {
			q = &l.retrieval[i]
		}
	}
	if q == nil {
		l.mu.Unlock()
		return ErrNoQuestion
	}
	if skip {
		q.Status = "skipped"
	} else {
		q.Status = "answered"
	}
	concept, prompt := q.Concept, q.Prompt
	qs := append([]RetrievalQuestion{}, l.retrieval...)
	l.mu.Unlock()
	l.saveRetrieval(qs)
	if skip {
		return nil
	}
	prior, err := m.priorSession(l)
	if err != nil {
		return err
	}
	b, _ := json.Marshal(map[string]string{"text": answer, "question": prompt, "concept": concept, "fromSession": prior})
	return l.bus.Publish(domain.Event{SessionID: sessionID, Type: domain.EvRetrievalAnswered, Origin: "retrieval", Payload: b})
}

func (m *Manager) priorSession(l *Live) (string, error) {
	sessions, err := m.cfg.Store.ListSessions(l.pursuit.ID)
	if err != nil {
		return "", err
	}
	prior := ""
	for _, s := range sessions {
		if s.ID != l.session.ID {
			prior = s.ID
		}
	}
	return prior, nil
}

// ExportSession is one session in an export.
type ExportSession struct {
	domain.Session
	Messages  []domain.Message  `json:"messages"`
	Events    []domain.Event    `json:"events"`
	Artifacts []domain.Artifact `json:"artifacts"`
	Drafts    map[string]string `json:"drafts"`
}

// ExportBundle is all of a pursuit's data.
type ExportBundle struct {
	ExportedAt time.Time              `json:"exportedAt"`
	Pursuit    domain.Pursuit         `json:"pursuit"`
	Sessions   []ExportSession        `json:"sessions"`
	Objects    []domain.ConceptObject `json:"objects"`
	Evidence   []domain.Evidence      `json:"evidence"`
}

// Export gathers everything stored for a pursuit.
func (m *Manager) Export(pursuitID string) (ExportBundle, error) {
	st := m.cfg.Store
	b := ExportBundle{ExportedAt: time.Now()}
	var err error
	if b.Pursuit, err = st.GetPursuit(pursuitID); err != nil {
		return b, err
	}
	sessions, err := st.ListSessions(pursuitID)
	if err != nil {
		return b, err
	}
	for _, s := range sessions {
		es := ExportSession{Session: s}
		if es.Messages, err = st.ListMessages(s.ID); err != nil {
			return b, err
		}
		if es.Events, err = st.ListEvents(s.ID); err != nil {
			return b, err
		}
		if es.Artifacts, err = st.ListArtifacts(s.ID); err != nil {
			return b, err
		}
		if es.Drafts, err = st.GetDrafts(s.ID); err != nil {
			return b, err
		}
		b.Sessions = append(b.Sessions, es)
	}
	if b.Objects, err = st.ListObjects(pursuitID); err != nil {
		return b, err
	}
	if b.Evidence, err = st.ListEvidence(pursuitID); err != nil {
		return b, err
	}
	return b, nil
}

// Delete stops any live session for the pursuit, then erases all its data.
func (m *Manager) Delete(pursuitID string) error {
	m.mu.Lock()
	l := m.liveForPursuit(pursuitID)
	if l != nil {
		delete(m.live, l.session.ID)
	}
	m.mu.Unlock()
	if l != nil {
		l.Close()
	}
	err := m.cfg.Store.DeletePursuit(pursuitID)
	if errors.Is(err, store.ErrNotFound) && l != nil {
		return nil
	}
	return err
}
