// Package store persists DeepFlow's durable learning data in SQLite.
package store

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

var (
	// ErrNotFound is returned when a row does not exist.
	ErrNotFound = errors.New("not found")
	// ErrVersionConflict is returned when an edit was based on a stale version.
	ErrVersionConflict = errors.New("version conflict")
)

// Store is the SQLite-backed repository.
type Store struct {
	db *sql.DB
}

// Open opens or creates the database at path and applies the schema.
func Open(path string) (*Store, error) {
	u := url.URL{Scheme: "file", Path: path, RawQuery: "_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)"}
	dsn := u.String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection serializes access. WAL transactions that upgrade from read
	// to write would otherwise fail with SQLITE_BUSY, despite the busy timeout.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{db: db}, nil
}

// migrate brings databases from earlier versions up to date. The schema's
// CREATE TABLE IF NOT EXISTS doesn't add columns to tables that already exist.
func migrate(db *sql.DB) error {
	cols := map[string]bool{}
	rows, err := db.Query(`SELECT name FROM pragma_table_info('pursuits')`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		cols[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	if !cols["grade"] {
		if _, err := db.Exec(fmt.Sprintf(`ALTER TABLE pursuits ADD COLUMN grade INTEGER NOT NULL DEFAULT %d`, DefaultGrade)); err != nil {
			return err
		}
	}
	if !cols["journey"] {
		if _, err := db.Exec(`ALTER TABLE pursuits ADD COLUMN journey TEXT`); err != nil {
			return err
		}
	}
	return nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromMs(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v)
}

func nullable(b json.RawMessage) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// GetSetting returns a setting value, or "" if unset.
func (s *Store) GetSetting(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetSetting stores a setting value.
func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}
