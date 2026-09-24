package memory

import (
	"path/filepath"
	"strings"
	"testing"
)

// openTestDB opens a fresh in-memory-style DB in a temp dir for each test.
func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// ─── DB open / path ───────────────────────────────────────────────────────────

func TestOpenCreatesDB(t *testing.T) {
	db := openTestDB(t)
	if db.Path() == "" {
		t.Error("Path() should not be empty")
	}
}

func TestOpenIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "janus.db")
	db1, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	db1.Close()
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open (same path): %v", err)
	}
	db2.Close()
}

// ─── Sessions ─────────────────────────────────────────────────────────────────

func TestCreateAndGetSession(t *testing.T) {
	db := openTestDB(t)
	s, err := db.CreateSession("s1", "My Session")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if s.ID != "s1" || s.Title != "My Session" {
		t.Errorf("unexpected session: %+v", s)
	}

	got, err := db.GetSession("s1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.ID != "s1" || got.Title != "My Session" {
		t.Errorf("retrieved session mismatch: %+v", got)
	}
}

func TestGetSessionNotFound(t *testing.T) {
	db := openTestDB(t)
	s, err := db.GetSession("nonexistent")
	if err != nil {
		t.Fatalf("GetSession should not error for missing: %v", err)
	}
	if s.ID != "" {
		t.Errorf("expected empty session, got %+v", s)
	}
}

func TestListSessionsEmpty(t *testing.T) {
	db := openTestDB(t)
	sessions, err := db.ListSessions(10)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(sessions))
	}
}

func TestListSessionsOrdered(t *testing.T) {
	db := openTestDB(t)
	db.CreateSession("s1", "first")
	db.CreateSession("s2", "second")
	// Force s1 to have a strictly newer updated_at regardless of clock resolution.
	db.sql.Exec(`UPDATE sessions SET updated_at = updated_at + 2 WHERE id = 's1'`)

	sessions, err := db.ListSessions(10)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
	if sessions[0].ID != "s1" {
		t.Errorf("expected s1 first (most recently updated), got %s", sessions[0].ID)
	}
}

func TestListSessionsLimit(t *testing.T) {
	db := openTestDB(t)
	for i := 0; i < 5; i++ {
		db.CreateSession(string(rune('a'+i)), "title")
	}
	sessions, err := db.ListSessions(3)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 3 {
		t.Errorf("expected 3 sessions with limit=3, got %d", len(sessions))
	}
}

func TestDeleteSession(t *testing.T) {
	db := openTestDB(t)
	db.CreateSession("s1", "to delete")
	db.AddMessage("s1", "user", "hello")

	if err := db.DeleteSession("s1"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	got, _ := db.GetSession("s1")
	if got.ID != "" {
		t.Error("session should be gone after delete")
	}
}

// ─── Messages ─────────────────────────────────────────────────────────────────

func TestAddAndGetMessages(t *testing.T) {
	db := openTestDB(t)
	db.CreateSession("s1", "chat")

	msg, err := db.AddMessage("s1", "user", "hello")
	if err != nil {
		t.Fatalf("AddMessage: %v", err)
	}
	if msg.SessionID != "s1" || msg.Role != "user" || msg.Content != "hello" {
		t.Errorf("unexpected message: %+v", msg)
	}

	db.AddMessage("s1", "assistant", "hi there")

	msgs, err := db.GetMessages("s1")
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[1].Role != "assistant" {
		t.Errorf("message order wrong: %+v", msgs)
	}
}

func TestGetMessagesEmpty(t *testing.T) {
	db := openTestDB(t)
	db.CreateSession("s1", "empty")
	msgs, err := db.GetMessages("s1")
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages, got %d", len(msgs))
	}
}

// ─── Facts ────────────────────────────────────────────────────────────────────

func TestUpsertAndListFacts(t *testing.T) {
	db := openTestDB(t)

	if err := db.UpsertFact("name", "Alice", "user"); err != nil {
		t.Fatalf("UpsertFact: %v", err)
	}
	if err := db.UpsertFact("role", "doctor", "user"); err != nil {
		t.Fatalf("UpsertFact: %v", err)
	}

	facts, err := db.ListFacts()
	if err != nil {
		t.Fatalf("ListFacts: %v", err)
	}
	if len(facts) != 2 {
		t.Fatalf("expected 2 facts, got %d", len(facts))
	}
}

func TestUpsertFactUpdates(t *testing.T) {
	db := openTestDB(t)
	db.UpsertFact("key", "original", "user")
	db.UpsertFact("key", "updated", "user")

	facts, _ := db.ListFacts()
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact after upsert, got %d", len(facts))
	}
	if facts[0].Value != "updated" {
		t.Errorf("expected updated value, got %q", facts[0].Value)
	}
}

