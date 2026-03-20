package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"primitivebox/internal/primitive"

	_ "modernc.org/sqlite"
)

const (
	defaultAppID     = "pb-kv-adapter"
	defaultNamespace = "kv"
)

type adapterConfig struct {
	socketPath  string
	manifest    string
	rpcEndpoint string
	appID       string
	namespace   string
	backend     string
	dbPath      string
	noRegister  bool
}

type appRPCRequest struct {
	ID     any             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type appRPCResponse struct {
	ID     any          `json:"id"`
	Result any          `json:"result,omitempty"`
	Error  *appRPCError `json:"error,omitempty"`
}

type appRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *appRPCError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

type httpRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      any             `json:"id"`
}

type httpRPCResponse struct {
	JSONRPC string `json:"jsonrpc"`
	Result  any    `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    any    `json:"data,omitempty"`
	} `json:"error,omitempty"`
	ID any `json:"id"`
}

type manifestFile struct {
	Primitives []primitive.AppPrimitiveManifest `json:"primitives"`
}

type testControl struct {
	DelayMs      int    `json:"delay_ms,omitempty"`
	ErrorCode    int    `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

type entryMetadata struct {
	ContentType string            `json:"content_type,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Version     int               `json:"version,omitempty"`
}

type kvEntry struct {
	Key       string        `json:"key"`
	Value     string        `json:"value"`
	Metadata  entryMetadata `json:"metadata,omitempty"`
	UpdatedAt string        `json:"updated_at"`
}

type kvStore interface {
	Get(ctx context.Context, key string) (kvEntry, bool, error)
	Upsert(ctx context.Context, entry kvEntry) (kvEntry, bool, error)
	Create(ctx context.Context, entry kvEntry) error
	Delete(ctx context.Context, key string) (kvEntry, bool, error)
	List(ctx context.Context, prefix string, limit int) ([]kvEntry, error)
	BatchUpsert(ctx context.Context, entries []kvEntry, mode string) (int, int, error)
	ReplaceAll(ctx context.Context, entries []kvEntry, merge bool) (int, error)
	Close() error
}

var (
	errKeyExists         = errors.New("key already exists")
	errDuplicateBatchKey = errors.New("duplicate key in batch")
)

type memoryKVStore struct {
	mu      sync.RWMutex
	entries map[string]kvEntry
}

func newMemoryKVStore() kvStore {
	return &memoryKVStore{entries: make(map[string]kvEntry)}
}

func (m *memoryKVStore) Get(ctx context.Context, key string) (kvEntry, bool, error) {
	_ = ctx
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok := m.entries[key]
	return cloneEntry(entry), ok, nil
}

func (m *memoryKVStore) Upsert(ctx context.Context, entry kvEntry) (kvEntry, bool, error) {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	prev, ok := m.entries[entry.Key]
	m.entries[entry.Key] = cloneEntry(entry)
	return cloneEntry(prev), ok, nil
}

func (m *memoryKVStore) Create(ctx context.Context, entry kvEntry) error {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.entries[entry.Key]; exists {
		return keyExistsError(entry.Key)
	}
	m.entries[entry.Key] = cloneEntry(entry)
	return nil
}

func (m *memoryKVStore) Delete(ctx context.Context, key string) (kvEntry, bool, error) {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	prev, ok := m.entries[key]
	if ok {
		delete(m.entries, key)
	}
	return cloneEntry(prev), ok, nil
}

func (m *memoryKVStore) List(ctx context.Context, prefix string, limit int) ([]kvEntry, error) {
	_ = ctx
	m.mu.RLock()
	defer m.mu.RUnlock()
	keys := make([]string, 0, len(m.entries))
	for key := range m.entries {
		if prefix == "" || strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if limit > 0 && len(keys) > limit {
		keys = keys[:limit]
	}
	out := make([]kvEntry, 0, len(keys))
	for _, key := range keys {
		out = append(out, cloneEntry(m.entries[key]))
	}
	return out, nil
}

func (m *memoryKVStore) BatchUpsert(ctx context.Context, entries []kvEntry, mode string) (int, int, error) {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()

	keys, err := validateBatchEntries(entries)
	if err != nil {
		return 0, 0, err
	}

	created := 0
	for _, key := range keys {
		_, exists := m.entries[key]
		if mode == "create" && exists {
			return 0, 0, keyExistsError(key)
		}
		if !exists {
			created++
		}
	}

	for _, entry := range entries {
		m.entries[entry.Key] = cloneEntry(entry)
	}
	return len(entries), created, nil
}

func (m *memoryKVStore) ReplaceAll(ctx context.Context, entries []kvEntry, merge bool) (int, error) {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	if !merge {
		m.entries = make(map[string]kvEntry, len(entries))
	}
	for _, entry := range entries {
		m.entries[entry.Key] = cloneEntry(entry)
	}
	return len(entries), nil
}

func (m *memoryKVStore) Close() error {
	return nil
}

type sqliteKVStore struct {
	db *sql.DB
}

func newSQLiteKVStore(dbPath string) (kvStore, error) {
	if strings.TrimSpace(dbPath) == "" {
		return nil, errors.New("db path is required for sqlite backend")
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	store := &sqliteKVStore{db: db}
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *sqliteKVStore) init() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS kv_entries (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			metadata_json TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)
	`)
	return err
}

