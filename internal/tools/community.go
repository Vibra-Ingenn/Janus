package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// CommunityToolDef is the 7-box universal tool definition.
// Anyone can fill in these fields to add a new capability to Janus — no Go code required.
// Think of it like a party macro: define once, call as many times as you want with
// different arguments. The AI fills in the {{param}} placeholders from context.
type CommunityToolDef struct {
	// Identity
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Author      string            `json:"author"`
	Version     string            `json:"version"`
	Tags        []string          `json:"tags"`
	Parameters  map[string]string `json:"parameters"` // param_name -> human description

	// The 7 Protocol fields — {{param_name}} placeholders are substituted at call time.
	// Box 1: ProtocolID is derived from Name at runtime.
	// Box 2:
	StatusBroadcast string `json:"status_broadcast"` // workflow context / queue tag
	// Box 3:
	EntitySelector string `json:"entity_selector"` // e.g. "{{file_path}}" or a fixed URL
	// Box 4:
	PrimaryOperation string `json:"primary_operation"` // e.g. "shell:python upload.py --file {{file_path}}"
	// Box 5:
	StepLatency int `json:"step_latency"` // milliseconds; 0 = no pre-run delay
	// Box 6:
	RemediationLogic string `json:"remediation_logic"` // "fail" | "retry:N" | "retry:N:backoff" | "fallback:<cmd>" | "dlq" | "heal-auto"
	// Box 7:
	AuditReceipt string `json:"audit_receipt"` // expected hash for verification; stored in audit log

	// Source metadata
	SourceURL   string `json:"source_url,omitempty"`
	Enabled     bool   `json:"enabled"`
	InstalledAt int64  `json:"installed_at,omitempty"`
}

// CommunityStore persists community tool definitions in a local SQLite database.
type CommunityStore struct {
	db     *sql.DB
	dbPath string
}

// OpenCommunityStore opens (or creates) the community tools database.
// Pass an empty string to use the default: data/community_tools.db
func OpenCommunityStore(dbDir string) (*CommunityStore, error) {
	if dbDir == "" {
		dbDir = "data"
	}
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		return nil, fmt.Errorf("community: mkdir %q: %w", dbDir, err)
	}
	path := filepath.Join(dbDir, "community_tools.db")
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("community: open %q: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	cs := &CommunityStore{db: db, dbPath: path}
	if err := cs.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return cs, nil
}

// Close releases the database connection.
func (cs *CommunityStore) Close() error { return cs.db.Close() }

// Path returns the database file path.
func (cs *CommunityStore) Path() string { return cs.dbPath }

func (cs *CommunityStore) migrate() error {
	_, err := cs.db.Exec(`
		CREATE TABLE IF NOT EXISTS community_tools (
			id                INTEGER PRIMARY KEY AUTOINCREMENT,
			name              TEXT NOT NULL UNIQUE,
			description       TEXT NOT NULL DEFAULT '',
			author            TEXT NOT NULL DEFAULT 'local',
			version           TEXT NOT NULL DEFAULT '1.0',
			tags              TEXT NOT NULL DEFAULT '[]',
			parameters        TEXT NOT NULL DEFAULT '{}',
			status_broadcast  TEXT NOT NULL DEFAULT '',
			entity_selector     TEXT NOT NULL DEFAULT '',
			primary_operation TEXT NOT NULL,
			step_latency  INTEGER NOT NULL DEFAULT 0,
			remediation_logic     TEXT NOT NULL DEFAULT 'fail',
			audit_receipt     TEXT NOT NULL DEFAULT '',
			source_url        TEXT NOT NULL DEFAULT '',
			enabled           INTEGER NOT NULL DEFAULT 1,
			installed_at      INTEGER NOT NULL DEFAULT (unixepoch()),
			updated_at        INTEGER NOT NULL DEFAULT (unixepoch())
		);
	`)
	if err != nil {
		return fmt.Errorf("community: migrate: %w", err)
	}
	return nil
}

