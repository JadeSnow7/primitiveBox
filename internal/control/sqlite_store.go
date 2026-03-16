package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"primitivebox/internal/eventing"
	"primitivebox/internal/runtrace"
	"primitivebox/internal/sandbox"

	_ "modernc.org/sqlite"
)

// SQLiteStore persists control-plane state in a local SQLite database.
type SQLiteStore struct {
	db *sql.DB
}

// OpenSQLiteStore opens or creates the SQLite control-plane store.
func OpenSQLiteStore(path string) (*SQLiteStore, error) {
	if path == "" {
		return nil, fmt.Errorf("sqlite store path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create sqlite dir: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite store: %w", err)
	}

	store := &SQLiteStore{db: db}
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *SQLiteStore) init() error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS sandboxes (
			id TEXT PRIMARY KEY,
			driver TEXT,
			namespace TEXT,
			container_id TEXT,
			config_json TEXT NOT NULL,
			status TEXT NOT NULL,
			health_status TEXT,
			rpc_endpoint TEXT,
			rpc_port INTEGER,
			created_at INTEGER,
			updated_at INTEGER,
			last_accessed_at INTEGER,
			expires_at INTEGER,
			labels_json TEXT,
			capabilities_json TEXT,
			metadata_json TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp TEXT NOT NULL,
			type TEXT NOT NULL,
			source TEXT,
			sandbox_id TEXT,
			method TEXT,
			stream TEXT,
			message TEXT,
			data_json TEXT,
			sequence INTEGER DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_events_sandbox_id ON events (sandbox_id, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_events_type ON events (type, id DESC)`,
		`CREATE TABLE IF NOT EXISTS trace_steps (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT,
			trace_id TEXT,
			session_id TEXT,
			attempt_id TEXT,
			sandbox_id TEXT,
			step_id TEXT,
			primitive TEXT NOT NULL,
			checkpoint_id TEXT,
			verify_result TEXT,
			duration_ms INTEGER,
			failure_kind TEXT,
			timestamp TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_trace_steps_sandbox_id ON trace_steps (sandbox_id, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_trace_steps_trace_id ON trace_steps (trace_id, id DESC)`,
	}
	for _, stmt := range statements {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("init sqlite schema: %w", err)
		}
	}
	return nil
}

// Close closes the underlying database.
func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Upsert stores or updates sandbox metadata.
func (s *SQLiteStore) Upsert(ctx context.Context, sb *sandbox.Sandbox) error {
	configJSON, err := json.Marshal(sb.Config)
	if err != nil {
		return err
	}
	labelsJSON, err := json.Marshal(sb.Labels)
	if err != nil {
		return err
	}
	capabilitiesJSON, err := json.Marshal(sb.Capabilities)
	if err != nil {
		return err
	}
	metadataJSON, err := json.Marshal(sb.Metadata)
	if err != nil {
		return err
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO sandboxes (
			id, driver, namespace, container_id, config_json, status, health_status,
			rpc_endpoint, rpc_port, created_at, updated_at, last_accessed_at, expires_at,
			labels_json, capabilities_json, metadata_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			driver=excluded.driver,
			namespace=excluded.namespace,
			container_id=excluded.container_id,
			config_json=excluded.config_json,
			status=excluded.status,
			health_status=excluded.health_status,
			rpc_endpoint=excluded.rpc_endpoint,
			rpc_port=excluded.rpc_port,
			created_at=excluded.created_at,
			updated_at=excluded.updated_at,
			last_accessed_at=excluded.last_accessed_at,
			expires_at=excluded.expires_at,
			labels_json=excluded.labels_json,
			capabilities_json=excluded.capabilities_json,
			metadata_json=excluded.metadata_json
	`, sb.ID, sb.Driver, sb.Namespace, sb.ContainerID, string(configJSON), string(sb.Status), sb.HealthStatus,
		sb.RPCEndpoint, sb.RPCPort, sb.CreatedAt, sb.UpdatedAt, sb.LastAccessedAt, sb.ExpiresAt,
		string(labelsJSON), string(capabilitiesJSON), string(metadataJSON))
	return err
}

// Get fetches a sandbox by ID.
func (s *SQLiteStore) Get(ctx context.Context, sandboxID string) (*sandbox.Sandbox, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, driver, namespace, container_id, config_json, status, health_status,
		       rpc_endpoint, rpc_port, created_at, updated_at, last_accessed_at, expires_at,
		       labels_json, capabilities_json, metadata_json
		FROM sandboxes
		WHERE id = ?
	`, sandboxID)

	sb, err := scanSandbox(row.Scan)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return sb, true, nil
}

// List returns sandboxes ordered by ID.
func (s *SQLiteStore) List(ctx context.Context) ([]*sandbox.Sandbox, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, driver, namespace, container_id, config_json, status, health_status,
		       rpc_endpoint, rpc_port, created_at, updated_at, last_accessed_at, expires_at,
		       labels_json, capabilities_json, metadata_json
		FROM sandboxes
		ORDER BY id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*sandbox.Sandbox
	for rows.Next() {
		sb, err := scanSandbox(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, sb)
	}
	return out, rows.Err()
}

// Delete removes a sandbox record.
func (s *SQLiteStore) Delete(ctx context.Context, sandboxID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sandboxes WHERE id = ?`, sandboxID)
	return err
}

// ListExpired returns sandboxes whose TTL already elapsed.
func (s *SQLiteStore) ListExpired(ctx context.Context, before time.Time, limit int) ([]*sandbox.Sandbox, error) {
	query := `
		SELECT id, driver, namespace, container_id, config_json, status, health_status,
		       rpc_endpoint, rpc_port, created_at, updated_at, last_accessed_at, expires_at,
		       labels_json, capabilities_json, metadata_json
		FROM sandboxes
		WHERE expires_at > 0 AND expires_at <= ? AND status != ?
		ORDER BY expires_at ASC
	`
	args := []any{before.Unix(), string(sandbox.StatusDestroyed)}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*sandbox.Sandbox
	for rows.Next() {
		sb, err := scanSandbox(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, sb)
	}
	return out, rows.Err()
}

// Append persists an event.
func (s *SQLiteStore) Append(ctx context.Context, evt eventing.Event) (eventing.Event, error) {
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO events (timestamp, type, source, sandbox_id, method, stream, message, data_json, sequence)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, evt.Timestamp, evt.Type, evt.Source, evt.SandboxID, evt.Method, evt.Stream, evt.Message, string(evt.Data), evt.Sequence)
	if err != nil {
		return evt, err
	}
	if id, err := result.LastInsertId(); err == nil {
		evt.ID = id
	}
	return evt, nil
}

