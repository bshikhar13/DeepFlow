package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"deepflow/internal/domain"
)

func openTest(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func seed(t *testing.T, s *Store) (domain.Pursuit, domain.Session) {
	t.Helper()
	p, err := s.CreatePursuit("Database Systems", "desc", 7)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.CreateSession(p.ID, "Why are indexes slow?")
	if err != nil {
		t.Fatal(err)
	}
	return p, sess
}

func TestPursuitRoundTrip(t *testing.T) {
	s, _ := openTest(t)
	p, _ := seed(t, s)
	got, err := s.GetPursuit(p.ID)
	if err != nil || got.Title != "Database Systems" || got.Description != "desc" || got.Grade != 7 {
		t.Fatalf("got %+v err %v", got, err)
	}
	all, err := s.ListPursuits()
	if err != nil || len(all) != 1 {
		t.Fatalf("list %v %v", all, err)
	}
	if _, err := s.GetPursuit("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestDeletePursuitCascades(t *testing.T) {
	s, _ := openTest(t)
	p, sess := seed(t, s)
	mustNoErr(t, s.AddMessage(&domain.Message{SessionID: sess.ID, Role: "user", Content: "x"}))
	mustNoErr(t, s.UpsertObject(&domain.ConceptObject{PursuitID: p.ID, Type: domain.ObjInsight, Content: "c", Authorship: domain.AuthorAI}))
	mustNoErr(t, s.AddEvidence(&domain.Evidence{PursuitID: p.ID, SessionID: sess.ID, Concept: "c", Tier: domain.TierWeak}))
	_, err := s.AppendEvent(domain.Event{ID: "e1", SessionID: sess.ID, Type: domain.EvUserMessage, DedupeKey: "k1"})
	mustNoErr(t, err)
	mustNoErr(t, s.AddArtifact(&domain.Artifact{SessionID: sess.ID, CellID: "c1", CellType: domain.CellConcept}))
	mustNoErr(t, s.SaveDraft(sess.ID, "c1", "draft"))
	mustNoErr(t, s.DeletePursuit(p.ID))
	for _, table := range []string{"pursuits", "sessions", "messages", "concept_objects", "evidence", "events", "artifacts", "drafts"} {
		var n int
		mustNoErr(t, s.db.QueryRow("SELECT count(*) FROM "+table).Scan(&n))
		if n != 0 {
			t.Errorf("%s has %d rows", table, n)
		}
	}
}

func TestUpdateObjectBumpsVersionAndKeepsHistory(t *testing.T) {
	s, _ := openTest(t)
	p, _ := seed(t, s)
	o := domain.ConceptObject{PursuitID: p.ID, Type: domain.ObjInsight, Content: "v1", Authorship: domain.AuthorAI}
	mustNoErr(t, s.UpsertObject(&o))
	got, err := s.UpdateObjectContent(o.ID, "v2", domain.AuthorEdited, 1)
	mustNoErr(t, err)
	if got.Version != 2 || got.Content != "v2" || got.Authorship != domain.AuthorEdited {
		t.Fatalf("%+v", got)
	}
	h, err := s.ObjectVersions(o.ID)
	mustNoErr(t, err)
	if len(h) != 2 || h[0].Content != "v1" || h[1].Content != "v2" {
		t.Fatalf("%+v", h)
	}
}

func TestUpdateObjectConflict(t *testing.T) {
	s, _ := openTest(t)
	p, _ := seed(t, s)
	o := domain.ConceptObject{PursuitID: p.ID, Type: domain.ObjInsight, Content: "v1", Authorship: domain.AuthorAI}
	mustNoErr(t, s.UpsertObject(&o))
	_, err := s.UpdateObjectContent(o.ID, "v2", domain.AuthorEdited, 1)
	mustNoErr(t, err)
	_, err = s.UpdateObjectContent(o.ID, "stale", domain.AuthorEdited, 1)
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("got %v", err)
	}
	cur, _ := s.GetObject(o.ID)
	if cur.Content != "v2" {
		t.Fatalf("overwritten: %q", cur.Content)
	}
}

func TestAppendEventDedupes(t *testing.T) {
	s, _ := openTest(t)
	_, sess := seed(t, s)
	e := domain.Event{ID: "e1", SessionID: sess.ID, Type: domain.EvUserMessage, DedupeKey: "k", Payload: json.RawMessage(`{"a":1}`)}
	ok, err := s.AppendEvent(e)
	if err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	e.ID = "e2"
	ok, err = s.AppendEvent(e)
	if err != nil || ok {
		t.Fatalf("second insert: %v %v", ok, err)
	}
	got, err := s.GetEvent("e1")
	if err != nil || string(got.Payload) != `{"a":1}` {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	s, _ := openTest(t)
	_, sess := seed(t, s)
	ws := domain.WorkspaceState{SessionID: sess.ID, Revision: 3, Cells: []domain.Cell{{ID: "c1", Type: domain.CellDialogue, Pinned: true, Layout: domain.Layout{ColSpan: 5}}}}
	mustNoErr(t, s.SaveSnapshot(sess.ID, ws))
	got, err := s.GetSession(sess.ID)
	mustNoErr(t, err)
	var back domain.WorkspaceState
	mustNoErr(t, json.Unmarshal(got.Snapshot, &back))
	if back.Revision != 3 || len(back.Cells) != 1 || !back.Cells[0].Pinned || back.Cells[0].Layout.ColSpan != 5 {
		t.Fatalf("%+v", back)
	}
}

func TestOpenPathWithSpaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dir with spaces", "t.db")
	mustNoErr(t, os.MkdirAll(filepath.Dir(path), 0o700))
	s, err := Open(path)
	mustNoErr(t, err)
	defer s.Close()
	if _, err := s.CreatePursuit("x", "", 8); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestJourneyRoundTrip(t *testing.T) {
	s, _ := openTest(t)
	p, _ := seed(t, s)
	if got, _ := s.GetPursuit(p.ID); got.Journey != nil {
		t.Fatal("new pursuit has no journey")
	}
	j := domain.Journey{Topics: []domain.JourneyTopic{{Title: "Force", Milestone: "Force Finder", Goal: 3, Progress: 1}}}
	mustNoErr(t, s.SaveJourney(p.ID, j))
	got, err := s.GetPursuit(p.ID)
	mustNoErr(t, err)
	if got.Journey == nil || got.Journey.Topics[0].Progress != 1 {
		t.Fatalf("%+v", got.Journey)
	}
}

func TestOpenMigratesOlderDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", "file:"+path)
	mustNoErr(t, err)
	_, err = db.Exec(`CREATE TABLE pursuits (id TEXT PRIMARY KEY, title TEXT NOT NULL, description TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
		INSERT INTO pursuits VALUES ('p1','Old','',1,1);`)
	mustNoErr(t, err)
	mustNoErr(t, db.Close())
	s, err := Open(path)
	mustNoErr(t, err)
	defer s.Close()
	got, err := s.GetPursuit("p1")
	mustNoErr(t, err)
	if got.Title != "Old" || got.Grade != DefaultGrade {
		t.Fatalf("%+v", got)
	}
}

func TestDataSurvivesReopen(t *testing.T) {
	s, path := openTest(t)
	p, sess := seed(t, s)
	mustNoErr(t, s.SaveDraft(sess.ID, "c1", "my draft"))
	mustNoErr(t, s.Close())
	s2, err := Open(path)
	mustNoErr(t, err)
	defer s2.Close()
	if _, err := s2.GetPursuit(p.ID); err != nil {
		t.Fatal(err)
	}
	d, err := s2.GetDrafts(sess.ID)
	mustNoErr(t, err)
	if d["c1"] != "my draft" {
		t.Fatalf("%v", d)
	}
}

func TestLatestSessionAndEnd(t *testing.T) {
	s, _ := openTest(t)
	p, sess := seed(t, s)
	mustNoErr(t, s.EndSession(sess.ID, "summary", time.Now(), false))
	got, err := s.LatestSession(p.ID)
	mustNoErr(t, err)
	if got.EndedAt.IsZero() || got.Summary != "summary" {
		t.Fatalf("%+v", got)
	}
}

func mustNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
