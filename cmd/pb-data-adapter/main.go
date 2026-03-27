// pb-data-adapter is a minimal SQLite-backed adapter that exposes data.*
// primitives to the PrimitiveBox runtime. It is the reference example for the
// Phase 4 Package Manager and doubles as a boilerplate template for app authors.
//
// Primitives exposed:
//   - data.schema  – list tables and columns (query/low)
//   - data.query   – execute a SELECT statement (query/low, ui_layout_hint: table)
//   - data.insert  – insert a row via parameterised query (mutation/high/irreversible)
//
// The adapter follows the same boilerplate as pb-os-adapter:
//
//	--socket, --rpc-endpoint, --app-id, --no-register, --db flags
package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
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
	"strings"
	"syscall"
	"time"

	"primitivebox/internal/cvr"
	"primitivebox/internal/primitive"

	_ "modernc.org/sqlite"
)

const defaultAppID = "pb-data-adapter"

// ---------------------------------------------------------------------------
// Config and wire types
// ---------------------------------------------------------------------------

type adapterConfig struct {
	socketPath  string
	rpcEndpoint string
	appID       string
	dbPath      string
	noRegister  bool
}

type adapterState struct {
	db *sql.DB
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
	} `json:"error,omitempty"`
	ID any `json:"id"`
}

// ---------------------------------------------------------------------------
// Entry point
// ---------------------------------------------------------------------------

func main() {
	cfg := parseFlags()
	if err := run(cfg); err != nil {
		log.Fatal(err)
	}
}

func parseFlags() adapterConfig {
	cfg := adapterConfig{}
	flag.StringVar(&cfg.socketPath, "socket", filepath.Join(os.TempDir(), "pb-data.sock"),
		"Unix socket path for data primitive dispatch")
	flag.StringVar(&cfg.rpcEndpoint, "rpc-endpoint", "",
		"PrimitiveBox HTTP endpoint used for app.register")
	flag.StringVar(&cfg.appID, "app-id", defaultAppID,
		"Override app_id for registered primitives")
	flag.StringVar(&cfg.dbPath, "db", filepath.Join(os.TempDir(), "pb-data.db"),
		"Path to the SQLite database file")
	flag.BoolVar(&cfg.noRegister, "no-register", false,
		"Skip app.register calls and only serve the Unix socket")
	flag.Parse()
	return cfg
}

func run(cfg adapterConfig) error {
	// Open (or create) the SQLite database.
	db, err := sql.Open("sqlite", cfg.dbPath)
	if err != nil {
		return fmt.Errorf("open database %q: %w", cfg.dbPath, err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}

	listener, err := listenUnix(cfg.socketPath)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()

	state := &adapterState{db: db}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- serve(ctx, listener, state)
	}()

	if !cfg.noRegister {
		if strings.TrimSpace(cfg.rpcEndpoint) == "" {
			return errors.New("--rpc-endpoint is required unless --no-register is set")
		}
		for _, manifest := range buildManifestSet(cfg.appID, cfg.socketPath) {
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

// ---------------------------------------------------------------------------
// Manifest set
// ---------------------------------------------------------------------------

func buildManifestSet(appID, socketPath string) []primitive.AppPrimitiveManifest {
	queryLow := cvr.PrimitiveIntent{
		Category:       cvr.IntentQuery,
		Reversible:     true,
		RiskLevel:      cvr.RiskLow,
		AffectedScopes: []string{"app:data"},
	}
	mutationHigh := cvr.PrimitiveIntent{
		Category:       cvr.IntentMutation,
		Reversible:     false,
		RiskLevel:      cvr.RiskHigh,
		AffectedScopes: []string{"app:data"},
	}

	return []primitive.AppPrimitiveManifest{
		{
			AppID:       appID,
			Name:        "data.schema",
			Description: "List tables and their columns in the connected SQLite database.",
			InputSchema: mustJSON(map[string]any{
				"type":                 "object",
				"additionalProperties": false,
			}),
			OutputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tables": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"name":    map[string]any{"type": "string"},
								"columns": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
							},
							"required":             []string{"name", "columns"},
							"additionalProperties": false,
						},
					},
				},
				"required":             []string{"tables"},
				"additionalProperties": false,
			}),
			SocketPath: socketPath,
			Intent:     queryLow,
		},
		{
			AppID:        appID,
			Name:         "data.query",
			Description:  "Execute a read-only SELECT statement. Returns rows as an array of objects.",
			UILayoutHint: "table",
			InputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"sql":  map[string]any{"type": "string", "description": "A SELECT statement"},
					"args": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Positional bind args"},
				},
				"required":             []string{"sql"},
				"additionalProperties": false,
			}),
			OutputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"columns": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"rows": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "array"},
					},
					"row_count": map[string]any{"type": "integer"},
				},
				"required":             []string{"columns", "rows", "row_count"},
				"additionalProperties": false,
			}),
			SocketPath: socketPath,
			Intent:     queryLow,
		},
		{
			AppID:       appID,
			Name:        "data.insert",
			Description: "Insert a row into a table. Irreversible — CVR auto-checkpoints before execution.",
			InputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"table":  map[string]any{"type": "string", "description": "Target table name"},
					"values": map[string]any{"type": "object", "description": "Column → value pairs", "additionalProperties": true},
				},
				"required":             []string{"table", "values"},
				"additionalProperties": false,
			}),
			OutputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"inserted":       map[string]any{"type": "boolean"},
					"last_insert_id": map[string]any{"type": "integer"},
					"rows_affected":  map[string]any{"type": "integer"},
				},
				"required":             []string{"inserted", "last_insert_id", "rows_affected"},
				"additionalProperties": false,
			}),
			SocketPath: socketPath,
			Intent:     mutationHigh,
		},
	}
}

