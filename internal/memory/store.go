package memory

import (
	"database/sql"
	"fmt"
	"time"
)

// Session represents a conversation session.
type Session struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// Message is a single chat turn stored in memory.
type Message struct {
	ID        int64  `json:"id"`
	SessionID string `json:"session_id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	CreatedAt int64  `json:"created_at"`
}

// Fact is a persistent key/value remembered across sessions.
type Fact struct {
	ID        int64  `json:"id"`
	Key       string `json:"key"`
	Value     string `json:"value"`
	Source    string `json:"source"`
	UpdatedAt int64  `json:"updated_at"`
}

// CreateSession creates a new session and returns it.
func (db *DB) CreateSession(id, title string) (Session, error) {
	now := time.Now().Unix()
	_, err := db.sql.Exec(
		`INSERT INTO sessions(id, title, created_at, updated_at) VALUES(?,?,?,?)`,
		id, title, now, now,
	)
	if err != nil {
		return Session{}, fmt.Errorf("memory: create session: %w", err)
	}
	return Session{ID: id, Title: title, CreatedAt: now, UpdatedAt: now}, nil
}

// GetSession retrieves a session by ID.
func (db *DB) GetSession(id string) (Session, error) {
	var s Session
	err := db.sql.QueryRow(
		`SELECT id, title, created_at, updated_at FROM sessions WHERE id=?`, id,
	).Scan(&s.ID, &s.Title, &s.CreatedAt, &s.UpdatedAt)
	if err == sql.ErrNoRows {
		return Session{}, nil
	}
	return s, err
}

// ListSessions returns sessions ordered by most recently updated.
func (db *DB) ListSessions(limit int) ([]Session, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := db.sql.Query(
		`SELECT id, title, created_at, updated_at FROM sessions ORDER BY updated_at DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var s Session
		if err := rows.Scan(&s.ID, &s.Title, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// TouchSession updates the updated_at timestamp of a session.
func (db *DB) TouchSession(id string) {
	db.sql.Exec(`UPDATE sessions SET updated_at=? WHERE id=?`, time.Now().Unix(), id)
}

// AddMessage appends a message to a session.
func (db *DB) AddMessage(sessionID, role, content string) (Message, error) {
	now := time.Now().Unix()
	res, err := db.sql.Exec(
		`INSERT INTO messages(session_id, role, content, created_at) VALUES(?,?,?,?)`,
		sessionID, role, content, now,
	)
	if err != nil {
		return Message{}, fmt.Errorf("memory: add message: %w", err)
	}
	id, _ := res.LastInsertId()
	db.TouchSession(sessionID)
	return Message{ID: id, SessionID: sessionID, Role: role, Content: content, CreatedAt: now}, nil
}

// GetMessages returns all messages for a session in order.
func (db *DB) GetMessages(sessionID string) ([]Message, error) {
	rows, err := db.sql.Query(
		`SELECT id, session_id, role, content, created_at FROM messages WHERE session_id=? ORDER BY created_at, id`,
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Content, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// DeleteSession removes a session and all its messages.
func (db *DB) DeleteSession(id string) error {
	_, err := db.sql.Exec(`DELETE FROM sessions WHERE id=?`, id)
	return err
}

// UpsertFact stores or updates a remembered fact.
func (db *DB) UpsertFact(key, value, source string) error {
	now := time.Now().Unix()
	_, err := db.sql.Exec(
		`INSERT INTO facts(key, value, source, updated_at) VALUES(?,?,?,?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value, source=excluded.source, updated_at=excluded.updated_at`,
		key, value, source, now,
	)
	return err
}

// ListFacts returns all stored facts.
func (db *DB) ListFacts() ([]Fact, error) {
	rows, err := db.sql.Query(`SELECT id, key, value, source, updated_at FROM facts ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Fact
	for rows.Next() {
		var f Fact
		if err := rows.Scan(&f.ID, &f.Key, &f.Value, &f.Source, &f.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// DeleteFact removes a fact by key.
func (db *DB) DeleteFact(key string) error {
	_, err := db.sql.Exec(`DELETE FROM facts WHERE key=?`, key)
	return err
}

// FactsAsContext returns a formatted string of all non-brief facts suitable for
// injecting into a system prompt.
func (db *DB) FactsAsContext() string {
	facts, err := db.ListFacts()
	if err != nil || len(facts) == 0 {
		return ""
	}
	var out string
	for _, f := range facts {
		if f.Key == "__project_brief__" {
			continue // rendered separately via ProjectBriefAsContext
		}
		out += fmt.Sprintf("- %s: %s\n", f.Key, f.Value)
	}
	if out == "" {
		return ""
	}
	return "Things I remember about you:\n" + out
}

// SetProjectBrief stores the human-authored project brief.
// The brief is the human's layer in the human-AI partnership:
// project goals, vision, expected outcomes, and what each part does.
// It is injected at the top of every AI system prompt automatically.
func (db *DB) SetProjectBrief(brief string) error {
	return db.UpsertFact("__project_brief__", brief, "user")
}

// GetProjectBrief retrieves the stored project brief.
// Returns empty string if none has been set yet.
func (db *DB) GetProjectBrief() string {
	rows, err := db.sql.Query(`SELECT value FROM facts WHERE key='__project_brief__'`)
	if err != nil {
		return ""
	}
	defer rows.Close()
	if rows.Next() {
		var v string
		rows.Scan(&v)
		return v
	}
	return ""
}

// ProjectBriefAsContext formats the project brief for injection into a system prompt.
// This is the human's contribution to the human-AI partnership — the big picture
// context the AI would otherwise lose between sessions.
func (db *DB) ProjectBriefAsContext() string {
	brief := db.GetProjectBrief()
	if brief == "" {
		return ""
	}
	return "=== PROJECT BRIEF (written by the human operator) ===\n" +
		brief + "\n" +
		"=== END PROJECT BRIEF ===\n\n"
}

// Summary is a compressed snapshot of a session, used for long-term memory.
type Summary struct {
	ID        int64  `json:"id"`
	SessionID string `json:"session_id"`
	Content   string `json:"content"`
	CreatedAt int64  `json:"created_at"`
}

// SaveSummary stores a compressed summary for a session.
// Call this after the Pruner compresses a long conversation.
func (db *DB) SaveSummary(sessionID, content string) error {
	now := time.Now().Unix()
	_, err := db.sql.Exec(
		`INSERT INTO summaries(session_id, content, created_at) VALUES(?,?,?)`,
		sessionID, content, now,
	)
	return err
}

// GetLatestSummary returns the most recent summary for a session.
// Returns empty string and no error if none exists.
func (db *DB) GetLatestSummary(sessionID string) (string, error) {
	var content string
	err := db.sql.QueryRow(
		`SELECT content FROM summaries WHERE session_id=? ORDER BY created_at DESC LIMIT 1`,
		sessionID,
	).Scan(&content)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return content, err
}

// ListSummaries returns all summaries for a session, oldest first.
func (db *DB) ListSummaries(sessionID string) ([]Summary, error) {
	rows, err := db.sql.Query(
		`SELECT id, session_id, content, created_at FROM summaries WHERE session_id=? ORDER BY created_at`,
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Summary
	for rows.Next() {
		var s Summary
		if err := rows.Scan(&s.ID, &s.SessionID, &s.Content, &s.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

