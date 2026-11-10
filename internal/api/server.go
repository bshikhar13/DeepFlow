// Package api exposes DeepFlow over loopback HTTP and server-sent events.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"deepflow/internal/domain"
	"deepflow/internal/orchestrator"
	"deepflow/internal/provider"
	"deepflow/internal/session"
	"deepflow/internal/store"
	"deepflow/web"
)

// Server holds the API's dependencies.
type Server struct {
	m *session.Manager
	s *store.Store
	p provider.Provider
}

// New builds the HTTP handler.
func New(m *session.Manager, s *store.Store, p provider.Provider) http.Handler {
	srv := &Server{m: m, s: s, p: p}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", srv.status)
	mux.HandleFunc("POST /api/consent", srv.consent)
	mux.HandleFunc("GET /api/pursuits", srv.listPursuits)
	mux.HandleFunc("POST /api/pursuits", srv.createPursuit)
	mux.HandleFunc("GET /api/pursuits/{id}", srv.getPursuit)
	mux.HandleFunc("DELETE /api/pursuits/{id}", srv.deletePursuit)
	mux.HandleFunc("GET /api/pursuits/{id}/export", srv.exportPursuit)
	mux.HandleFunc("POST /api/pursuits/{id}/sessions", srv.startSession)
	mux.HandleFunc("GET /api/pursuits/{id}/objects", srv.listObjects)
	mux.HandleFunc("GET /api/pursuits/{id}/evidence", srv.listEvidence)
	mux.HandleFunc("GET /api/sessions/{id}", srv.getSession)
	mux.HandleFunc("GET /api/sessions/{id}/stream", srv.stream)
	mux.HandleFunc("POST /api/sessions/{id}/end", srv.endSession)
	mux.HandleFunc("POST /api/sessions/{id}/undo", srv.undo)
	mux.HandleFunc("POST /api/sessions/{id}/reassess", srv.reassess)
	mux.HandleFunc("GET /api/sessions/{id}/metrics", srv.metrics)
	mux.HandleFunc("POST /api/sessions/{id}/retrieval/{qid}", srv.answerRetrieval)
	mux.HandleFunc("POST /api/cells/{id}/action", srv.cellAction)
	mux.HandleFunc("POST /api/cells/{id}/busy", srv.cellBusy)
	mux.HandleFunc("PUT /api/cells/{id}/draft", srv.cellDraft)
	mux.HandleFunc("POST /api/cells/{id}/pin", srv.cellControl(func(l *session.Live, id string) error { return l.Pin(id, true) }))
	mux.HandleFunc("POST /api/cells/{id}/unpin", srv.cellControl(func(l *session.Live, id string) error { return l.Pin(id, false) }))
	mux.HandleFunc("POST /api/cells/{id}/remove", srv.cellControl(func(l *session.Live, id string) error { return l.Remove(id) }))
	mux.HandleFunc("POST /api/cells/{id}/refresh", srv.cellControl(func(l *session.Live, id string) error { return l.Refresh(id) }))
	mux.HandleFunc("POST /api/cells/{id}/retry", srv.cellAccepted(func(l *session.Live, id string) { l.Retry(id) }))
	mux.HandleFunc("POST /api/cells/{id}/stop", srv.cellAccepted(func(l *session.Live, id string) { l.Stop(id) }))
	mux.HandleFunc("PATCH /api/objects/{id}", srv.patchObject)
	mux.HandleFunc("POST /api/objects/{id}/challenge", srv.challengeObject)
	mux.HandleFunc("GET /api/objects/{id}/evidence", srv.objectEvidence)
	mux.Handle("/", staticHandler())
	return guard(mux)
}

