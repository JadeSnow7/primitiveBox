package primitive

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"primitivebox/internal/eventing"

	_ "github.com/lib/pq"
	"github.com/xwb1989/sqlparser"
	_ "modernc.org/sqlite"
)

const maxReadonlyRows = 100

type DBConnectionConfig struct {
	Dialect string `json:"dialect"`
	Path    string `json:"path,omitempty"`
	DSN     string `json:"dsn,omitempty"`
}

type dbSchemaParams struct {
	Connection DBConnectionConfig `json:"connection"`
}

type dbQueryReadonlyParams struct {
	Connection DBConnectionConfig `json:"connection"`
	Query      string             `json:"query"`
	MaxRows    int                `json:"max_rows,omitempty"`
}

type dbSchemaResult struct {
	Dialect string         `json:"dialect"`
	Tables  []dbTableInfo  `json:"tables"`
	Indexes []dbIndexInfo  `json:"indexes"`
}

type dbTableInfo struct {
	Schema  string         `json:"schema,omitempty"`
	Name    string         `json:"name"`
	DDL     string         `json:"ddl,omitempty"`
	Columns []dbColumnInfo `json:"columns"`
}

type dbColumnInfo struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Nullable   bool   `json:"nullable"`
	Default    string `json:"default,omitempty"`
	PrimaryKey bool   `json:"primary_key,omitempty"`
}

type dbIndexInfo struct {
	Schema   string   `json:"schema,omitempty"`
	Table    string   `json:"table"`
	Name     string   `json:"name"`
	Columns  []string `json:"columns,omitempty"`
	Unique   bool     `json:"unique"`
	DDL      string   `json:"ddl,omitempty"`
	Primary  bool     `json:"primary,omitempty"`
}

type dbQueryResult struct {
	Dialect   string                   `json:"dialect"`
	Columns   []dbResultColumn         `json:"columns"`
	Rows      []map[string]any         `json:"rows"`
	RowCount  int                      `json:"row_count"`
	Limited   bool                     `json:"limited"`
	Query     string                   `json:"query"`
}

type dbResultColumn struct {
	Name     string `json:"name"`
	Database string `json:"database_type,omitempty"`
	Nullable bool   `json:"nullable"`
}

type dbSchemaPrimitive struct {
	workspaceDir string
	resolver     workspacePathResolver
}

type dbQueryReadonlyPrimitive struct {
	workspaceDir string
	resolver     workspacePathResolver
}

func NewDBSchema(workspaceDir string) Primitive {
	return &dbSchemaPrimitive{
		workspaceDir: workspaceDir,
		resolver:     newWorkspacePathResolver(workspaceDir),
	}
}

func NewDBQueryReadonly(workspaceDir string) Primitive {
	return &dbQueryReadonlyPrimitive{
		workspaceDir: workspaceDir,
		resolver:     newWorkspacePathResolver(workspaceDir),
	}
}

func (p *dbSchemaPrimitive) Name() string     { return "db.schema" }
func (p *dbSchemaPrimitive) Category() string { return "db" }

func (p *dbSchemaPrimitive) Schema() Schema {
	return Schema{
		Name:        p.Name(),
		Description: "Inspect database tables, columns, and indexes for a sandbox-local read-only connection.",
		Input: json.RawMessage(`{
			"type":"object",
			"properties":{
				"connection":{
					"type":"object",
					"properties":{
						"dialect":{"type":"string","enum":["sqlite","postgres"]},
						"path":{"type":"string"},
						"dsn":{"type":"string"}
					},
					"required":["dialect"]
				}
			},
			"required":["connection"]
		}`),
		Output: json.RawMessage(`{
			"type":"object",
			"properties":{
				"dialect":{"type":"string"},
				"tables":{"type":"array"},
				"indexes":{"type":"array"}
			},
			"required":["dialect","tables","indexes"]
		}`),
	}
}

