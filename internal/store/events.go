package store

import (
	"database/sql"
	"encoding/json"
	"time"

	"deepflow/internal/domain"
)

// AppendEvent persists an event. A repeated dedupe key returns inserted=false.
func (s *Store) AppendEvent(e domain.Event) (inserted bool, err error) {
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now()
	}
	res, err := s.db.Exec(`INSERT INTO events(id,session_id,type,origin,payload,context_version,dedupe_key,created_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(dedupe_key) DO NOTHING`,
		e.ID, e.SessionID, e.Type, e.Origin, nullable(e.Payload), e.ContextVersion, e.DedupeKey, ms(e.CreatedAt))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func scanEvent(row interface{ Scan(...any) error }) (domain.Event, error) {
	var e domain.Event
	var p sql.NullString
	var c int64
	if err := row.Scan(&e.ID, &e.SessionID, &e.Type, &e.Origin, &p, &e.ContextVersion, &e.DedupeKey, &c); err != nil {
		return e, notFound(err)
	}
	if p.Valid {
		e.Payload = json.RawMessage(p.String)
	}
	e.CreatedAt = fromMs(c)
	return e, nil
}

const eventCols = `id,session_id,type,origin,payload,context_version,dedupe_key,created_at`

// GetEvent loads an event by ID.
func (s *Store) GetEvent(id string) (domain.Event, error) {
	return scanEvent(s.db.QueryRow(`SELECT `+eventCols+` FROM events WHERE id=?`, id))
}

// ListEvents returns events for a session, oldest first, optionally by type.
func (s *Store) ListEvents(sessionID string, types ...domain.EventType) ([]domain.Event, error) {
	rows, err := s.db.Query(`SELECT `+eventCols+` FROM events WHERE session_id=? ORDER BY created_at, rowid`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	want := map[domain.EventType]bool{}
	for _, t := range types {
		want[t] = true
	}
	out := []domain.Event{}
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		if len(want) == 0 || want[e.Type] {
			out = append(out, e)
		}
	}
	return out, rows.Err()
}

// AddArtifact preserves a retired cell's output.
func (s *Store) AddArtifact(a *domain.Artifact) error {
	if a.ID == "" {
		a.ID = domain.NewID("art")
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now()
	}
	_, err := s.db.Exec(`INSERT INTO artifacts(id,session_id,cell_id,cell_type,content,learner_authored,created_at) VALUES(?,?,?,?,?,?,?)`,
		a.ID, a.SessionID, a.CellID, a.CellType, nullable(a.Content), b2i(a.LearnerAuthored), ms(a.CreatedAt))
	return err
}

// ListArtifacts returns a session's artifacts, oldest first.
func (s *Store) ListArtifacts(sessionID string) ([]domain.Artifact, error) {
	rows, err := s.db.Query(`SELECT id,session_id,cell_id,cell_type,content,learner_authored,created_at FROM artifacts WHERE session_id=? ORDER BY created_at, id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Artifact{}
	for rows.Next() {
		var a domain.Artifact
		var content sql.NullString
		var l int
		var c int64
		if err := rows.Scan(&a.ID, &a.SessionID, &a.CellID, &a.CellType, &content, &l, &c); err != nil {
			return nil, err
		}
		if content.Valid {
			a.Content = json.RawMessage(content.String)
		}
		a.LearnerAuthored, a.CreatedAt = l != 0, fromMs(c)
		out = append(out, a)
	}
	return out, rows.Err()
}

// SaveDraft stores learner text for a cell. Empty text clears the draft.
func (s *Store) SaveDraft(sessionID, cellID, content string) error {
	if content == "" {
		_, err := s.db.Exec(`DELETE FROM drafts WHERE session_id=? AND cell_id=?`, sessionID, cellID)
		return err
	}
	_, err := s.db.Exec(`INSERT INTO drafts(session_id,cell_id,content,updated_at) VALUES(?,?,?,?) ON CONFLICT(session_id,cell_id) DO UPDATE SET content=excluded.content, updated_at=excluded.updated_at`,
		sessionID, cellID, content, ms(time.Now()))
	return err
}

// GetDrafts returns a session's drafts keyed by cell ID.
func (s *Store) GetDrafts(sessionID string) (map[string]string, error) {
	rows, err := s.db.Query(`SELECT cell_id,content FROM drafts WHERE session_id=?`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}
