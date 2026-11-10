CREATE TABLE IF NOT EXISTS pursuits (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  grade INTEGER NOT NULL DEFAULT 8,
  journey TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
  id TEXT PRIMARY KEY,
  pursuit_id TEXT NOT NULL REFERENCES pursuits(id) ON DELETE CASCADE,
  inquiry TEXT NOT NULL DEFAULT '',
  started_at INTEGER NOT NULL,
  ended_at INTEGER NOT NULL DEFAULT 0,
  summary TEXT NOT NULL DEFAULT '',
  workspace_snapshot TEXT,
  retrieval_questions TEXT,
  needs_consolidation INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS sessions_pursuit ON sessions(pursuit_id, started_at);

CREATE TABLE IF NOT EXISTS messages (
  seq INTEGER PRIMARY KEY AUTOINCREMENT,
  id TEXT NOT NULL UNIQUE,
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  role TEXT NOT NULL,
  content TEXT NOT NULL,
  interrupted INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS messages_session ON messages(session_id, seq);

CREATE TABLE IF NOT EXISTS concept_objects (
  id TEXT PRIMARY KEY,
  pursuit_id TEXT NOT NULL REFERENCES pursuits(id) ON DELETE CASCADE,
  type TEXT NOT NULL,
  content TEXT NOT NULL,
  related_ids TEXT NOT NULL DEFAULT '[]',
  authorship TEXT NOT NULL,
  confidence TEXT NOT NULL DEFAULT 'low',
  status TEXT NOT NULL DEFAULT 'active',
  source_event_id TEXT NOT NULL DEFAULT '',
  version INTEGER NOT NULL DEFAULT 1,
  updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS objects_pursuit ON concept_objects(pursuit_id);

CREATE TABLE IF NOT EXISTS object_versions (
  object_id TEXT NOT NULL REFERENCES concept_objects(id) ON DELETE CASCADE,
  version INTEGER NOT NULL,
  content TEXT NOT NULL,
  authorship TEXT NOT NULL,
  changed_at INTEGER NOT NULL,
  PRIMARY KEY (object_id, version)
);

CREATE TABLE IF NOT EXISTS evidence (
  id TEXT PRIMARY KEY,
  pursuit_id TEXT NOT NULL REFERENCES pursuits(id) ON DELETE CASCADE,
  session_id TEXT NOT NULL,
  concept TEXT NOT NULL,
  learner_action TEXT NOT NULL DEFAULT '',
  tier TEXT NOT NULL,
  correctness TEXT NOT NULL DEFAULT '',
  reasoning TEXT NOT NULL DEFAULT '',
  rationale TEXT NOT NULL DEFAULT '',
  uncertainty TEXT NOT NULL DEFAULT '',
  source_event_id TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS evidence_pursuit ON evidence(pursuit_id, created_at);

CREATE TABLE IF NOT EXISTS events (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  type TEXT NOT NULL,
  origin TEXT NOT NULL DEFAULT '',
  payload TEXT,
  context_version INTEGER NOT NULL DEFAULT 0,
  dedupe_key TEXT NOT NULL UNIQUE,
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS events_session ON events(session_id, created_at);

CREATE TABLE IF NOT EXISTS artifacts (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  cell_id TEXT NOT NULL,
  cell_type TEXT NOT NULL,
  content TEXT,
  learner_authored INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS drafts (
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  cell_id TEXT NOT NULL,
  content TEXT NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (session_id, cell_id)
);

CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