func (p *dbSchemaPrimitive) Execute(ctx context.Context, params json.RawMessage) (Result, error) {
	var input dbSchemaParams
	if err := json.Unmarshal(params, &input); err != nil {
		return Result{}, &PrimitiveError{Code: ErrValidation, Message: "invalid params: " + err.Error()}
	}
	start := time.Now()
	db, dialect, err := p.openDB(ctx, input.Connection)
	if err != nil {
		return Result{}, err
	}
	defer db.Close()

	emitDBProgress(ctx, p.Name(), "schema_started", map[string]any{
		"dialect": dialect,
		"target":  "schema",
	})

	var schema dbSchemaResult
	switch dialect {
	case "sqlite":
		schema, err = introspectSQLiteSchema(ctx, db)
	case "postgres":
		schema, err = introspectPostgresSchema(ctx, db)
	default:
		err = &PrimitiveError{Code: ErrValidation, Message: "unsupported database dialect"}
	}
	if err != nil {
		return Result{}, err
	}
	schema.Dialect = dialect
	emitDBProgress(ctx, p.Name(), "schema_completed", map[string]any{
		"dialect":     dialect,
		"target":      "schema",
		"table_count": len(schema.Tables),
		"index_count": len(schema.Indexes),
		"duration_ms": time.Since(start).Milliseconds(),
	})
	return Result{
		Data:     schema,
		Duration: time.Since(start).Milliseconds(),
	}, nil
}

func (p *dbQueryReadonlyPrimitive) Name() string     { return "db.query_readonly" }
func (p *dbQueryReadonlyPrimitive) Category() string { return "db" }

func (p *dbQueryReadonlyPrimitive) Schema() Schema {
	return Schema{
		Name:        p.Name(),
		Description: "Run a single read-only SQL query with a strict row cap.",
		Input: json.RawMessage(`{
			"type":"object",
			"properties":{
				"connection":{
					"type":"object",
					"properties":{
						"dialect":{"type":"string","enum":["sqlite","postgres"]},
						"path":{"type":"string"},
						"dsn":{"type":"string"}
					},
					"required":["dialect"]
				},
				"query":{"type":"string"},
				"max_rows":{"type":"integer","minimum":1,"maximum":100}
			},
			"required":["connection","query"]
		}`),
		Output: json.RawMessage(`{
			"type":"object",
			"properties":{
				"dialect":{"type":"string"},
				"columns":{"type":"array"},
				"rows":{"type":"array"},
				"row_count":{"type":"integer"},
				"limited":{"type":"boolean"},
				"query":{"type":"string"}
			},
			"required":["dialect","columns","rows","row_count","limited","query"]
		}`),
	}
}

func (p *dbQueryReadonlyPrimitive) Execute(ctx context.Context, params json.RawMessage) (Result, error) {
	var input dbQueryReadonlyParams
	if err := json.Unmarshal(params, &input); err != nil {
		return Result{}, &PrimitiveError{Code: ErrValidation, Message: "invalid params: " + err.Error()}
	}
	if strings.TrimSpace(input.Query) == "" {
		return Result{}, &PrimitiveError{Code: ErrValidation, Message: "query is required"}
	}
	start := time.Now()
	db, dialect, err := p.openDB(ctx, input.Connection)
	if err != nil {
		return Result{}, err
	}
	defer db.Close()

	capRows, normalized, err := normalizeReadonlyQuery(input.Query, input.MaxRows)
	if err != nil {
		return Result{}, err
	}
	execQuery := fmt.Sprintf("SELECT * FROM (%s) AS pb_readonly LIMIT %d", normalized, capRows+1)
	emitDBProgress(ctx, p.Name(), "query_started", map[string]any{
		"dialect":  dialect,
		"target":   "query",
		"max_rows": capRows,
	})

	rows, err := db.QueryContext(ctx, execQuery)
	if err != nil {
		return Result{}, &PrimitiveError{Code: ErrExecution, Message: err.Error()}
	}
	defer rows.Close()

	columns, err := rows.ColumnTypes()
	if err != nil {
		return Result{}, &PrimitiveError{Code: ErrExecution, Message: err.Error()}
	}
	columnDefs := make([]dbResultColumn, 0, len(columns))
	for _, column := range columns {
		nullable, ok := column.Nullable()
		columnDefs = append(columnDefs, dbResultColumn{
			Name:     column.Name(),
			Database: column.DatabaseTypeName(),
			Nullable: ok && nullable,
		})
	}

	resultRows := make([]map[string]any, 0, capRows)
	limited := false
	for rows.Next() {
		record, err := scanRow(columns, rows)
		if err != nil {
			return Result{}, &PrimitiveError{Code: ErrExecution, Message: err.Error()}
		}
		if len(resultRows) == capRows {
			limited = true
			break
		}
		resultRows = append(resultRows, record)
	}
	if err := rows.Err(); err != nil {
		return Result{}, &PrimitiveError{Code: ErrExecution, Message: err.Error()}
	}

	result := dbQueryResult{
		Dialect:  dialect,
		Columns:  columnDefs,
		Rows:     resultRows,
		RowCount: len(resultRows),
		Limited:  limited,
		Query:    normalized,
	}
	emitDBProgress(ctx, p.Name(), "query_completed", map[string]any{
		"dialect":     dialect,
		"target":      "query",
		"row_count":   result.RowCount,
		"limited":     result.Limited,
		"duration_ms": time.Since(start).Milliseconds(),
	})
	return Result{
		Data:     result,
		Duration: time.Since(start).Milliseconds(),
	}, nil
}