// ---------------------------------------------------------------------------
// Network layer
// ---------------------------------------------------------------------------

func listenUnix(socketPath string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return nil, err
	}
	_ = os.Remove(socketPath)
	return net.Listen("unix", socketPath)
}

func serve(ctx context.Context, listener net.Listener, state *adapterState) error {
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
		go handleConn(ctx, conn, state)
	}
}

func handleConn(ctx context.Context, conn net.Conn, state *adapterState) {
	defer conn.Close()
	var req appRPCRequest
	defer func() {
		if recovered := recover(); recovered != nil {
			_ = writeAppResponse(conn, appRPCResponse{
				ID:    req.ID,
				Error: &appRPCError{Code: -32603, Message: fmt.Sprintf("internal adapter error: %v", recovered)},
			})
		}
	}()

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		_ = writeAppResponse(conn, appRPCResponse{
			ID:    0,
			Error: &appRPCError{Code: -32600, Message: "invalid request"},
		})
		return
	}

	if err := json.Unmarshal(line, &req); err != nil {
		_ = writeAppResponse(conn, appRPCResponse{
			ID:    0,
			Error: &appRPCError{Code: -32600, Message: "invalid request"},
		})
		return
	}

	result, rpcErr := dispatch(ctx, state, req.Method, req.Params)
	resp := appRPCResponse{ID: req.ID, Result: result}
	if rpcErr != nil {
		resp.Result = nil
		resp.Error = rpcErr
	}
	_ = writeAppResponse(conn, resp)
}

// ---------------------------------------------------------------------------
// Dispatch
// ---------------------------------------------------------------------------

func dispatch(ctx context.Context, state *adapterState, method string, raw json.RawMessage) (any, *appRPCError) {
	switch method {
	case "data.schema":
		return handleDataSchema(ctx, state.db)
	case "data.query":
		var in struct {
			SQL  string   `json:"sql"`
			Args []string `json:"args"`
		}
		if err := json.Unmarshal(orEmpty(raw), &in); err != nil {
			return nil, invalidParams("invalid params")
		}
		return handleDataQuery(ctx, state.db, in.SQL, in.Args)
	case "data.insert":
		var in struct {
			Table  string         `json:"table"`
			Values map[string]any `json:"values"`
		}
		if err := json.Unmarshal(orEmpty(raw), &in); err != nil {
			return nil, invalidParams("invalid params")
		}
		return handleDataInsert(ctx, state.db, in.Table, in.Values)
	default:
		return nil, &appRPCError{Code: -32601, Message: "method not found: " + method}
	}
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

func handleDataSchema(ctx context.Context, db *sql.DB) (any, *appRPCError) {
	rows, err := db.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")
	if err != nil {
		return nil, internalErr("list tables: " + err.Error())
	}
	defer rows.Close()

	type tableInfo struct {
		Name    string   `json:"name"`
		Columns []string `json:"columns"`
	}

	var tables []tableInfo
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			continue
		}
		cols, err := tableColumns(ctx, db, name)
		if err != nil {
			cols = []string{}
		}
		tables = append(tables, tableInfo{Name: name, Columns: cols})
	}
	if tables == nil {
		tables = []tableInfo{}
	}
	return map[string]any{"tables": tables}, nil
}

