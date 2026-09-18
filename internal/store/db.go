// Package store owns the SQLite database: schema migration and all queries.
package store

import (
	"database/sql"
	"fmt"
	"net/url"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, keeps CGO_ENABLED=0 builds possible
)

// DB wraps the SQLite handle and exposes the DAOs.
type DB struct {
	sql *sql.DB
}

// Open opens (creating if needed) the SQLite database at path and runs
// migrations.
func Open(path string) (*DB, error) {
	// Build a proper file: URI. PathEscape must not be applied to the whole
	// path (it would encode the separators); URL.String escapes only the
	// characters that are actually unsafe in a URI path.
	dsn := (&url.URL{Scheme: "file", Path: path}).String() +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=synchronous(NORMAL)"

	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// A single connection sidesteps SQLITE_BUSY entirely. The workload is a
	// personal admin tool with tiny queries, so serialising is not a concern.
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetConnMaxLifetime(time.Hour)

	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	db := &DB{sql: sqlDB}
	if err := db.migrate(); err != nil {
		sqlDB.Close()
		return nil, err
	}
	return db, nil
}

// Close releases the database handle.
func (d *DB) Close() error { return d.sql.Close() }

// migrations are applied in order. Each entry runs exactly once and its index
// is recorded in schema_migrations.
var migrations = []string{
	// 1: initial schema
	`
CREATE TABLE profiles (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    name             TEXT    NOT NULL UNIQUE,
    description      TEXT    NOT NULL DEFAULT '',
    model            TEXT    NOT NULL DEFAULT '',
    reasoning_effort TEXT    NOT NULL DEFAULT '',
    sandbox_mode     TEXT    NOT NULL DEFAULT 'read-only',
    approval_policy  TEXT    NOT NULL DEFAULT 'never',
    extra_config     TEXT    NOT NULL DEFAULT '',
    work_dir         TEXT    NOT NULL DEFAULT '',
    is_minimal       INTEGER NOT NULL DEFAULT 0,
    created_at       TEXT    NOT NULL,
    updated_at       TEXT    NOT NULL
);

CREATE TABLE conversations (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    title      TEXT    NOT NULL DEFAULT 'New conversation',
    profile_id INTEGER NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    thread_id  TEXT    NOT NULL DEFAULT '',
    status     TEXT    NOT NULL DEFAULT 'active',
    created_at TEXT    NOT NULL,
    updated_at TEXT    NOT NULL
);
CREATE INDEX idx_conversations_profile ON conversations(profile_id);

CREATE TABLE messages (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    conversation_id INTEGER NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    role            TEXT    NOT NULL,
    content         TEXT    NOT NULL DEFAULT '',
    status          TEXT    NOT NULL DEFAULT 'ok',
    error           TEXT    NOT NULL DEFAULT '',
    input_tokens    INTEGER NOT NULL DEFAULT 0,
    output_tokens   INTEGER NOT NULL DEFAULT 0,
    duration_ms     INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT    NOT NULL
);
CREATE INDEX idx_messages_conversation ON messages(conversation_id, id);

CREATE TABLE tasks (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT    NOT NULL,
    prompt      TEXT    NOT NULL,
    profile_id  INTEGER NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    cron_expr   TEXT    NOT NULL,
    enabled     INTEGER NOT NULL DEFAULT 1,
    work_dir    TEXT    NOT NULL DEFAULT '',
    timeout_sec INTEGER NOT NULL DEFAULT 300,
    last_run_at TEXT,
    next_run_at TEXT,
    last_status TEXT    NOT NULL DEFAULT '',
    created_at  TEXT    NOT NULL,
    updated_at  TEXT    NOT NULL
);
CREATE INDEX idx_tasks_enabled ON tasks(enabled, next_run_at);

CREATE TABLE task_runs (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id       INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    status        TEXT    NOT NULL DEFAULT 'running',
    trigger       TEXT    NOT NULL DEFAULT 'schedule',
    output        TEXT    NOT NULL DEFAULT '',
    error         TEXT    NOT NULL DEFAULT '',
    input_tokens  INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    duration_ms   INTEGER NOT NULL DEFAULT 0,
    started_at    TEXT    NOT NULL,
    finished_at   TEXT
);
CREATE INDEX idx_task_runs_task ON task_runs(task_id, id DESC);
CREATE INDEX idx_task_runs_started ON task_runs(started_at DESC);
`,
	// 2–5: inbound-prompt include flags (Codex config.toml include_* keys).
	// SQLite applies one statement per Exec, so each column is its own migration.
	`ALTER TABLE profiles ADD COLUMN include_permissions_instructions INTEGER NOT NULL DEFAULT 0;`,
	`ALTER TABLE profiles ADD COLUMN include_apps_instructions INTEGER NOT NULL DEFAULT 0;`,
	`ALTER TABLE profiles ADD COLUMN include_collaboration_mode_instructions INTEGER NOT NULL DEFAULT 0;`,
	`ALTER TABLE profiles ADD COLUMN include_environment_context INTEGER NOT NULL DEFAULT 0;`,
	`
CREATE TABLE model_prices (
    id                            INTEGER PRIMARY KEY AUTOINCREMENT,
    pattern                       TEXT    NOT NULL UNIQUE,
    input_usd_per_mtok            REAL    NOT NULL,
    cached_input_usd_per_mtok     REAL    NOT NULL,
    cache_write_usd_per_mtok      REAL    NOT NULL,
    output_usd_per_mtok           REAL    NOT NULL,
    long_input_usd_per_mtok       REAL,
    long_cached_input_usd_per_mtok REAL,
    long_cache_write_usd_per_mtok REAL,
    long_output_usd_per_mtok      REAL,
    long_threshold_tokens         INTEGER NOT NULL DEFAULT 272000,
    priority                      INTEGER NOT NULL DEFAULT 100,
    notes                         TEXT    NOT NULL DEFAULT '',
    created_at                    TEXT    NOT NULL,
    updated_at                    TEXT    NOT NULL
);
`,
	// 7–8: one-shot tasks — schedule_type is 'cron' or 'once', run_at holds the
	// single fire time (RFC3339, UTC) for 'once' tasks.
	`ALTER TABLE tasks ADD COLUMN schedule_type TEXT NOT NULL DEFAULT 'cron';`,
	`ALTER TABLE tasks ADD COLUMN run_at TEXT;`,
	// 9: serialize active assistant turns per conversation. Close any stale
	// running rows before installing the invariant so upgrades are deterministic.
	`
UPDATE messages
SET status = 'error',
    error = 'interrupted: upgraded while this reply was marked running'
WHERE role = 'assistant' AND status = 'running';

CREATE UNIQUE INDEX idx_messages_one_active_turn
ON messages(conversation_id)
WHERE role = 'assistant' AND status = 'running';
`,
	// 10: Task deletion becomes logical so task_runs remain durable history.
	`
ALTER TABLE tasks ADD COLUMN deleted_at TEXT;
CREATE INDEX idx_tasks_active_schedule ON tasks(deleted_at, enabled, next_run_at);
`,
}

func (d *DB) migrate() error {
	if _, err := d.sql.Exec(`
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL
)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	var current int
	if err := d.sql.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	for i := current; i < len(migrations); i++ {
		version := i + 1
		tx, err := d.sql.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", version, err)
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d failed: %w", version, err)
		}
		if _, err := tx.Exec(
			`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
			version, formatTime(time.Now()),
		); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %d: %w", version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", version, err)
		}
	}
	return nil
}

// SchemaVersion reports the highest applied migration.
func (d *DB) SchemaVersion() (int, error) {
	var v int
	err := d.sql.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v)
	return v, err
}

// nowStamp is the canonical on-disk representation for every timestamp.
func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(s string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, s)
}

// nullTime converts a nullable TEXT column into *time.Time.
func nullTime(ns sql.NullString) *time.Time {
	if !ns.Valid || ns.String == "" {
		return nil
	}
	t, err := parseTime(ns.String)
	if err != nil {
		return nil
	}
	return &t
}

// boolToInt maps a Go bool onto SQLite's 0/1 integer convention.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
