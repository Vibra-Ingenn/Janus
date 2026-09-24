package memory

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// DB wraps a SQLite connection for Janus memory storage.
type DB struct {
	sql    *sql.DB
	dbPath string
}

// Path returns the file path of the underlying SQLite database.
func (db *DB) Path() string { return db.dbPath }

// Open opens (or creates) the SQLite database at dbPath.
// Pass an empty string to use the default location: ./janus_memory.db
func Open(dbPath string) (*DB, error) {
	if dbPath == "" {
		dbPath = defaultDBPath()
	}

	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("memory: mkdir %q: %w", filepath.Dir(dbPath), err)
	}

	conn, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("memory: open db: %w", err)
	}
	conn.SetMaxOpenConns(1)

	db := &DB{sql: conn, dbPath: dbPath}
	if err := db.migrate(); err != nil {
		conn.Close()
		return nil, err
	}
	return db, nil
}

// Close closes the underlying database connection.
func (db *DB) Close() error { return db.sql.Close() }

func defaultDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "data/janus_memory.db"
	}
	newPath := filepath.Join(home, ".janus", "janus_memory.db")
	oldPath := "data/janus_memory.db"

	// If new database does not exist, but the legacy database exists in workspace, migrate it.
	if _, err := os.Stat(newPath); os.IsNotExist(err) {
		if _, err := os.Stat(oldPath); err == nil {
			if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err == nil {
				if data, err := os.ReadFile(oldPath); err == nil {
					_ = os.WriteFile(newPath, data, 0o644)
				}
			}
		}
	}
	return newPath
}

func (db *DB) migrate() error {
	_, err := db.sql.Exec(`
		CREATE TABLE IF NOT EXISTS sessions (
			id         TEXT PRIMARY KEY,
			title      TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL DEFAULT (unixepoch()),
			updated_at INTEGER NOT NULL DEFAULT (unixepoch())
		);

		CREATE TABLE IF NOT EXISTS messages (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			role       TEXT NOT NULL,
			content    TEXT NOT NULL,
			created_at INTEGER NOT NULL DEFAULT (unixepoch())
		);
		CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id, created_at);

		CREATE TABLE IF NOT EXISTS facts (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			key        TEXT NOT NULL UNIQUE,
			value      TEXT NOT NULL,
			source     TEXT NOT NULL DEFAULT 'user',
			updated_at INTEGER NOT NULL DEFAULT (unixepoch())
		);

		CREATE TABLE IF NOT EXISTS summaries (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			content    TEXT NOT NULL,
			created_at INTEGER NOT NULL DEFAULT (unixepoch())
		);

		CREATE TABLE IF NOT EXISTS protocol_checkpoints (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			batch_id      TEXT NOT NULL,
			protocol_id   TEXT NOT NULL,
			status        TEXT NOT NULL DEFAULT 'pending',
			output        TEXT NOT NULL DEFAULT '',
			error         TEXT NOT NULL DEFAULT '',
			attempt       INTEGER NOT NULL DEFAULT 0,
			started_at    INTEGER NOT NULL DEFAULT (unixepoch()),
			completed_at  INTEGER NOT NULL DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS idx_checkpoints_batch ON protocol_checkpoints(batch_id, status);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_checkpoints_unique ON protocol_checkpoints(batch_id, protocol_id);
	`)
	if err != nil {
		return fmt.Errorf("memory: migrate: %w", err)
	}
	return nil
}

