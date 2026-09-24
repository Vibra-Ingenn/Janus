package audit

import (
	"bufio"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestHMACChaining(t *testing.T) {
	dir := t.TempDir()
	Init(dir)

	// Write 3 events
	for i := 0; i < 3; i++ {
		Write(Event{Kind: KindTaskSubmit, TaskID: "test", OK: true})
	}

	// Close logger before reading so file handle is released
	Close()

	// Read back and verify chain
	f, err := os.Open(filepath.Join(dir, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	var events []Event
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		events = append(events, e)
	}
	f.Close()

	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}

	// First event should have empty prev_hmac
	if events[0].PrevHMAC != "" {
		t.Errorf("first event prev_hmac should be empty, got %q", events[0].PrevHMAC)
	}

	// Verify each event's HMAC
	prevHMAC := ""
	for i, e := range events {
		savedHMAC := e.HMAC
		e.PrevHMAC = prevHMAC
		e.HMAC = ""
		payload, _ := json.Marshal(e)
		mac := hmac.New(sha256.New, []byte(getChainSecret()))
		mac.Write(payload)
		expected := hex.EncodeToString(mac.Sum(nil))

		if savedHMAC != expected {
			t.Errorf("event %d: HMAC mismatch: got %s, want %s", i, savedHMAC, expected)
		}

		// Chain check: event's prev_hmac must match the previous event's hmac
		if i > 0 && events[i].PrevHMAC != events[i-1].HMAC {
			t.Errorf("event %d: prev_hmac %q != event %d hmac %q", i, events[i].PrevHMAC, i-1, events[i-1].HMAC)
		}

		prevHMAC = savedHMAC
	}
}

func TestInitCreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "logs")
	Init(dir)

	if _, err := os.Stat(filepath.Join(dir, "audit.jsonl")); os.IsNotExist(err) {
		t.Error("audit.jsonl was not created")
	}
	Close()
}

