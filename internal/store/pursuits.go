package store

import (
	"database/sql"
	"encoding/json"
	"time"

	"deepflow/internal/domain"
)

// DefaultGrade is used when a pursuit predates class levels.
const DefaultGrade = 8

const pursuitCols = `id,title,description,grade,journey,created_at,updated_at`

func scanPursuit(row interface{ Scan(...any) error }) (domain.Pursuit, error) {
	var p domain.Pursuit
	var c, u int64
	var j sql.NullString
	if err := row.Scan(&p.ID, &p.Title, &p.Description, &p.Grade, &j, &c, &u); err != nil {
		return p, notFound(err)
	}
	p.CreatedAt, p.UpdatedAt = fromMs(c), fromMs(u)
	if j.Valid && j.String != "" {
		var journey domain.Journey
		if err := json.Unmarshal([]byte(j.String), &journey); err == nil {
			p.Journey = &journey
		}
	}
	return p, nil
}

// CreatePursuit inserts a new pursuit for a student in the given class.
func (s *Store) CreatePursuit(title, description string, grade int) (domain.Pursuit, error) {
	now := time.Now()
	p := domain.Pursuit{ID: domain.NewID("pur"), Title: title, Description: description, Grade: grade, CreatedAt: now, UpdatedAt: now}
	_, err := s.db.Exec(`INSERT INTO pursuits(id,title,description,grade,created_at,updated_at) VALUES(?,?,?,?,?,?)`,
		p.ID, p.Title, p.Description, p.Grade, ms(now), ms(now))
	return p, err
}

// GetPursuit loads a pursuit by ID.
func (s *Store) GetPursuit(id string) (domain.Pursuit, error) {
	return scanPursuit(s.db.QueryRow(`SELECT `+pursuitCols+` FROM pursuits WHERE id=?`, id))
}

// ListPursuits returns all pursuits, most recently updated first.
func (s *Store) ListPursuits() ([]domain.Pursuit, error) {
	rows, err := s.db.Query(`SELECT ` + pursuitCols + ` FROM pursuits ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Pursuit{}
	for rows.Next() {
		p, err := scanPursuit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SaveJourney stores a pursuit's index and progress.
func (s *Store) SaveJourney(pursuitID string, j domain.Journey) error {
	b, err := json.Marshal(j)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE pursuits SET journey=? WHERE id=?`, string(b), pursuitID)
	return err
}

// TouchPursuit marks a pursuit as recently used.
func (s *Store) TouchPursuit(id string) error {
	_, err := s.db.Exec(`UPDATE pursuits SET updated_at=? WHERE id=?`, ms(time.Now()), id)
	return err
}