// List returns persisted events ordered from newest to oldest.
func (s *SQLiteStore) ListEvents(ctx context.Context, filter eventing.ListFilter) ([]eventing.Event, error) {
	query := `SELECT id, timestamp, type, source, sandbox_id, method, stream, message, data_json, sequence FROM events`
	var clauses []string
	var args []any
	if filter.SandboxID != "" {
		clauses = append(clauses, "sandbox_id = ?")
		args = append(args, filter.SandboxID)
	}
	if filter.Method != "" {
		clauses = append(clauses, "method = ?")
		args = append(args, filter.Method)
	}
	if filter.Type != "" {
		clauses = append(clauses, "type = ?")
		args = append(args, filter.Type)
	}
	if len(clauses) > 0 {
		query += " WHERE " + joinClauses(clauses)
	}
	query += " ORDER BY id DESC"
	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []eventing.Event
	for rows.Next() {
		var evt eventing.Event
		var dataJSON string
		if err := rows.Scan(&evt.ID, &evt.Timestamp, &evt.Type, &evt.Source, &evt.SandboxID, &evt.Method, &evt.Stream, &evt.Message, &dataJSON, &evt.Sequence); err != nil {
			return nil, err
		}
		if dataJSON != "" {
			evt.Data = json.RawMessage(dataJSON)
		}
		out = append(out, evt)
	}
	return out, rows.Err()
}