func isLoopback(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// guard keeps learning data local: other hosts (DNS rebinding) and other
// sites' pages (cross-site requests to localhost) are refused.
func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopback(r.Host) {
			writeError(w, http.StatusForbidden, "forbidden", "DeepFlow only accepts local requests.")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && r.Method != http.MethodGet {
			u, err := url.Parse(origin)
			if err != nil || !isLoopback(u.Host) {
				writeError(w, http.StatusForbidden, "forbidden", "Cross-site requests are not allowed.")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func staticHandler() http.Handler {
	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		panic(err)
	}
	files := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeError(w, http.StatusNotFound, "not_found", "No such endpoint.")
			return
		}
		if _, err := fs.Stat(dist, "index.html"); err != nil {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprintln(w, "The frontend isn't built. Run: make web")
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path != "" {
			if _, err := fs.Stat(dist, path); err == nil {
				files.ServeHTTP(w, r)
				return
			}
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/"
		files.ServeHTTP(w, r2)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("api: encode: %v", err)
	}
}

func writeError(w http.ResponseWriter, code int, errCode, message string) {
	writeJSON(w, code, map[string]any{"error": map[string]string{"code": errCode, "message": message}})
}

// fail maps domain errors to HTTP responses.
func fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, orchestrator.ErrNoCell):
		writeError(w, http.StatusNotFound, "not_found", "That doesn't exist.")
	case errors.Is(err, session.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error())
	case errors.Is(err, session.ErrConsentRequired):
		writeError(w, http.StatusConflict, "consent_required", "Acknowledge the data notice first.")
	case errors.Is(err, orchestrator.ErrProtected):
		writeError(w, http.StatusConflict, "protected", "The dialogue cell stays in the workspace.")
	case errors.Is(err, orchestrator.ErrNothingToUndo):
		writeError(w, http.StatusConflict, "nothing_to_undo", "There's nothing to undo.")
	case errors.Is(err, session.ErrClosed):
		writeError(w, http.StatusConflict, "session_closed", "This session has ended.")
	case errors.Is(err, session.ErrNoQuestion):
		writeError(w, http.StatusConflict, "no_question", "That question is already handled.")
	default:
		log.Printf("api: %v", err)
		writeError(w, http.StatusInternalServerError, "internal", "Something went wrong on the server.")
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", "Request body isn't valid JSON, or is too large.")
		return false
	}
	return true
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	vendor, model := "", ""
	if remote, ok := s.p.(provider.Remote); ok {
		vendor, model = remote.Vendor(), remote.ModelName()
	}
	writeJSON(w, 200, map[string]any{"provider": s.p.Name(), "vendor": vendor, "model": model, "consentRequired": s.m.ConsentNeeded()})
}

func (s *Server) consent(w http.ResponseWriter, r *http.Request) {
	if err := s.m.GrantConsent(); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) listPursuits(w http.ResponseWriter, r *http.Request) {
	out, err := s.s.ListPursuits()
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) createPursuit(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Title   string `json:"title"`
		Inquiry string `json:"inquiry"`
		Grade   *int   `json:"grade"`
	}
	if !decode(w, r, &b) {
		return
	}
	grade := store.DefaultGrade
	if b.Grade != nil {
		grade = *b.Grade
	}
	if grade < 6 || grade > 12 {
		writeError(w, 400, "invalid_input", "Pick a class from 6 to 12.")
		return
	}
	b.Title = strings.TrimSpace(b.Title)
	if b.Title == "" || len(b.Title) > 200 {
		writeError(w, 400, "invalid_input", "Give the pursuit a title of up to 200 characters.")
		return
	}
	p, err := s.s.CreatePursuit(b.Title, strings.TrimSpace(b.Inquiry), grade)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 201, p)
}

func (s *Server) getPursuit(w http.ResponseWriter, r *http.Request) {
	p, err := s.s.GetPursuit(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	sessions, err := s.s.ListSessions(p.ID)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"pursuit": p, "sessions": sessions})
}