// DeletePursuit removes a pursuit and everything that belongs to it.
func (s *Store) DeletePursuit(id string) error {
	res, err := s.db.Exec(`DELETE FROM pursuits WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

const sessionCols = `id,pursuit_id,inquiry,started_at,ended_at,summary,workspace_snapshot,retrieval_questions,needs_consolidation`

func scanSession(row interface{ Scan(...any) error }) (domain.Session, error) {
	var x domain.Session
	var st, en int64
	var snap, rq sql.NullString
	var nc int
	if err := row.Scan(&x.ID, &x.PursuitID, &x.Inquiry, &st, &en, &x.Summary, &snap, &rq, &nc); err != nil {
		return x, notFound(err)
	}
	x.StartedAt, x.EndedAt = fromMs(st), fromMs(en)
	if snap.Valid {
		x.Snapshot = json.RawMessage(snap.String)
	}
	if rq.Valid {
		x.RetrievalQuestions = json.RawMessage(rq.String)
	}
	x.NeedsConsolidation = nc != 0
	return x, nil
}

// CreateSession starts a new session in a pursuit.
func (s *Store) CreateSession(pursuitID, inquiry string) (domain.Session, error) {
	x := domain.Session{ID: domain.NewID("ses"), PursuitID: pursuitID, Inquiry: inquiry, StartedAt: time.Now()}
	_, err := s.db.Exec(`INSERT INTO sessions(id,pursuit_id,inquiry,started_at) VALUES(?,?,?,?)`, x.ID, pursuitID, inquiry, ms(x.StartedAt))
	return x, err
}

// GetSession loads a session by ID.
func (s *Store) GetSession(id string) (domain.Session, error) {
	return scanSession(s.db.QueryRow(`SELECT `+sessionCols+` FROM sessions WHERE id=?`, id))
}

// LatestSession returns the newest session of a pursuit.
func (s *Store) LatestSession(pursuitID string) (domain.Session, error) {
	return scanSession(s.db.QueryRow(`SELECT `+sessionCols+` FROM sessions WHERE pursuit_id=? ORDER BY started_at DESC, rowid DESC LIMIT 1`, pursuitID))
}

// EndSession records a session's end and summary.
func (s *Store) EndSession(id, summary string, at time.Time, needsConsolidation bool) error {
	_, err := s.db.Exec(`UPDATE sessions SET ended_at=?, summary=?, needs_consolidation=? WHERE id=?`, ms(at), summary, b2i(needsConsolidation), id)
	return err
}

// ClearConsolidation records a late consolidation's result.
func (s *Store) ClearConsolidation(id, summary string) error {
	_, err := s.db.Exec(`UPDATE sessions SET summary=?, needs_consolidation=0 WHERE id=?`, summary, id)
	return err
}

// SetInquiry updates a session's central inquiry.
func (s *Store) SetInquiry(id, inquiry string) error {
	_, err := s.db.Exec(`UPDATE sessions SET inquiry=? WHERE id=?`, inquiry, id)
	return err
}

// SaveSnapshot stores the workspace snapshot for a session.
func (s *Store) SaveSnapshot(sessionID string, ws domain.WorkspaceState) error {
	b, err := json.Marshal(ws)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE sessions SET workspace_snapshot=? WHERE id=?`, string(b), sessionID)
	return err
}

// SaveRetrievalQuestions stores a session's retrieval questions.
func (s *Store) SaveRetrievalQuestions(sessionID string, q json.RawMessage) error {
	_, err := s.db.Exec(`UPDATE sessions SET retrieval_questions=? WHERE id=?`, nullable(q), sessionID)
	return err
}

// ListSessions returns a pursuit's sessions, oldest first.
func (s *Store) ListSessions(pursuitID string) ([]domain.Session, error) {
	rows, err := s.db.Query(`SELECT `+sessionCols+` FROM sessions WHERE pursuit_id=? ORDER BY started_at, rowid`, pursuitID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Session{}
	for rows.Next() {
		x, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// AddMessage inserts a dialogue message, assigning its ID and time if unset.
func (s *Store) AddMessage(m *domain.Message) error {
	if m.ID == "" {
		m.ID = domain.NewID("msg")
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now()
	}
	_, err := s.db.Exec(`INSERT INTO messages(id,session_id,role,content,interrupted,created_at) VALUES(?,?,?,?,?,?)`,
		m.ID, m.SessionID, m.Role, m.Content, b2i(m.Interrupted), ms(m.CreatedAt))
	return err
}

// UpdateMessage replaces a message's content and interrupted flag.
func (s *Store) UpdateMessage(id, content string, interrupted bool) error {
	_, err := s.db.Exec(`UPDATE messages SET content=?, interrupted=? WHERE id=?`, content, b2i(interrupted), id)
	return err
}

// ListMessages returns a session's dialogue in order.
func (s *Store) ListMessages(sessionID string) ([]domain.Message, error) {
	rows, err := s.db.Query(`SELECT id,session_id,role,content,interrupted,created_at FROM messages WHERE session_id=? ORDER BY seq`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Message{}
	for rows.Next() {
		var m domain.Message
		var in int
		var c int64
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Content, &in, &c); err != nil {
			return nil, err
		}
		m.Interrupted, m.CreatedAt = in != 0, fromMs(c)
		out = append(out, m)
	}
	return out, rows.Err()
}