func TestDeleteFact(t *testing.T) {
	db := openTestDB(t)
	db.UpsertFact("name", "Alice", "user")

	if err := db.DeleteFact("name"); err != nil {
		t.Fatalf("DeleteFact: %v", err)
	}

	facts, _ := db.ListFacts()
	if len(facts) != 0 {
		t.Errorf("expected 0 facts after delete, got %d", len(facts))
	}
}

func TestFactsAsContextEmpty(t *testing.T) {
	db := openTestDB(t)
	if s := db.FactsAsContext(); s != "" {
		t.Errorf("expected empty context with no facts, got %q", s)
	}
}

func TestFactsAsContextPopulated(t *testing.T) {
	db := openTestDB(t)
	db.UpsertFact("name", "Alice", "user")
	db.UpsertFact("specialty", "cardiology", "user")

	ctx := db.FactsAsContext()
	if ctx == "" {
		t.Fatal("expected non-empty facts context")
	}
	for _, want := range []string{"name", "Alice", "specialty", "cardiology"} {
		if !strings.Contains(ctx, want) {
			t.Errorf("context missing %q", want)
		}
	}
}

// ─── Checkpoints ─────────────────────────────────────────────────────────────

func TestUpsertAndGetCheckpoints(t *testing.T) {
	db := openTestDB(t)

	err := db.UpsertCheckpoint("batch1", "proto1", CheckpointPending, "", "", 0)
	if err != nil {
		t.Fatalf("UpsertCheckpoint: %v", err)
	}

	cps, err := db.GetCheckpoints("batch1")
	if err != nil {
		t.Fatalf("GetCheckpoints: %v", err)
	}
	if len(cps) != 1 {
		t.Fatalf("expected 1 checkpoint, got %d", len(cps))
	}
	if cps[0].Status != CheckpointPending {
		t.Errorf("expected pending, got %q", cps[0].Status)
	}
}

func TestUpsertCheckpointUpdates(t *testing.T) {
	db := openTestDB(t)
	db.UpsertCheckpoint("b1", "p1", CheckpointPending, "", "", 0)
	db.UpsertCheckpoint("b1", "p1", CheckpointSuccess, "done", "", 1)

	cps, _ := db.GetCheckpoints("b1")
	if len(cps) != 1 {
		t.Fatalf("expected 1 checkpoint after upsert, got %d", len(cps))
	}
	if cps[0].Status != CheckpointSuccess {
		t.Errorf("expected success status, got %q", cps[0].Status)
	}
	if cps[0].Output != "done" {
		t.Errorf("expected output 'done', got %q", cps[0].Output)
	}
}

func TestGetCheckpointsEmpty(t *testing.T) {
	db := openTestDB(t)
	cps, err := db.GetCheckpoints("nonexistent")
	if err != nil {
		t.Fatalf("GetCheckpoints: %v", err)
	}
	if len(cps) != 0 {
		t.Errorf("expected 0 checkpoints, got %d", len(cps))
	}
}

func TestGetPendingProtocolIDs(t *testing.T) {
	db := openTestDB(t)
	db.UpsertCheckpoint("b1", "p1", CheckpointSuccess, "ok", "", 1)
	db.UpsertCheckpoint("b1", "p2", CheckpointPending, "", "", 0)

	allIDs := []string{"p1", "p2", "p3"} // p3 not in DB yet
	pending, err := db.GetPendingProtocolIDs("b1", allIDs)
	if err != nil {
		t.Fatalf("GetPendingProtocolIDs: %v", err)
	}
	// p1 succeeded → excluded; p2 pending → included; p3 never inserted → included
	if len(pending) != 2 {
		t.Errorf("expected 2 pending, got %d: %v", len(pending), pending)
	}
	for _, id := range pending {
		if id == "p1" {
			t.Error("p1 succeeded, should not be in pending list")
		}
	}
}

func TestGetPendingProtocolIDsAllSucceeded(t *testing.T) {
	db := openTestDB(t)
	db.UpsertCheckpoint("b1", "p1", CheckpointSuccess, "ok", "", 1)

	pending, err := db.GetPendingProtocolIDs("b1", []string{"p1"})
	if err != nil {
		t.Fatalf("GetPendingProtocolIDs: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("expected 0 pending when all succeeded, got %d", len(pending))
	}
}

func TestGetPendingProtocolIDsEmpty(t *testing.T) {
	db := openTestDB(t)
	pending, err := db.GetPendingProtocolIDs("b1", nil)
	if err != nil {
		t.Fatalf("GetPendingProtocolIDs with nil: %v", err)
	}
	if pending != nil {
		t.Errorf("expected nil for empty input, got %v", pending)
	}
}

