package store

import (
	"encoding/json"
	"time"

	"deepflow/internal/domain"
)

const objectCols = `id,pursuit_id,type,content,related_ids,authorship,confidence,status,source_event_id,version,updated_at`

func scanObject(row interface{ Scan(...any) error }) (domain.ConceptObject, error) {
	var o domain.ConceptObject
	var rel string
	var u int64
	if err := row.Scan(&o.ID, &o.PursuitID, &o.Type, &o.Content, &rel, &o.Authorship, &o.Confidence, &o.Status, &o.SourceEventID, &o.Version, &u); err != nil {
		return o, notFound(err)
	}
	o.UpdatedAt = fromMs(u)
	_ = json.Unmarshal([]byte(rel), &o.RelatedIDs)
	if o.RelatedIDs == nil {
		o.RelatedIDs = []string{}
	}
	return o, nil
}

// UpsertObject inserts an object, or replaces the content of an existing one
// with the given ID, keeping version history.
func (s *Store) UpsertObject(o *domain.ConceptObject) error {
	now := time.Now()
	if o.ID == "" {
		o.ID = domain.NewID("obj")
	}
	if o.Status == "" {
		o.Status = "active"
	}
	if o.Confidence == "" {
		o.Confidence = "low"
	}
	if o.RelatedIDs == nil {
		o.RelatedIDs = []string{}
	}
	rel, _ := json.Marshal(o.RelatedIDs)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var cur int
	err = tx.QueryRow(`SELECT version FROM concept_objects WHERE id=?`, o.ID).Scan(&cur)
	if notFound(err) == ErrNotFound {
		o.Version = 1
		_, err = tx.Exec(`INSERT INTO concept_objects(`+objectCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			o.ID, o.PursuitID, o.Type, o.Content, string(rel), o.Authorship, o.Confidence, o.Status, o.SourceEventID, 1, ms(now))
	} else if err == nil {
		o.Version = cur + 1
		_, err = tx.Exec(`UPDATE concept_objects SET content=?, related_ids=?, authorship=?, confidence=?, status=?, version=?, updated_at=? WHERE id=?`,
			o.Content, string(rel), o.Authorship, o.Confidence, o.Status, o.Version, ms(now), o.ID)
	}
	if err != nil {
		return err
	}
	o.UpdatedAt = now
	if _, err := tx.Exec(`INSERT INTO object_versions(object_id,version,content,authorship,changed_at) VALUES(?,?,?,?,?)`,
		o.ID, o.Version, o.Content, o.Authorship, ms(now)); err != nil {
		return err
	}
	return tx.Commit()
}

// GetObject loads an object by ID.
func (s *Store) GetObject(id string) (domain.ConceptObject, error) {
	return scanObject(s.db.QueryRow(`SELECT `+objectCols+` FROM concept_objects WHERE id=?`, id))
}

// UpdateObjectContent applies an edit based on expectedVersion. A stale
// version returns ErrVersionConflict and changes nothing.
func (s *Store) UpdateObjectContent(id, content string, author domain.Authorship, expectedVersion int) (domain.ConceptObject, error) {
	now := time.Now()
	tx, err := s.db.Begin()
	if err != nil {
		return domain.ConceptObject{}, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE concept_objects SET content=?, authorship=?, version=version+1, updated_at=? WHERE id=? AND version=?`,
		content, author, ms(now), id, expectedVersion)
	if err != nil {
		return domain.ConceptObject{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var v int
		if err := tx.QueryRow(`SELECT version FROM concept_objects WHERE id=?`, id).Scan(&v); err != nil {
			return domain.ConceptObject{}, notFound(err)
		}
		return domain.ConceptObject{}, ErrVersionConflict
	}
	if _, err := tx.Exec(`INSERT INTO object_versions(object_id,version,content,authorship,changed_at) VALUES(?,?,?,?,?)`,
		id, expectedVersion+1, content, author, ms(now)); err != nil {
		return domain.ConceptObject{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.ConceptObject{}, err
	}
	return s.GetObject(id)
}

// SetObjectStatus changes an object's status without creating a new version.
func (s *Store) SetObjectStatus(id, status string) error {
	_, err := s.db.Exec(`UPDATE concept_objects SET status=?, updated_at=? WHERE id=?`, status, ms(time.Now()), id)
	return err
}

// ListObjects returns a pursuit's Living Document objects.
func (s *Store) ListObjects(pursuitID string) ([]domain.ConceptObject, error) {
	rows, err := s.db.Query(`SELECT `+objectCols+` FROM concept_objects WHERE pursuit_id=? ORDER BY updated_at DESC, id`, pursuitID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ConceptObject{}
	for rows.Next() {
		o, err := scanObject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// ObjectVersions returns an object's history, oldest first.
func (s *Store) ObjectVersions(id string) ([]domain.ObjectVersion, error) {
	rows, err := s.db.Query(`SELECT object_id,version,content,authorship,changed_at FROM object_versions WHERE object_id=? ORDER BY version`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ObjectVersion{}
	for rows.Next() {
		var v domain.ObjectVersion
		var c int64
		if err := rows.Scan(&v.ObjectID, &v.Version, &v.Content, &v.Authorship, &c); err != nil {
			return nil, err
		}
		v.ChangedAt = fromMs(c)
		out = append(out, v)
	}
	return out, rows.Err()
}

// AddEvidence records an understanding assessment.
func (s *Store) AddEvidence(e *domain.Evidence) error {
	if e.ID == "" {
		e.ID = domain.NewID("evd")
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now()
	}
	_, err := s.db.Exec(`INSERT INTO evidence(id,pursuit_id,session_id,concept,learner_action,tier,correctness,reasoning,rationale,uncertainty,source_event_id,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.ID, e.PursuitID, e.SessionID, e.Concept, e.LearnerAction, e.Tier, e.Correctness, e.Reasoning, e.Rationale, e.Uncertainty, e.SourceEventID, ms(e.CreatedAt))
	return err
}

// ListEvidence returns a pursuit's evidence, newest first.
func (s *Store) ListEvidence(pursuitID string) ([]domain.Evidence, error) {
	rows, err := s.db.Query(`SELECT id,pursuit_id,session_id,concept,learner_action,tier,correctness,reasoning,rationale,uncertainty,source_event_id,created_at FROM evidence WHERE pursuit_id=? ORDER BY created_at DESC, id`, pursuitID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Evidence{}
	for rows.Next() {
		var e domain.Evidence
		var c int64
		if err := rows.Scan(&e.ID, &e.PursuitID, &e.SessionID, &e.Concept, &e.LearnerAction, &e.Tier, &e.Correctness, &e.Reasoning, &e.Rationale, &e.Uncertainty, &e.SourceEventID, &c); err != nil {
			return nil, err
		}
		e.CreatedAt = fromMs(c)
		out = append(out, e)
	}
	return out, rows.Err()
}