// RecordTraceStep persists a runtime trace summary record.
func (s *SQLiteStore) RecordTraceStep(ctx context.Context, record runtrace.StepRecord) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO trace_steps (
			task_id, trace_id, session_id, attempt_id, sandbox_id, step_id,
			primitive, checkpoint_id, verify_result, duration_ms, failure_kind, timestamp
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, record.TaskID, record.TraceID, record.SessionID, record.AttemptID, record.SandboxID, record.StepID,
		record.Primitive, record.CheckpointID, record.VerifyResult, record.DurationMs, record.FailureKind, record.Timestamp)
	return err
}

// ListTraceSteps returns trace summaries ordered newest-first.
func (s *SQLiteStore) ListTraceSteps(ctx context.Context, sandboxID string, limit int) ([]runtrace.StepRecord, error) {
	query := `SELECT task_id, trace_id, session_id, attempt_id, sandbox_id, step_id, primitive, checkpoint_id, verify_result, duration_ms, failure_kind, timestamp FROM trace_steps`
	args := []any{}
	if sandboxID != "" {
		query += ` WHERE sandbox_id = ?`
		args = append(args, sandboxID)
	}
	query += ` ORDER BY id DESC`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []runtrace.StepRecord
	for rows.Next() {
		var record runtrace.StepRecord
		if err := rows.Scan(
			&record.TaskID,
			&record.TraceID,
			&record.SessionID,
			&record.AttemptID,
			&record.SandboxID,
			&record.StepID,
			&record.Primitive,
			&record.CheckpointID,
			&record.VerifyResult,
			&record.DurationMs,
			&record.FailureKind,
			&record.Timestamp,
		); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

// ImportLegacyRegistryDir migrates JSON registry files into SQLite once.
func (s *SQLiteStore) ImportLegacyRegistryDir(ctx context.Context, registryDir string) (int, error) {
	entries, err := os.ReadDir(registryDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	imported := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(registryDir, entry.Name()))
		if err != nil {
			return imported, err
		}
		var sb sandbox.Sandbox
		if err := json.Unmarshal(data, &sb); err != nil {
			return imported, err
		}
		if _, ok, err := s.Get(ctx, sb.ID); err != nil {
			return imported, err
		} else if ok {
			continue
		}
		if err := s.Upsert(ctx, &sb); err != nil {
			return imported, err
		}
		imported++
	}
	return imported, nil
}

func scanSandbox(scan func(dest ...any) error) (*sandbox.Sandbox, error) {
	var (
		sb               sandbox.Sandbox
		status           string
		configJSON       string
		labelsJSON       string
		capabilitiesJSON string
		metadataJSON     string
	)
	err := scan(&sb.ID, &sb.Driver, &sb.Namespace, &sb.ContainerID, &configJSON, &status, &sb.HealthStatus,
		&sb.RPCEndpoint, &sb.RPCPort, &sb.CreatedAt, &sb.UpdatedAt, &sb.LastAccessedAt, &sb.ExpiresAt,
		&labelsJSON, &capabilitiesJSON, &metadataJSON)
	if err != nil {
		return nil, err
	}
	sb.Status = sandbox.SandboxStatus(status)
	if configJSON != "" {
		if err := json.Unmarshal([]byte(configJSON), &sb.Config); err != nil {
			return nil, err
		}
	}
	if labelsJSON != "" {
		_ = json.Unmarshal([]byte(labelsJSON), &sb.Labels)
	}
	if capabilitiesJSON != "" {
		_ = json.Unmarshal([]byte(capabilitiesJSON), &sb.Capabilities)
	}
	if metadataJSON != "" {
		_ = json.Unmarshal([]byte(metadataJSON), &sb.Metadata)
	}
	return &sb, nil
}

func joinClauses(clauses []string) string {
	if len(clauses) == 0 {
		return ""
	}
	out := clauses[0]
	for i := 1; i < len(clauses); i++ {
		out += " AND " + clauses[i]
	}
	return out
}