func (p *dbSchemaPrimitive) openDB(ctx context.Context, config DBConnectionConfig) (*sql.DB, string, error) {
	return openDBConnection(ctx, p.resolver, config)
}

func (p *dbQueryReadonlyPrimitive) openDB(ctx context.Context, config DBConnectionConfig) (*sql.DB, string, error) {
	return openDBConnection(ctx, p.resolver, config)
}

func openDBConnection(ctx context.Context, resolver workspacePathResolver, config DBConnectionConfig) (*sql.DB, string, error) {
	switch strings.ToLower(strings.TrimSpace(config.Dialect)) {
	case "sqlite":
		if strings.TrimSpace(config.Path) == "" {
			return nil, "", &PrimitiveError{Code: ErrValidation, Message: "sqlite connection requires path"}
		}
		resolved, err := resolver.Resolve(config.Path)
		if err != nil {
			return nil, "", err
		}
		db, err := sql.Open("sqlite", resolved)
		if err != nil {
			return nil, "", &PrimitiveError{Code: ErrExecution, Message: err.Error()}
		}
		if err := db.PingContext(ctx); err != nil {
			_ = db.Close()
			return nil, "", &PrimitiveError{Code: ErrExecution, Message: err.Error()}
		}
		return db, "sqlite", nil
	case "postgres":
		if strings.TrimSpace(config.DSN) == "" {
			return nil, "", &PrimitiveError{Code: ErrValidation, Message: "postgres connection requires dsn"}
		}
		db, err := sql.Open("postgres", config.DSN)
		if err != nil {
			return nil, "", &PrimitiveError{Code: ErrExecution, Message: err.Error()}
		}
		if err := db.PingContext(ctx); err != nil {
			_ = db.Close()
			return nil, "", &PrimitiveError{Code: ErrExecution, Message: err.Error()}
		}
		return db, "postgres", nil
	default:
		return nil, "", &PrimitiveError{Code: ErrValidation, Message: "dialect must be sqlite or postgres"}
	}
}

func normalizeReadonlyQuery(query string, requestedRows int) (int, string, error) {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return 0, "", &PrimitiveError{Code: ErrValidation, Message: "query is required"}
	}
	if strings.Contains(trimmed, ";") {
		if !strings.HasSuffix(trimmed, ";") || strings.Contains(strings.TrimSuffix(trimmed, ";"), ";") {
			return 0, "", &PrimitiveError{Code: ErrPermission, Message: "multiple SQL statements are not allowed"}
		}
		trimmed = strings.TrimSpace(strings.TrimSuffix(trimmed, ";"))
	}
	stmt, err := sqlparser.Parse(trimmed)
	if err != nil {
		return 0, "", &PrimitiveError{Code: ErrPermission, Message: "query must be a single read-only SELECT statement"}
	}
	switch stmt.(type) {
	case *sqlparser.Select, *sqlparser.Union:
	default:
		return 0, "", &PrimitiveError{Code: ErrPermission, Message: "query must be a read-only SELECT statement"}
	}
	maxRows := requestedRows
	if maxRows <= 0 || maxRows > maxReadonlyRows {
		maxRows = maxReadonlyRows
	}
	return maxRows, trimmed, nil
}

func scanRow(columns []*sql.ColumnType, rows *sql.Rows) (map[string]any, error) {
	values := make([]any, len(columns))
	targets := make([]any, len(columns))
	for i := range values {
		targets[i] = &values[i]
	}
	if err := rows.Scan(targets...); err != nil {
		return nil, err
	}
	record := make(map[string]any, len(columns))
	for i, column := range columns {
		record[column.Name()] = normalizeSQLValue(values[i])
	}
	return record, nil
}