func (s *sqliteKVStore) Get(ctx context.Context, key string) (kvEntry, bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT value, metadata_json, updated_at FROM kv_entries WHERE key = ?`, key)
	var value string
	var metadataJSON string
	var updatedAt string
	if err := row.Scan(&value, &metadataJSON, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return kvEntry{}, false, nil
		}
		return kvEntry{}, false, err
	}
	metadata, err := decodeMetadata(metadataJSON)
	if err != nil {
		return kvEntry{}, false, err
	}
	return kvEntry{Key: key, Value: value, Metadata: metadata, UpdatedAt: updatedAt}, true, nil
}

func (s *sqliteKVStore) Upsert(ctx context.Context, entry kvEntry) (kvEntry, bool, error) {
	prev, exists, err := s.Get(ctx, entry.Key)
	if err != nil {
		return kvEntry{}, false, err
	}
	metadataJSON, err := encodeMetadata(entry.Metadata)
	if err != nil {
		return kvEntry{}, false, err
	}
	_, err = s.db.ExecContext(
		ctx,
		`INSERT INTO kv_entries(key, value, metadata_json, updated_at) VALUES(?, ?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, metadata_json = excluded.metadata_json, updated_at = excluded.updated_at`,
		entry.Key,
		entry.Value,
		metadataJSON,
		entry.UpdatedAt,
	)
	if err != nil {
		return kvEntry{}, false, err
	}
	return prev, exists, nil
}

func (s *sqliteKVStore) Create(ctx context.Context, entry kvEntry) error {
	metadataJSON, err := encodeMetadata(entry.Metadata)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(
		ctx,
		`INSERT OR IGNORE INTO kv_entries(key, value, metadata_json, updated_at) VALUES(?, ?, ?, ?)`,
		entry.Key,
		entry.Value,
		metadataJSON,
		entry.UpdatedAt,
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return keyExistsError(entry.Key)
	}
	return nil
}

func (s *sqliteKVStore) Delete(ctx context.Context, key string) (kvEntry, bool, error) {
	prev, exists, err := s.Get(ctx, key)
	if err != nil || !exists {
		return prev, exists, err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM kv_entries WHERE key = ?`, key); err != nil {
		return kvEntry{}, false, err
	}
	return prev, true, nil
}

func (s *sqliteKVStore) List(ctx context.Context, prefix string, limit int) ([]kvEntry, error) {
	query := `SELECT key, value, metadata_json, updated_at FROM kv_entries`
	args := []any{}
	if prefix != "" {
		query += ` WHERE key LIKE ?`
		args = append(args, prefix+"%")
	}
	query += ` ORDER BY key ASC`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []kvEntry
	for rows.Next() {
		var entry kvEntry
		var metadataJSON string
		if err := rows.Scan(&entry.Key, &entry.Value, &metadataJSON, &entry.UpdatedAt); err != nil {
			return nil, err
		}
		metadata, err := decodeMetadata(metadataJSON)
		if err != nil {
			return nil, err
		}
		entry.Metadata = metadata
		out = append(out, entry)
	}
	return out, rows.Err()
}