// Save inserts or updates a community tool definition.
func (cs *CommunityStore) Save(def CommunityToolDef) error {
	if def.Name == "" {
		return fmt.Errorf("community: tool name is required")
	}
	if def.PrimaryOperation == "" {
		return fmt.Errorf("community: primary_operation is required")
	}
	if def.RemediationLogic == "" {
		def.RemediationLogic = "fail"
	}
	tagsJSON, _ := json.Marshal(def.Tags)
	if tagsJSON == nil {
		tagsJSON = []byte("[]")
	}
	paramsJSON, _ := json.Marshal(def.Parameters)
	if paramsJSON == nil {
		paramsJSON = []byte("{}")
	}
	enabled := 1
	if !def.Enabled {
		enabled = 0
	}
	if def.InstalledAt == 0 {
		def.InstalledAt = time.Now().Unix()
	}
	_, err := cs.db.Exec(`
		INSERT INTO community_tools
			(name, description, author, version, tags, parameters,
			 status_broadcast, entity_selector, primary_operation,
			 step_latency, remediation_logic, audit_receipt,
			 source_url, enabled, installed_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,unixepoch())
		ON CONFLICT(name) DO UPDATE SET
			description=excluded.description,
			author=excluded.author,
			version=excluded.version,
			tags=excluded.tags,
			parameters=excluded.parameters,
			status_broadcast=excluded.status_broadcast,
			entity_selector=excluded.entity_selector,
			primary_operation=excluded.primary_operation,
			step_latency=excluded.step_latency,
			remediation_logic=excluded.remediation_logic,
			audit_receipt=excluded.audit_receipt,
			source_url=excluded.source_url,
			enabled=excluded.enabled,
			updated_at=unixepoch()
	`,
		def.Name, def.Description, def.Author, def.Version,
		string(tagsJSON), string(paramsJSON),
		def.StatusBroadcast, def.EntitySelector, def.PrimaryOperation,
		def.StepLatency, def.RemediationLogic, def.AuditReceipt,
		def.SourceURL, enabled, def.InstalledAt,
	)
	return err
}

// Delete removes a community tool by name.
func (cs *CommunityStore) Delete(name string) error {
	res, err := cs.db.Exec("DELETE FROM community_tools WHERE name=?", name)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("community: tool %q not found", name)
	}
	return nil
}

// Enable or disable a community tool without deleting it.
func (cs *CommunityStore) SetEnabled(name string, enabled bool) error {
	v := 1
	if !enabled {
		v = 0
	}
	res, err := cs.db.Exec("UPDATE community_tools SET enabled=?, updated_at=unixepoch() WHERE name=?", v, name)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("community: tool %q not found", name)
	}
	return nil
}

// List returns all community tools (enabled and disabled).
func (cs *CommunityStore) List() ([]CommunityToolDef, error) {
	rows, err := cs.db.Query(`
		SELECT name, description, author, version, tags, parameters,
		       status_broadcast, entity_selector, primary_operation,
		       step_latency, remediation_logic, audit_receipt,
		       source_url, enabled, installed_at
		FROM community_tools ORDER BY name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCommunityTools(rows)
}

// ListEnabled returns only enabled community tools.
func (cs *CommunityStore) ListEnabled() ([]CommunityToolDef, error) {
	rows, err := cs.db.Query(`
		SELECT name, description, author, version, tags, parameters,
		       status_broadcast, entity_selector, primary_operation,
		       step_latency, remediation_logic, audit_receipt,
		       source_url, enabled, installed_at
		FROM community_tools WHERE enabled=1 ORDER BY name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCommunityTools(rows)
}

// Get returns a single community tool by name.
func (cs *CommunityStore) Get(name string) (CommunityToolDef, error) {
	rows, err := cs.db.Query(`
		SELECT name, description, author, version, tags, parameters,
		       status_broadcast, entity_selector, primary_operation,
		       step_latency, remediation_logic, audit_receipt,
		       source_url, enabled, installed_at
		FROM community_tools WHERE name=? LIMIT 1
	`, name)
	if err != nil {
		return CommunityToolDef{}, err
	}
	defer rows.Close()
	defs, err := scanCommunityTools(rows)
	if err != nil {
		return CommunityToolDef{}, err
	}
	if len(defs) == 0 {
		return CommunityToolDef{}, fmt.Errorf("community: tool %q not found", name)
	}
	return defs[0], nil
}

// Count returns the number of installed community tools.
func (cs *CommunityStore) Count() int {
	var n int
	cs.db.QueryRow("SELECT COUNT(*) FROM community_tools").Scan(&n)
	return n
}

func scanCommunityTools(rows *sql.Rows) ([]CommunityToolDef, error) {
	var defs []CommunityToolDef
	for rows.Next() {
		var d CommunityToolDef
		var tagsStr, paramsStr string
		var enabled int
		if err := rows.Scan(
			&d.Name, &d.Description, &d.Author, &d.Version,
			&tagsStr, &paramsStr,
			&d.StatusBroadcast, &d.EntitySelector, &d.PrimaryOperation,
			&d.StepLatency, &d.RemediationLogic, &d.AuditReceipt,
			&d.SourceURL, &enabled, &d.InstalledAt,
		); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(tagsStr), &d.Tags)
		if d.Tags == nil {
			d.Tags = []string{}
		}
		if err := json.Unmarshal([]byte(paramsStr), &d.Parameters); err != nil || d.Parameters == nil {
			d.Parameters = map[string]string{}
		}
		d.Enabled = enabled == 1
		defs = append(defs, d)
	}
	return defs, rows.Err()
}