func (s *Server) deletePursuit(w http.ResponseWriter, r *http.Request) {
	if err := s.m.Delete(r.PathValue("id")); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) exportPursuit(w http.ResponseWriter, r *http.Request) {
	b, err := s.m.Export(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="deepflow-export.json"`)
	writeJSON(w, 200, b)
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Inquiry string `json:"inquiry"`
	}
	if r.ContentLength != 0 && !decode(w, r, &b) {
		return
	}
	l, err := s.m.Start(r.PathValue("id"), strings.TrimSpace(b.Inquiry))
	if err != nil {
		fail(w, err)
		return
	}
	s.writeState(w, l.SessionID())
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, _, err := s.m.Open(id); err != nil {
		fail(w, err)
		return
	}
	s.writeState(w, id)
}

func (s *Server) writeState(w http.ResponseWriter, id string) {
	sess, err := s.s.GetSession(id)
	if err != nil {
		fail(w, err)
		return
	}
	pursuit, err := s.s.GetPursuit(sess.PursuitID)
	if err != nil {
		fail(w, err)
		return
	}
	all, err := s.s.ListMessages(id)
	if err != nil {
		fail(w, err)
		return
	}
	messages := make([]domain.Message, 0, len(all))
	for _, m := range all {
		if m.Content != "" {
			messages = append(messages, m)
		}
	}
	drafts, err := s.s.GetDrafts(id)
	if err != nil {
		fail(w, err)
		return
	}
	artifacts, err := s.s.ListArtifacts(id)
	if err != nil {
		fail(w, err)
		return
	}
	retrieval, err := s.m.RetrievalQuestions(id)
	if err != nil {
		fail(w, err)
		return
	}
	state := map[string]any{"session": sess, "pursuit": pursuit, "messages": messages, "drafts": drafts, "artifacts": artifacts, "retrieval": retrieval, "live": false, "seq": 0, "decisions": []any{}}
	if l, ok := s.m.Get(id); ok {
		state["live"] = true
		state["workspace"] = session.PublicWorkspace(l.Workspace())
		state["seq"] = l.CurrentSeq()
		state["decisions"] = l.Decisions()
	} else {
		var ws domain.WorkspaceState
		if len(sess.Snapshot) > 0 {
			_ = json.Unmarshal(sess.Snapshot, &ws)
		}
		state["workspace"] = session.PublicWorkspace(ws)
	}
	writeJSON(w, 200, state)
}

func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	l, live, err := s.m.Open(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	if !live {
		writeError(w, 409, "session_closed", "This session has ended.")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, 500, "internal", "Streaming isn't supported.")
		return
	}
	var last int64
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		last, _ = strconv.ParseInt(v, 10, 64)
	} else if v := r.URL.Query().Get("after"); v != "" {
		last, _ = strconv.ParseInt(v, 10, 64)
	}
	replay, resync, ch, cancel := l.Subscribe(last)
	defer cancel()
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	send := func(e session.HubEvent) {
		fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.Seq, e.Name, e.Data)
	}
	if resync {
		fmt.Fprintf(w, "event: resync\ndata: {}\n\n")
	}
	for _, e := range replay {
		send(e)
	}
	flusher.Flush()
	keepalive := time.NewTicker(20 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		case e, ok := <-ch:
			if !ok {
				return
			}
			send(e)
			flusher.Flush()
		}
	}
}

func (s *Server) withLive(w http.ResponseWriter, r *http.Request, fn func(*session.Live) error, code int) {
	l, live, err := s.m.Open(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	if !live {
		fail(w, session.ErrClosed)
		return
	}
	if err := fn(l); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(code)
}

func (s *Server) endSession(w http.ResponseWriter, r *http.Request) {
	sess, err := s.m.End(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, sess)
}

func (s *Server) undo(w http.ResponseWriter, r *http.Request) {
	s.withLive(w, r, func(l *session.Live) error { return l.Undo() }, 204)
}

func (s *Server) reassess(w http.ResponseWriter, r *http.Request) {
	s.withLive(w, r, func(l *session.Live) error { return l.Reassess() }, 202)
}

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	l, ok := s.m.Get(r.PathValue("id"))
	if !ok {
		fail(w, session.ErrClosed)
		return
	}
	writeJSON(w, 200, l.Metrics())
}

func (s *Server) answerRetrieval(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Answer string `json:"answer"`
		Skip   bool   `json:"skip"`
	}
	if !decode(w, r, &b) {
		return
	}
	if err := s.m.AnswerRetrieval(r.PathValue("id"), r.PathValue("qid"), b.Answer, b.Skip); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(202)
}

func (s *Server) liveForCell(w http.ResponseWriter, r *http.Request) (*session.Live, string, bool) {
	id := r.PathValue("id")
	l, ok := s.m.ForCell(id)
	if !ok {
		fail(w, orchestrator.ErrNoCell)
		return nil, "", false
	}
	return l, id, true
}

func (s *Server) cellAction(w http.ResponseWriter, r *http.Request) {
	l, id, ok := s.liveForCell(w, r)
	if !ok {
		return
	}
	var b struct {
		Type     domain.EventType `json:"type"`
		Payload  json.RawMessage  `json:"payload"`
		ClientID string           `json:"client_id"`
	}
	if !decode(w, r, &b) {
		return
	}
	if err := l.Action(id, b.Type, b.Payload, b.ClientID); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(202)
}

func (s *Server) cellBusy(w http.ResponseWriter, r *http.Request) {
	l, id, ok := s.liveForCell(w, r)
	if !ok {
		return
	}
	var b struct {
		Busy bool `json:"busy"`
	}
	if !decode(w, r, &b) {
		return
	}
	if err := l.Busy(id, b.Busy); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) cellDraft(w http.ResponseWriter, r *http.Request) {
	l, id, ok := s.liveForCell(w, r)
	if !ok {
		return
	}
	var b struct {
		Content string `json:"content"`
	}
	if !decode(w, r, &b) {
		return
	}
	if err := s.s.SaveDraft(l.SessionID(), id, b.Content); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) cellControl(fn func(*session.Live, string) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l, id, ok := s.liveForCell(w, r)
		if !ok {
			return
		}
		if err := fn(l, id); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(204)
	}
}

func (s *Server) cellAccepted(fn func(*session.Live, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l, id, ok := s.liveForCell(w, r)
		if !ok {
			return
		}
		fn(l, id)
		w.WriteHeader(202)
	}
}

func (s *Server) listObjects(w http.ResponseWriter, r *http.Request) {
	out, err := s.s.ListObjects(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) listEvidence(w http.ResponseWriter, r *http.Request) {
	out, err := s.s.ListEvidence(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) patchObject(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Content string `json:"content"`
		Version int    `json:"version"`
	}
	if !decode(w, r, &b) {
		return
	}
	if strings.TrimSpace(b.Content) == "" || len([]rune(b.Content)) > session.MaxInputRunes {
		writeError(w, 400, "invalid_input", "Content must be non-empty and within the length limit.")
		return
	}
	id := r.PathValue("id")
	o, err := s.s.UpdateObjectContent(id, b.Content, domain.AuthorEdited, b.Version)
	if errors.Is(err, store.ErrVersionConflict) {
		cur, gerr := s.s.GetObject(id)
		if gerr != nil {
			fail(w, gerr)
			return
		}
		writeJSON(w, 409, map[string]any{"error": map[string]string{"code": "version_conflict", "message": "This object changed elsewhere."}, "current": cur})
		return
	}
	if err != nil {
		fail(w, err)
		return
	}
	if l, ok := s.m.ForPursuit(o.PursuitID); ok {
		p, _ := json.Marshal(map[string]string{"objectId": o.ID, "text": b.Content})
		l.ObjectsChanged(domain.EvObjectEdited, p)
	}
	writeJSON(w, 200, o)
}

func (s *Server) challengeObject(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Text string `json:"text"`
	}
	if !decode(w, r, &b) {
		return
	}
	if strings.TrimSpace(b.Text) == "" {
		writeError(w, 400, "invalid_input", "Say what you're challenging.")
		return
	}
	id := r.PathValue("id")
	o, err := s.s.GetObject(id)
	if err != nil {
		fail(w, err)
		return
	}
	if err := s.s.SetObjectStatus(id, "challenged"); err != nil {
		fail(w, err)
		return
	}
	if l, ok := s.m.ForPursuit(o.PursuitID); ok {
		p, _ := json.Marshal(map[string]string{"objectId": id, "text": b.Text, "object": o.Content})
		l.ObjectsChanged(domain.EvObjectChallenged, p)
	}
	w.WriteHeader(202)
}

func (s *Server) objectEvidence(w http.ResponseWriter, r *http.Request) {
	o, err := s.s.GetObject(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	all, err := s.s.ListEvidence(o.PursuitID)
	if err != nil {
		fail(w, err)
		return
	}
	out := []domain.Evidence{}
	for _, e := range all {
		if o.SourceEventID != "" && e.SourceEventID == o.SourceEventID {
			out = append(out, e)
		}
	}
	writeJSON(w, 200, out)
}
