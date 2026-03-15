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

func TestDBQueryReadonlyRejectsMultipleStatements(t *testing.T) {
	t.Parallel()

	if _, _, err := normalizeReadonlyQuery("select * from widgets; drop table widgets;", 100); err == nil {
		t.Fatalf("expected multiple statements to be rejected")
	}
}

func TestDBSchemaAndReadonlyQuerySQLite(t *testing.T) {
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

	queryPrimitive := NewDBQueryReadonly(workspace)
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
		t.Fatalf("db.query_readonly execute: %v", err)
	}
	body, _ := json.Marshal(queryResult.Data)
	if !json.Valid(body) || !strings.Contains(string(body), `"limited":true`) {
		t.Fatalf("expected limited query result, got %s", string(body))
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