func normalizeSQLValue(value any) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case []byte:
		return string(typed)
	case time.Time:
		return typed.UTC().Format(time.RFC3339Nano)
	default:
		return typed
	}
}

func introspectSQLiteSchema(ctx context.Context, db *sql.DB) (dbSchemaResult, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT type, name, tbl_name, sql
		FROM sqlite_master
		WHERE type IN ('table', 'index')
		  AND name NOT LIKE 'sqlite_%'
		ORDER BY type, name
	`)
	if err != nil {
		return dbSchemaResult{}, &PrimitiveError{Code: ErrExecution, Message: err.Error()}
	}
	defer rows.Close()

	result := dbSchemaResult{Tables: []dbTableInfo{}, Indexes: []dbIndexInfo{}}
	for rows.Next() {
		var kind, name, tableName, ddl string
		if err := rows.Scan(&kind, &name, &tableName, &ddl); err != nil {
			return dbSchemaResult{}, &PrimitiveError{Code: ErrExecution, Message: err.Error()}
		}
		switch kind {
		case "table":
			columns, err := introspectSQLiteColumns(ctx, db, name)
			if err != nil {
				return dbSchemaResult{}, err
			}
			result.Tables = append(result.Tables, dbTableInfo{Name: name, DDL: ddl, Columns: columns})
		case "index":
			indexInfo, err := introspectSQLiteIndex(ctx, db, tableName, name, ddl)
			if err != nil {
				return dbSchemaResult{}, err
			}
			result.Indexes = append(result.Indexes, indexInfo)
		}
	}
	return result, rows.Err()
}

func introspectSQLiteColumns(ctx context.Context, db *sql.DB, table string) ([]dbColumnInfo, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", sqlparser.String(sqlparser.NewTableIdent(table))))
	if err != nil {
		return nil, &PrimitiveError{Code: ErrExecution, Message: err.Error()}
	}
	defer rows.Close()

	var columns []dbColumnInfo
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultValue, &pk); err != nil {
			return nil, &PrimitiveError{Code: ErrExecution, Message: err.Error()}
		}
		columns = append(columns, dbColumnInfo{
			Name:       name,
			Type:       colType,
			Nullable:   notNull == 0,
			Default:    defaultValue.String,
			PrimaryKey: pk > 0,
		})
	}
	return columns, rows.Err()
}

func introspectSQLiteIndex(ctx context.Context, db *sql.DB, tableName, indexName, ddl string) (dbIndexInfo, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA index_info(%s)", sqlparser.String(sqlparser.NewTableIdent(indexName))))
	if err != nil {
		return dbIndexInfo{}, &PrimitiveError{Code: ErrExecution, Message: err.Error()}
	}
	defer rows.Close()

	columns := []string{}
	for rows.Next() {
		var seqno, cid int
		var name string
		if err := rows.Scan(&seqno, &cid, &name); err != nil {
			return dbIndexInfo{}, &PrimitiveError{Code: ErrExecution, Message: err.Error()}
		}
		columns = append(columns, name)
	}
	return dbIndexInfo{
		Table:   tableName,
		Name:    indexName,
		Columns: columns,
		DDL:     ddl,
		Unique:  strings.Contains(strings.ToUpper(ddl), "UNIQUE"),
	}, rows.Err()
}

func introspectPostgresSchema(ctx context.Context, db *sql.DB) (dbSchemaResult, error) {
	tableRows, err := db.QueryContext(ctx, `
		SELECT table_schema, table_name
		FROM information_schema.tables
		WHERE table_type='BASE TABLE'
		  AND table_schema NOT IN ('pg_catalog', 'information_schema')
		ORDER BY table_schema, table_name
	`)
	if err != nil {
		return dbSchemaResult{}, &PrimitiveError{Code: ErrExecution, Message: err.Error()}
	}
	defer tableRows.Close()

	result := dbSchemaResult{Tables: []dbTableInfo{}, Indexes: []dbIndexInfo{}}
	for tableRows.Next() {
		var schemaName, tableName string
		if err := tableRows.Scan(&schemaName, &tableName); err != nil {
			return dbSchemaResult{}, &PrimitiveError{Code: ErrExecution, Message: err.Error()}
		}
		columns, err := introspectPostgresColumns(ctx, db, schemaName, tableName)
		if err != nil {
			return dbSchemaResult{}, err
		}
		result.Tables = append(result.Tables, dbTableInfo{
			Schema:  schemaName,
			Name:    tableName,
			DDL:     synthesizePostgresDDL(schemaName, tableName, columns),
			Columns: columns,
		})
	}
	if err := tableRows.Err(); err != nil {
		return dbSchemaResult{}, err
	}

	indexRows, err := db.QueryContext(ctx, `
		SELECT schemaname, tablename, indexname, indexdef
		FROM pg_indexes
		WHERE schemaname NOT IN ('pg_catalog', 'information_schema')
		ORDER BY schemaname, tablename, indexname
	`)
	if err != nil {
		return dbSchemaResult{}, &PrimitiveError{Code: ErrExecution, Message: err.Error()}
	}
	defer indexRows.Close()
	for indexRows.Next() {
		var schemaName, tableName, indexName, indexDDL string
		if err := indexRows.Scan(&schemaName, &tableName, &indexName, &indexDDL); err != nil {
			return dbSchemaResult{}, &PrimitiveError{Code: ErrExecution, Message: err.Error()}
		}
		result.Indexes = append(result.Indexes, dbIndexInfo{
			Schema:  schemaName,
			Table:   tableName,
			Name:    indexName,
			DDL:     indexDDL,
			Unique:  strings.Contains(strings.ToUpper(indexDDL), "UNIQUE"),
			Primary: strings.Contains(strings.ToUpper(indexDDL), "PRIMARY KEY"),
		})
	}
	return result, indexRows.Err()
}

func introspectPostgresColumns(ctx context.Context, db *sql.DB, schemaName, tableName string) ([]dbColumnInfo, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT column_name, data_type, is_nullable, COALESCE(column_default, ''), ordinal_position
		FROM information_schema.columns
		WHERE table_schema = $1 AND table_name = $2
		ORDER BY ordinal_position
	`, schemaName, tableName)
	if err != nil {
		return nil, &PrimitiveError{Code: ErrExecution, Message: err.Error()}
	}
	defer rows.Close()

	primaryKeys, err := postgresPrimaryKeys(ctx, db, schemaName, tableName)
	if err != nil {
		return nil, err
	}

	var columns []dbColumnInfo
	for rows.Next() {
		var name, dataType, isNullable, defaultValue string
		var ordinal int
		if err := rows.Scan(&name, &dataType, &isNullable, &defaultValue, &ordinal); err != nil {
			return nil, &PrimitiveError{Code: ErrExecution, Message: err.Error()}
		}
		columns = append(columns, dbColumnInfo{
			Name:       name,
			Type:       dataType,
			Nullable:   isNullable == "YES",
			Default:    defaultValue,
			PrimaryKey: primaryKeys[name],
		})
	}
	return columns, rows.Err()
}