// FetchPack downloads a tool pack (single CommunityToolDef or a JSON array of them)
// from url and saves all tools. Returns the names of installed tools.
// URLs can be GitHub Gists, raw file links, or any HTTP endpoint returning JSON.
func (cs *CommunityStore) FetchPack(ctx context.Context, rawURL string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("community: fetch request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("community: fetch %q: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("community: fetch %q: HTTP %d", rawURL, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MB max
	if err != nil {
		return nil, fmt.Errorf("community: fetch read: %w", err)
	}

	// Try array of tools first, then a single tool object.
	var defs []CommunityToolDef
	if err := json.Unmarshal(body, &defs); err != nil {
		var single CommunityToolDef
		if err2 := json.Unmarshal(body, &single); err2 != nil {
			return nil, fmt.Errorf("community: fetch parse: not a valid tool definition or tool pack")
		}
		defs = []CommunityToolDef{single}
	}

	var installed []string
	for i := range defs {
		defs[i].SourceURL = rawURL
		defs[i].Enabled = true
		defs[i].InstalledAt = time.Now().Unix()
		if err := cs.Save(defs[i]); err != nil {
			return installed, fmt.Errorf("community: save %q: %w", defs[i].Name, err)
		}
		installed = append(installed, defs[i].Name)
	}
	return installed, nil
}

// SubstituteTemplate replaces {{param_name}} placeholders in s with values from args.
// This is how the AI's provided arguments get wired into the primary_operation and entity_selector.
func SubstituteTemplate(s string, args map[string]any) string {
	for k, v := range args {
		s = strings.ReplaceAll(s, "{{"+k+"}}", fmt.Sprint(v))
	}
	return s
}

// CommunityExecFunc is the callback type used to execute a community tool's Protocol.
// It is provided by the caller (main.go) which has access to the orchestration package,
// avoiding a circular import between tools ↔ orchestration.
type CommunityExecFunc func(protocolID, command, entitySelector, statusBroadcast, remediationLogic, auditReceipt string, stepLatencyMs int) ToolResult

// RegisterCommunityTools loads all enabled community tools from the store and
// registers each as a live callable tool in the registry.
// The execFn is provided by the caller and runs the substituted Protocol through
// the orchestration engine.
func RegisterCommunityTools(cs *CommunityStore, reg *Registry, execFn CommunityExecFunc) error {
	defs, err := cs.ListEnabled()
	if err != nil {
		return fmt.Errorf("community: load tools: %w", err)
	}
	for _, def := range defs {
		d := def // capture loop variable
		toolDef := ToolDef{
			Name:        d.Name,
			Description: d.Description + " [community tool by " + d.Author + "]",
			Parameters:  d.Parameters,
		}
		handler := ToolFunc(func(args map[string]any) ToolResult {
			cmd := SubstituteTemplate(d.PrimaryOperation, args)
			target := SubstituteTemplate(d.EntitySelector, args)
			return execFn(d.Name, cmd, target, d.StatusBroadcast, d.RemediationLogic, d.AuditReceipt, d.StepLatency)
		})
		reg.Register(toolDef, handler)
	}
	return nil
}