func tableColumns(ctx context.Context, db *sql.DB, table string) ([]string, error) {
	// PRAGMA table_info is safe for any table name obtained from sqlite_master.
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+quoteIdentifier(table)+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	// PRAGMA table_info columns: cid, name, type, notnull, dflt_value, pk
	nameIdx := -1
	for i, c := range cols {
		if c == "name" {
			nameIdx = i
			break
		}
	}
	if nameIdx < 0 {
		return nil, errors.New("unexpected PRAGMA result")
	}

	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}

	var names []string
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			continue
		}
		if s, ok := vals[nameIdx].(string); ok {
			names = append(names, s)
		}
	}
	return names, rows.Err()
}

func handleDataQuery(ctx context.Context, db *sql.DB, query string, args []string) (any, *appRPCError) {
	if strings.TrimSpace(query) == "" {
		return nil, invalidParams("sql is required")
	}

	// Enforce read-only: only SELECT is allowed.
	normalized := strings.ToUpper(strings.TrimSpace(query))
	if !strings.HasPrefix(normalized, "SELECT") {
		return nil, invalidParams("data.query only allows SELECT statements")
	}

	iArgs := make([]any, len(args))
	for i, a := range args {
		iArgs[i] = a
	}

	rows, err := db.QueryContext(ctx, query, iArgs...)
	if err != nil {
		return nil, internalErr("query: " + err.Error())
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, internalErr("columns: " + err.Error())
	}

	vals := make([]any, len(columns))
	ptrs := make([]any, len(columns))
	for i := range vals {
		ptrs[i] = &vals[i]
	}

	var resultRows [][]any
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return nil, internalErr("scan: " + err.Error())
		}
		row := make([]any, len(columns))
		copy(row, vals)
		resultRows = append(resultRows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, internalErr("rows: " + err.Error())
	}
	if resultRows == nil {
		resultRows = [][]any{}
	}

	return map[string]any{
		"columns":   columns,
		"rows":      resultRows,
		"row_count": len(resultRows),
	}, nil
}

func handleDataInsert(ctx context.Context, db *sql.DB, table string, values map[string]any) (any, *appRPCError) {
	if strings.TrimSpace(table) == "" {
		return nil, invalidParams("table is required")
	}
	if len(values) == 0 {
		return nil, invalidParams("values must not be empty")
	}

	// Build parameterised INSERT.
	cols := make([]string, 0, len(values))
	placeholders := make([]string, 0, len(values))
	args := make([]any, 0, len(values))
	for col, val := range values {
		cols = append(cols, quoteIdentifier(col))
		placeholders = append(placeholders, "?")
		args = append(args, val)
	}

	stmt := fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES (%s)",
		quoteIdentifier(table),
		strings.Join(cols, ", "),
		strings.Join(placeholders, ", "),
	)

	result, err := db.ExecContext(ctx, stmt, args...)
	if err != nil {
		return nil, internalErr("insert: " + err.Error())
	}
	lastID, _ := result.LastInsertId()
	affected, _ := result.RowsAffected()

	return map[string]any{
		"inserted":       true,
		"last_insert_id": lastID,
		"rows_affected":  affected,
	}, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// quoteIdentifier wraps an identifier in double-quotes, escaping embedded
// double-quotes. This is safe for table/column names obtained from sqlite_master
// or user input before constructing SQL.
func quoteIdentifier(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func orEmpty(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("{}")
	}
	return raw
}

func invalidParams(msg string) *appRPCError {
	return &appRPCError{Code: -32602, Message: msg}
}

func internalErr(msg string) *appRPCError {
	return &appRPCError{Code: -32603, Message: msg}
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func writeAppResponse(w io.Writer, resp appRPCResponse) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(endpoint, "/")+"/rpc", bytes.NewReader(body))
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
		return fmt.Errorf("app.register error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	return nil
}