func postgresPrimaryKeys(ctx context.Context, db *sql.DB, schemaName, tableName string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT a.attname
		FROM pg_index i
		JOIN pg_class c ON c.oid = i.indrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = ANY(i.indkey)
		WHERE i.indisprimary = TRUE AND n.nspname = $1 AND c.relname = $2
	`, schemaName, tableName)
	if err != nil {
		return nil, &PrimitiveError{Code: ErrExecution, Message: err.Error()}
	}
	defer rows.Close()
	keys := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, &PrimitiveError{Code: ErrExecution, Message: err.Error()}
		}
		keys[name] = true
	}
	return keys, rows.Err()
}

func synthesizePostgresDDL(schemaName, tableName string, columns []dbColumnInfo) string {
	parts := make([]string, 0, len(columns))
	for _, column := range columns {
		def := fmt.Sprintf("%s %s", column.Name, column.Type)
		if !column.Nullable {
			def += " NOT NULL"
		}
		if column.Default != "" {
			def += " DEFAULT " + column.Default
		}
		parts = append(parts, def)
	}
	return fmt.Sprintf("CREATE TABLE %s.%s (%s);", schemaName, tableName, strings.Join(parts, ", "))
}

func emitDBProgress(ctx context.Context, method, message string, payload map[string]any) {
	eventing.Emit(ctx, eventing.Event{
		Type:    "db.progress",
		Source:  "primitive",
		Method:  method,
		Message: message,
		Data:    eventing.MustJSON(payload),
	})
}
