package memory

import "time"

// CheckpointStatus values.
const (
	CheckpointPending = "pending"
	CheckpointSuccess = "success"
	CheckpointFailed  = "failed"
)

// Checkpoint records the durable state of one protocol in a batch.
type Checkpoint struct {
	ID          int64
	BatchID     string
	ProtocolID  string
	Status      string
	Output      string
	Error       string
	Attempt     int
	StartedAt   time.Time
	CompletedAt time.Time
}

// UpsertCheckpoint creates or updates a checkpoint for the given batch/protocol.
func (db *DB) UpsertCheckpoint(batchID, protocolID, status, output, errStr string, attempt int) error {
	_, err := db.sql.Exec(`
		INSERT INTO protocol_checkpoints (batch_id, protocol_id, status, output, error, attempt, started_at, completed_at)
		VALUES (?,?,?,?,?,?,unixepoch(), CASE WHEN ? IN ('success','failed') THEN unixepoch() ELSE 0 END)
		ON CONFLICT(batch_id, protocol_id) DO UPDATE SET
			status=excluded.status,
			output=excluded.output,
			error=excluded.error,
			attempt=excluded.attempt,
			completed_at=excluded.completed_at
	`, batchID, protocolID, status, output, errStr, attempt, status)
	return err
}

// GetCheckpoints returns all checkpoints for a batch.
func (db *DB) GetCheckpoints(batchID string) ([]Checkpoint, error) {
	rows, err := db.sql.Query(`
		SELECT id, batch_id, protocol_id, status, output, error, attempt, started_at, completed_at
		FROM protocol_checkpoints WHERE batch_id=? ORDER BY id
	`, batchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cps []Checkpoint
	for rows.Next() {
		var c Checkpoint
		var startedUnix, completedUnix int64
		if err := rows.Scan(&c.ID, &c.BatchID, &c.ProtocolID, &c.Status, &c.Output, &c.Error, &c.Attempt, &startedUnix, &completedUnix); err != nil {
			return nil, err
		}
		c.StartedAt = time.Unix(startedUnix, 0).UTC()
		if completedUnix > 0 {
			c.CompletedAt = time.Unix(completedUnix, 0).UTC()
		}
		cps = append(cps, c)
	}
	return cps, rows.Err()
}

// GetPendingProtocolIDs returns the protocol IDs in a batch that have not yet succeeded.
// Used to resume a batch from the last checkpoint.
func (db *DB) GetPendingProtocolIDs(batchID string, allIDs []string) ([]string, error) {
	if len(allIDs) == 0 {
		return nil, nil
	}
	rows, err := db.sql.Query(
		`SELECT protocol_id FROM protocol_checkpoints WHERE batch_id=? AND status='success'`, batchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	succeeded := make(map[string]bool)
	for rows.Next() {
		var pid string
		rows.Scan(&pid)
		succeeded[pid] = true
	}
	var pending []string
	for _, id := range allIDs {
		if !succeeded[id] {
			pending = append(pending, id)
		}
	}
	return pending, rows.Err()
}

