package primitive

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

func TestDBQueryRejectsMultipleStatements(t *testing.T) {
	t.Parallel()

	if _, _, err := normalizeReadonlyQuery("select * from widgets; drop table widgets;", 100); err == nil {
		t.Fatalf("expected multiple statements to be rejected")
	}
}

func TestDBSchemaAndQuerySQLite(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	dbPath := filepath.Join(workspace, "sample.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE widgets (id INTEGER PRIMARY KEY, name TEXT NOT NULL);
		INSERT INTO widgets (name) VALUES ('alpha'), ('beta');
		CREATE INDEX idx_widgets_name ON widgets(name);
	`); err != nil {
		t.Fatalf("seed sqlite db: %v", err)
	}

	schemaPrimitive := NewDBSchema(workspace)
	schemaPayload, _ := json.Marshal(map[string]any{
		"connection": map[string]any{
			"dialect": "sqlite",
			"path":    "sample.db",
		},
	})
	schemaResult, err := schemaPrimitive.Execute(context.Background(), schemaPayload)
	if err != nil {
		t.Fatalf("db.schema execute: %v", err)
	}
	schemaBytes, _ := json.Marshal(schemaResult.Data)
	if !json.Valid(schemaBytes) || len(schemaBytes) == 0 {
		t.Fatalf("expected schema output to be valid json")
	}

	queryPrimitive := NewDBQuery(workspace)
	queryPayload, _ := json.Marshal(map[string]any{
		"connection": map[string]any{
			"dialect": "sqlite",
			"path":    "sample.db",
		},
		"query":    "SELECT id, name FROM widgets ORDER BY id",
		"max_rows": 1,
	})
	queryResult, err := queryPrimitive.Execute(context.Background(), queryPayload)
	if err != nil {
		t.Fatalf("db.query execute: %v", err)
	}
	body, _ := json.Marshal(queryResult.Data)
	if !json.Valid(body) || !strings.HasPrefix(string(body), `[`) {
		t.Fatalf("expected row array query result, got %s", string(body))
	}
	if !strings.Contains(string(body), `"id":1`) {
		t.Fatalf("expected first row to be present, got %s", string(body))
	}

	queryReadonlyPrimitive := NewDBQueryReadonly(workspace)
	queryReadonlyResult, err := queryReadonlyPrimitive.Execute(context.Background(), queryPayload)
	if err != nil {
		t.Fatalf("db.query_readonly execute: %v", err)
	}
	readonlyBody, _ := json.Marshal(queryReadonlyResult.Data)
	if !json.Valid(readonlyBody) || !strings.HasPrefix(string(readonlyBody), `{`) {
		t.Fatalf("expected legacy readonly envelope, got %s", string(readonlyBody))
	}
	if !strings.Contains(string(readonlyBody), `"rows":[`) {
		t.Fatalf("expected readonly result rows array, got %s", string(readonlyBody))
	}
	if !strings.Contains(string(readonlyBody), `"row_count":1`) {
		t.Fatalf("expected readonly result row_count, got %s", string(readonlyBody))
	}
}

func TestDBQueryRejectsDDL(t *testing.T) {
	t.Parallel()

	ddlStatements := []string{
		"DROP TABLE widgets",
		"DELETE FROM widgets",
		"INSERT INTO widgets (name) VALUES ('x')",
		"UPDATE widgets SET name = 'y' WHERE id = 1",
		"CREATE TABLE foo (id INTEGER)",
		"ALTER TABLE widgets ADD COLUMN weight REAL",
		"TRUNCATE widgets",
	}
	for _, stmt := range ddlStatements {
		if _, _, err := normalizeReadonlyQuery(stmt, 100); err == nil {
			t.Errorf("expected db.query to reject DDL/DML statement %q", stmt)
		}
	}
}

func TestDBExecuteRejectsSelectQueries(t *testing.T) {
	t.Parallel()
	if _, err := normalizeExecuteQuery("SELECT * FROM widgets"); err == nil {
		t.Fatalf("expected db.execute to reject read-only SELECT")
	}
}

func TestBrowserValidationAndMissingSession(t *testing.T) {
	t.Parallel()

	if _, err := validateBrowserURL("file:///tmp/demo.html"); err == nil {
		t.Fatalf("expected non-http browser url to be rejected")
	}

	manager := NewBrowserSessionManager(DefaultOptions())
	primitive := NewBrowserExtract(".", manager, DefaultOptions())
	payload, _ := json.Marshal(map[string]any{
		"session_id": "browser-missing",
		"selector":   "h1",
	})
	if _, err := primitive.Execute(context.Background(), payload); err == nil {
		t.Fatalf("expected missing browser session to fail")
	}
}

func TestBrowserRootContextSupportsTimedActions(t *testing.T) {
	t.Parallel()

	executable, err := findBrowserExecutable()
	if err != nil {
		t.Skipf("chromium not available: %v", err)
	}

	browserCtx, cancel, _, err := newBrowserRootContext(executable)
	if err != nil {
		t.Fatalf("create browser root context: %v", err)
	}
	defer cancel()

	if err := chromedp.Run(browserCtx); err != nil {
		t.Skipf("chromium not runnable in test environment: %v", err)
	}
	root := chromedp.FromContext(browserCtx)
	if root == nil || root.Browser == nil {
		t.Fatalf("expected root browser context to have an allocated browser")
	}

	runCtx, runCancel := context.WithTimeout(browserCtx, 5*time.Second)
	defer runCancel()
	if err := chromedp.Run(runCtx, chromedp.Navigate("data:text/html,<h1>ok</h1>")); err != nil {
		t.Fatalf("run timed browser action: %v", err)
	}
	child := chromedp.FromContext(runCtx)
	if child == nil || child.Browser == nil {
		t.Fatalf("expected timed child context to inherit an allocated browser")
	}
	if child.Browser != root.Browser {
		t.Fatalf("expected timed child context to reuse the existing browser instance")
	}
	runCtx2, runCancel2 := context.WithTimeout(browserCtx, 5*time.Second)
	defer runCancel2()
	if err := chromedp.Run(runCtx2, chromedp.Navigate("data:text/html,<h1>again</h1>")); err != nil {
		t.Fatalf("rerun timed browser action: %v", err)
	}
}

func TestNormalizeBrowserPageMetadata(t *testing.T) {
	t.Parallel()

	url, title := normalizeBrowserPageMetadata(browserPageContent{
		Title: "Redirected Page",
		URL:   "https://example.test/final",
	}, "https://example.test/original")
	if url != "https://example.test/final" || title != "Redirected Page" {
		t.Fatalf("expected extracted metadata to win, got url=%q title=%q", url, title)
	}

	url, title = normalizeBrowserPageMetadata(browserPageContent{}, "https://example.test/original")
	if url != "https://example.test/original" || title != "https://example.test/original" {
		t.Fatalf("expected fallback url/title, got url=%q title=%q", url, title)
	}
}