func (s *sqliteKVStore) BatchUpsert(ctx context.Context, entries []kvEntry, mode string) (int, int, error) {
	keys, err := validateBatchEntries(entries)
	if err != nil {
		return 0, 0, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	existingKeys, err := lookupExistingKeys(ctx, tx, keys)
	if err != nil {
		return 0, 0, err
	}
	if mode == "create" && len(existingKeys) > 0 {
		for _, entry := range entries {
			if _, exists := existingKeys[entry.Key]; exists {
				return 0, 0, keyExistsError(entry.Key)
			}
		}
	}

	query := `INSERT INTO kv_entries(key, value, metadata_json, updated_at) VALUES(?, ?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, metadata_json = excluded.metadata_json, updated_at = excluded.updated_at`
	if mode == "create" {
		query = `INSERT OR IGNORE INTO kv_entries(key, value, metadata_json, updated_at) VALUES(?, ?, ?, ?)`
	}

	for _, entry := range entries {
		metadataJSON, err := encodeMetadata(entry.Metadata)
		if err != nil {
			return 0, 0, err
		}
		result, err := tx.ExecContext(
			ctx,
			query,
			entry.Key,
			entry.Value,
			metadataJSON,
			entry.UpdatedAt,
		)
		if err != nil {
			return 0, 0, err
		}
		if mode == "create" {
			rows, err := result.RowsAffected()
			if err != nil {
				return 0, 0, err
			}
			if rows == 0 {
				return 0, 0, keyExistsError(entry.Key)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return len(entries), len(entries) - len(existingKeys), nil
}

func (s *sqliteKVStore) ReplaceAll(ctx context.Context, entries []kvEntry, merge bool) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	if !merge {
		if _, err := tx.ExecContext(ctx, `DELETE FROM kv_entries`); err != nil {
			return 0, err
		}
	}
	for _, entry := range entries {
		metadataJSON, err := encodeMetadata(entry.Metadata)
		if err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO kv_entries(key, value, metadata_json, updated_at) VALUES(?, ?, ?, ?)
			 ON CONFLICT(key) DO UPDATE SET value = excluded.value, metadata_json = excluded.metadata_json, updated_at = excluded.updated_at`,
			entry.Key,
			entry.Value,
			metadataJSON,
			entry.UpdatedAt,
		); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(entries), nil
}

func (s *sqliteKVStore) Close() error {
	return s.db.Close()
}

type kvAdapter struct {
	store kvStore
}

func newKVAdapter(store kvStore) *kvAdapter {
	return &kvAdapter{store: store}
}

type getParams struct {
	Key         string      `json:"key"`
	Default     *string     `json:"default,omitempty"`
	TestControl testControl `json:"test_control,omitempty"`
}

type setParams struct {
	Key         string        `json:"key"`
	Value       string        `json:"value"`
	Mode        string        `json:"mode,omitempty"`
	Metadata    entryMetadata `json:"metadata,omitempty"`
	TestControl testControl   `json:"test_control,omitempty"`
}

type deleteParams struct {
	Key         string      `json:"key"`
	TestControl testControl `json:"test_control,omitempty"`
}

type listParams struct {
	Prefix       string      `json:"prefix,omitempty"`
	Limit        int         `json:"limit,omitempty"`
	IncludeValue bool        `json:"include_value,omitempty"`
	TestControl  testControl `json:"test_control,omitempty"`
}

type existsParams struct {
	Key         string      `json:"key"`
	TestControl testControl `json:"test_control,omitempty"`
}

type batchSetParams struct {
	Entries []struct {
		Key      string        `json:"key"`
		Value    string        `json:"value"`
		Metadata entryMetadata `json:"metadata,omitempty"`
	} `json:"entries"`
	Mode        string      `json:"mode,omitempty"`
	TestControl testControl `json:"test_control,omitempty"`
}

type exportParams struct {
	Format      string      `json:"format,omitempty"`
	IncludeMeta bool        `json:"include_metadata,omitempty"`
	TestControl testControl `json:"test_control,omitempty"`
}

type importParams struct {
	Entries []struct {
		Key      string        `json:"key"`
		Value    string        `json:"value"`
		Metadata entryMetadata `json:"metadata,omitempty"`
	} `json:"entries"`
	Mode        string      `json:"mode,omitempty"`
	TestControl testControl `json:"test_control,omitempty"`
}

type verifyParams struct {
	ExpectedCount *int        `json:"expected_count,omitempty"`
	TestControl   testControl `json:"test_control,omitempty"`
}

func main() {
	cfg := parseFlags()
	if err := run(cfg); err != nil {
		log.Fatal(err)
	}
}

func parseFlags() adapterConfig {
	cfg := adapterConfig{}
	flag.StringVar(&cfg.socketPath, "socket", filepath.Join(os.TempDir(), "pb-kv.sock"), "Unix socket path for primitive dispatch")
	flag.StringVar(&cfg.manifest, "manifest", "manifest.json", "Path to the adapter manifest JSON")
	flag.StringVar(&cfg.rpcEndpoint, "rpc-endpoint", "", "Sandbox-local PrimitiveBox HTTP endpoint used for app.register")
	flag.StringVar(&cfg.appID, "app-id", defaultAppID, "Override app_id for registered primitives")
	flag.StringVar(&cfg.namespace, "namespace", defaultNamespace, "Override primitive namespace prefix")
	flag.StringVar(&cfg.backend, "backend", "memory", "Storage backend: memory or sqlite")
	flag.StringVar(&cfg.dbPath, "db-path", "", "SQLite database path when backend=sqlite")
	flag.BoolVar(&cfg.noRegister, "no-register", false, "Skip app.register calls and only serve the Unix socket")
	flag.Parse()
	return cfg
}

func run(cfg adapterConfig) error {
	manifests, err := loadManifest(cfg.manifest, cfg.appID, cfg.namespace, cfg.socketPath)
	if err != nil {
		return err
	}

	store, err := newStore(cfg)
	if err != nil {
		return err
	}
	defer store.Close()

	adapter := newKVAdapter(store)
	listener, err := listenUnix(cfg.socketPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = listener.Close()
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- adapter.serve(ctx, listener)
	}()

	if !cfg.noRegister {
		if strings.TrimSpace(cfg.rpcEndpoint) == "" {
			return errors.New("--rpc-endpoint is required unless --no-register is set")
		}
		for _, manifest := range manifests {
			if err := registerPrimitive(ctx, cfg.rpcEndpoint, manifest); err != nil {
				return fmt.Errorf("register %s: %w", manifest.Name, err)
			}
		}
	}

	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			return err
		}
		return nil
	}
}

func newStore(cfg adapterConfig) (kvStore, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.backend)) {
	case "", "memory":
		return newMemoryKVStore(), nil
	case "sqlite":
		if cfg.dbPath == "" {
			cfg.dbPath = defaultSQLiteDBPath(cfg.socketPath)
		}
		return newSQLiteKVStore(cfg.dbPath)
	default:
		return nil, fmt.Errorf("unsupported backend %q", cfg.backend)
	}
}

func loadManifest(path, appID, namespace, socketPath string) ([]primitive.AppPrimitiveManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var manifests []primitive.AppPrimitiveManifest
	if err := json.Unmarshal(data, &manifests); err != nil {
		var wrapped manifestFile
		if err2 := json.Unmarshal(data, &wrapped); err2 != nil {
			return nil, fmt.Errorf("decode manifest: %w", err)
		}
		manifests = wrapped.Primitives
	}
	if len(manifests) == 0 {
		return nil, errors.New("manifest contains no primitives")
	}

	out := make([]primitive.AppPrimitiveManifest, 0, len(manifests))
	for _, manifest := range manifests {
		manifest.AppID = defaultString(strings.TrimSpace(appID), defaultString(strings.TrimSpace(manifest.AppID), defaultAppID))
		manifest.Name = qualifyName(namespace, manifest.Name)
		manifest.SocketPath = socketPath
		manifest.VerifyEndpoint = qualifyOptionalName(namespace, manifest.VerifyEndpoint)
		out = append(out, manifest)
	}
	return out, nil
}

func qualifyName(namespace, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return name
	}
	short := shortName(name)
	return defaultString(strings.TrimSpace(namespace), defaultNamespace) + "." + short
}

func qualifyOptionalName(namespace, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if strings.HasPrefix(name, "state.") {
		return name
	}
	return qualifyName(namespace, name)
}

func shortName(name string) string {
	if idx := strings.LastIndexByte(name, '.'); idx >= 0 && idx < len(name)-1 {
		return name[idx+1:]
	}
	return name
}

func listenUnix(socketPath string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return nil, err
	}
	_ = os.Remove(socketPath)
	return net.Listen("unix", socketPath)
}

func registerPrimitive(ctx context.Context, endpoint string, manifest primitive.AppPrimitiveManifest) error {
	body, err := json.Marshal(httpRPCRequest{
		JSONRPC: "2.0",
		Method:  "app.register",
		Params:  mustJSON(manifest),
		ID:      "register-" + manifest.Name,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(endpoint, "/")+"/rpc", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-PB-Origin", "sandbox")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var rpcResp httpRPCResponse
	if err := json.Unmarshal(raw, &rpcResp); err != nil {
		return fmt.Errorf("decode register response: %w", err)
	}
	if rpcResp.Error != nil {
		return fmt.Errorf("%s", rpcResp.Error.Message)
	}
	return nil
}

func (a *kvAdapter) serve(ctx context.Context, listener net.Listener) error {
	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
			}
			return err
		}
		go a.handleConn(ctx, conn)
	}
}

func (a *kvAdapter) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		_ = writeAppResponse(conn, appRPCResponse{
			ID:    0,
			Error: &appRPCError{Code: -32600, Message: "invalid request"},
		})
		return
	}

	var req appRPCRequest
	if err := json.Unmarshal(line, &req); err != nil {
		_ = writeAppResponse(conn, appRPCResponse{
			ID:    0,
			Error: &appRPCError{Code: -32600, Message: "invalid request"},
		})
		return
	}

	callCtx := ctx
	result, rpcErr := a.dispatch(callCtx, req.Method, req.Params)
	resp := appRPCResponse{ID: req.ID, Result: result}
	if rpcErr != nil {
		resp.Result = nil
		resp.Error = rpcErr
	}
	_ = writeAppResponse(conn, resp)
}

func writeAppResponse(w io.Writer, resp appRPCResponse) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
}

func (a *kvAdapter) dispatch(ctx context.Context, method string, raw json.RawMessage) (any, *appRPCError) {
	switch method {
	case "kv.get":
		return a.handleGet(ctx, raw)
	case "kv.set":
		return a.handleSet(ctx, raw)
	case "kv.delete":
		return a.handleDelete(ctx, raw)
	case "kv.list":
		return a.handleList(ctx, raw)
	case "kv.exists":
		return a.handleExists(ctx, raw)
	case "kv.batch_set":
		return a.handleBatchSet(ctx, raw)
	case "kv.export":
		return a.handleExport(ctx, raw)
	case "kv.import":
		return a.handleImport(ctx, raw)
	case "kv.verify":
		return a.handleVerify(ctx, raw)
	default:
		return nil, &appRPCError{Code: -32601, Message: "method not found: " + method}
	}
}

func (a *kvAdapter) handleGet(ctx context.Context, raw json.RawMessage) (any, *appRPCError) {
	var params getParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	if rpcErr := applyTestControl(ctx, params.TestControl); rpcErr != nil {
		return nil, rpcErr
	}
	if strings.TrimSpace(params.Key) == "" {
		return nil, &appRPCError{Code: -32602, Message: "key is required"}
	}
	entry, ok, err := a.store.Get(ctx, params.Key)
	if err != nil {
		return nil, internalRPCError(err)
	}
	if !ok {
		if params.Default != nil {
			return map[string]any{
				"found": false,
				"key":   params.Key,
				"value": *params.Default,
			}, nil
		}
		return nil, &appRPCError{Code: 4041, Message: "key not found: " + params.Key}
	}
	return map[string]any{
		"found":      true,
		"key":        entry.Key,
		"value":      entry.Value,
		"metadata":   entry.Metadata,
		"updated_at": entry.UpdatedAt,
	}, nil
}

func (a *kvAdapter) handleSet(ctx context.Context, raw json.RawMessage) (any, *appRPCError) {
	var params setParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	if rpcErr := applyTestControl(ctx, params.TestControl); rpcErr != nil {
		return nil, rpcErr
	}
	if strings.TrimSpace(params.Key) == "" {
		return nil, &appRPCError{Code: -32602, Message: "key is required"}
	}
	if params.Mode == "" {
		params.Mode = "upsert"
	}
	if params.Mode != "upsert" && params.Mode != "create" {
		return nil, &appRPCError{Code: -32602, Message: "mode must be one of: create, upsert"}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	entry := kvEntry{
		Key:       params.Key,
		Value:     params.Value,
		Metadata:  cloneMetadata(params.Metadata),
		UpdatedAt: now,
	}
	if params.Mode == "create" {
		if err := a.store.Create(ctx, entry); err != nil {
			if errors.Is(err, errKeyExists) {
				return nil, &appRPCError{Code: 4091, Message: err.Error()}
			}
			return nil, internalRPCError(err)
		}
		return map[string]any{
			"stored":          true,
			"key":             params.Key,
			"previous_exists": false,
			"previous_value":  "",
			"size_bytes":      len(params.Value),
			"value_sha256":    digestString(params.Value),
			"updated_at":      now,
		}, nil
	}

	prev, exists, err := a.store.Upsert(ctx, entry)
	if err != nil {
		return nil, internalRPCError(err)
	}
	return map[string]any{
		"stored":          true,
		"key":             params.Key,
		"previous_exists": exists,
		"previous_value":  prev.Value,
		"size_bytes":      len(params.Value),
		"value_sha256":    digestString(params.Value),
		"updated_at":      now,
	}, nil
}

func (a *kvAdapter) handleDelete(ctx context.Context, raw json.RawMessage) (any, *appRPCError) {
	var params deleteParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	if rpcErr := applyTestControl(ctx, params.TestControl); rpcErr != nil {
		return nil, rpcErr
	}
	if strings.TrimSpace(params.Key) == "" {
		return nil, &appRPCError{Code: -32602, Message: "key is required"}
	}
	prev, ok, err := a.store.Delete(ctx, params.Key)
	if err != nil {
		return nil, internalRPCError(err)
	}
	return map[string]any{
		"deleted":        ok,
		"key":            params.Key,
		"previous_value": prev.Value,
	}, nil
}

func (a *kvAdapter) handleList(ctx context.Context, raw json.RawMessage) (any, *appRPCError) {
	var params listParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	if rpcErr := applyTestControl(ctx, params.TestControl); rpcErr != nil {
		return nil, rpcErr
	}
	entries, err := a.store.List(ctx, params.Prefix, params.Limit)
	if err != nil {
		return nil, internalRPCError(err)
	}
	items := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		item := map[string]any{
			"key":        entry.Key,
			"metadata":   entry.Metadata,
			"updated_at": entry.UpdatedAt,
		}
		if params.IncludeValue {
			item["value"] = entry.Value
		}
		items = append(items, item)
	}
	return map[string]any{
		"entries": items,
		"count":   len(items),
	}, nil
}

func (a *kvAdapter) handleExists(ctx context.Context, raw json.RawMessage) (any, *appRPCError) {
	var params existsParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	if rpcErr := applyTestControl(ctx, params.TestControl); rpcErr != nil {
		return nil, rpcErr
	}
	if strings.TrimSpace(params.Key) == "" {
		return nil, &appRPCError{Code: -32602, Message: "key is required"}
	}
	_, ok, err := a.store.Get(ctx, params.Key)
	if err != nil {
		return nil, internalRPCError(err)
	}
	return map[string]any{
		"key":    params.Key,
		"exists": ok,
	}, nil
}

func (a *kvAdapter) handleBatchSet(ctx context.Context, raw json.RawMessage) (any, *appRPCError) {
	var params batchSetParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	if rpcErr := applyTestControl(ctx, params.TestControl); rpcErr != nil {
		return nil, rpcErr
	}
	if params.Mode == "" {
		params.Mode = "upsert"
	}
	if params.Mode != "upsert" && params.Mode != "create" {
		return nil, &appRPCError{Code: -32602, Message: "mode must be one of: create, upsert"}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	entries := make([]kvEntry, 0, len(params.Entries))
	for _, item := range params.Entries {
		entries = append(entries, kvEntry{
			Key:       item.Key,
			Value:     item.Value,
			Metadata:  cloneMetadata(item.Metadata),
			UpdatedAt: now,
		})
	}
	stored, created, err := a.store.BatchUpsert(ctx, entries, params.Mode)
	if err != nil {
		var rpcErr *appRPCError
		if errors.As(err, &rpcErr) {
			return nil, rpcErr
		}
		if errors.Is(err, errKeyExists) || errors.Is(err, errDuplicateBatchKey) {
			return nil, &appRPCError{Code: 4092, Message: err.Error()}
		}
		return nil, internalRPCError(err)
	}
	return map[string]any{
		"stored":       stored,
		"created":      created,
		"entry_count":  len(params.Entries),
		"completed_at": now,
	}, nil
}

func (a *kvAdapter) handleExport(ctx context.Context, raw json.RawMessage) (any, *appRPCError) {
	var params exportParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	if rpcErr := applyTestControl(ctx, params.TestControl); rpcErr != nil {
		return nil, rpcErr
	}
	if params.Format == "" {
		params.Format = "entries"
	}
	entries, err := a.store.List(ctx, "", 0)
	if err != nil {
		return nil, internalRPCError(err)
	}
	if params.Format == "map" {
		items := make(map[string]any, len(entries))
		for _, entry := range entries {
			if params.IncludeMeta {
				items[entry.Key] = map[string]any{
					"value":      entry.Value,
					"metadata":   entry.Metadata,
					"updated_at": entry.UpdatedAt,
				}
			} else {
				items[entry.Key] = entry.Value
			}
		}
		return map[string]any{
			"format":      "map",
			"entry_count": len(entries),
			"items":       items,
		}, nil
	}
	items := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		item := map[string]any{
			"key":   entry.Key,
			"value": entry.Value,
		}
		if params.IncludeMeta {
			item["metadata"] = entry.Metadata
			item["updated_at"] = entry.UpdatedAt
		}
		items = append(items, item)
	}
	return map[string]any{
		"format":      "entries",
		"entry_count": len(entries),
		"entries":     items,
	}, nil
}

func (a *kvAdapter) handleImport(ctx context.Context, raw json.RawMessage) (any, *appRPCError) {
	var params importParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	if rpcErr := applyTestControl(ctx, params.TestControl); rpcErr != nil {
		return nil, rpcErr
	}
	if params.Mode == "" {
		params.Mode = "replace"
	}
	if params.Mode != "replace" && params.Mode != "merge" {
		return nil, &appRPCError{Code: -32602, Message: "mode must be one of: replace, merge"}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	entries := make([]kvEntry, 0, len(params.Entries))
	for _, item := range params.Entries {
		if strings.TrimSpace(item.Key) == "" {
			return nil, &appRPCError{Code: -32602, Message: "entry key is required"}
		}
		entries = append(entries, kvEntry{
			Key:       item.Key,
			Value:     item.Value,
			Metadata:  cloneMetadata(item.Metadata),
			UpdatedAt: now,
		})
	}
	imported, err := a.store.ReplaceAll(ctx, entries, params.Mode == "merge")
	if err != nil {
		return nil, internalRPCError(err)
	}
	return map[string]any{
		"imported":     imported,
		"mode":         params.Mode,
		"completed_at": now,
	}, nil
}

func (a *kvAdapter) handleVerify(ctx context.Context, raw json.RawMessage) (any, *appRPCError) {
	var params verifyParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	if rpcErr := applyTestControl(ctx, params.TestControl); rpcErr != nil {
		return nil, rpcErr
	}
	entries, err := a.store.List(ctx, "", 0)
	if err != nil {
		return nil, internalRPCError(err)
	}
	checksum := digestEntries(entries)
	consistent := true
	var problems []string
	if params.ExpectedCount != nil && len(entries) != *params.ExpectedCount {
		consistent = false
		problems = append(problems, fmt.Sprintf("expected_count=%d actual=%d", *params.ExpectedCount, len(entries)))
	}
	return map[string]any{
		"consistent":  consistent,
		"entry_count": len(entries),
		"checksum":    checksum,
		"problems":    problems,
	}, nil
}

func decodeParams(raw json.RawMessage, target any) *appRPCError {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return &appRPCError{Code: -32602, Message: "invalid params: " + err.Error()}
	}
	return nil
}

func applyTestControl(ctx context.Context, control testControl) *appRPCError {
	if control.DelayMs > 0 {
		timer := time.NewTimer(time.Duration(control.DelayMs) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return &appRPCError{Code: -32098, Message: ctx.Err().Error()}
		case <-timer.C:
		}
	}
	if control.ErrorCode != 0 {
		msg := control.ErrorMessage
		if msg == "" {
			msg = "simulated adapter error"
		}
		return &appRPCError{Code: control.ErrorCode, Message: msg}
	}
	return nil
}

func internalRPCError(err error) *appRPCError {
	return &appRPCError{Code: -32603, Message: err.Error()}
}

func keyExistsError(key string) error {
	return fmt.Errorf("%w: %s", errKeyExists, key)
}

func duplicateBatchKeyError(key string) error {
	return fmt.Errorf("%w: %s", errDuplicateBatchKey, key)
}

func validateBatchEntries(entries []kvEntry) ([]string, error) {
	keys := make([]string, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if strings.TrimSpace(entry.Key) == "" {
			return nil, &appRPCError{Code: -32602, Message: "entry key is required"}
		}
		if _, ok := seen[entry.Key]; ok {
			return nil, duplicateBatchKeyError(entry.Key)
		}
		seen[entry.Key] = struct{}{}
		keys = append(keys, entry.Key)
	}
	return keys, nil
}

func cloneEntry(entry kvEntry) kvEntry {
	entry.Metadata = cloneMetadata(entry.Metadata)
	return entry
}

func cloneMetadata(metadata entryMetadata) entryMetadata {
	if metadata.Labels != nil {
		copied := make(map[string]string, len(metadata.Labels))
		for key, value := range metadata.Labels {
			copied[key] = value
		}
		metadata.Labels = copied
	}
	if metadata.Tags != nil {
		metadata.Tags = append([]string(nil), metadata.Tags...)
	}
	return metadata
}

func encodeMetadata(metadata entryMetadata) (string, error) {
	data, err := json.Marshal(metadata)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func decodeMetadata(raw string) (entryMetadata, error) {
	if strings.TrimSpace(raw) == "" {
		return entryMetadata{}, nil
	}
	var metadata entryMetadata
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return entryMetadata{}, err
	}
	return metadata, nil
}

func digestString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func defaultSQLiteDBPath(socketPath string) string {
	sum := sha256.Sum256([]byte(socketPath))
	return filepath.Join(os.TempDir(), "pb-kv-adapter-"+hex.EncodeToString(sum[:])+".sqlite")
}

func digestEntries(entries []kvEntry) string {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Key < entries[j].Key
	})
	data, _ := json.Marshal(entries)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func mustJSON(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}

func defaultString(primary, fallback string) string {
	if strings.TrimSpace(primary) != "" {
		return primary
	}
	return fallback
}

func lookupExistingKeys(ctx context.Context, queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, keys []string) (map[string]struct{}, error) {
	if len(keys) == 0 {
		return map[string]struct{}{}, nil
	}

	placeholders := make([]string, 0, len(keys))
	args := make([]any, 0, len(keys))
	for _, key := range keys {
		placeholders = append(placeholders, "?")
		args = append(args, key)
	}

	rows, err := queryer.QueryContext(
		ctx,
		`SELECT key FROM kv_entries WHERE key IN (`+strings.Join(placeholders, ",")+`)`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	existing := make(map[string]struct{}, len(keys))
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		existing[key] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return existing, nil
}
