package primitive

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFSReadRejectsInvalidLineRange(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	file := filepath.Join(dir, "main.txt")
	if err := os.WriteFile(file, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err := NewFSRead(dir).Execute(context.Background(), mustJSON(t, map[string]any{
		"path":       "main.txt",
		"start_line": 3,
		"end_line":   1,
	}))
	assertPrimitiveError(t, err, ErrValidation)
}

func TestFSReadRejectsParentTraversal(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	outside := filepath.Join(base, "workspace-escape")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("nope"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	_, err := NewFSRead(workspace).Execute(context.Background(), mustJSON(t, map[string]any{
		"path": "../workspace-escape/secret.txt",
	}))
	assertPrimitiveError(t, err, ErrPermission)
}

func TestFSReadRejectsSymlinkEscape(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "link")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	_, err := NewFSRead(workspace).Execute(context.Background(), mustJSON(t, map[string]any{
		"path": "link/secret.txt",
	}))
	assertPrimitiveError(t, err, ErrPermission)
}

func TestCodeSearchRejectsPathEscape(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	_, err := NewCodeSearch(workspace).Execute(context.Background(), mustJSON(t, map[string]any{
		"query": "main",
		"path":  "../outside",
	}))
	assertPrimitiveError(t, err, ErrPermission)
}

func TestFSWriteSearchReplaceRequiresUniqueMatch(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	file := filepath.Join(dir, "main.txt")
	if err := os.WriteFile(file, []byte("return None\nreturn None\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err := NewFSWrite(dir).Execute(context.Background(), mustJSON(t, map[string]any{
		"path":    "main.txt",
		"mode":    "search_replace",
		"search":  "return None",
		"replace": "return 42",
	}))
	assertPrimitiveError(t, err, ErrValidation)
}

func TestShellExecEnforcesWhitelist(t *testing.T) {
	t.Parallel()

	_, err := NewShellExec(t.TempDir(), Options{
		AllowedCommands: []string{"echo"},
		DefaultTimeout:  1,
	}).Execute(context.Background(), mustJSON(t, map[string]any{
		"command": "pwd",
	}))
	assertPrimitiveError(t, err, ErrPermission)
}

func TestShellExecReturnsTimeoutResult(t *testing.T) {
	t.Parallel()

	result, err := NewShellExec(t.TempDir(), Options{
		DefaultTimeout: 1,
	}).Execute(context.Background(), mustJSON(t, map[string]any{
		"command":   "sleep 2",
		"timeout_s": 1,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data := decodeResult[shellExecResult](t, result)
	if !data.TimedOut {
		t.Fatalf("expected timeout result, got %+v", data)
	}
	if data.ExitCode != -1 {
		t.Fatalf("expected exit code -1 on timeout, got %d", data.ExitCode)
	}
}

func TestVerifyTestSharesShellPolicy(t *testing.T) {
	t.Parallel()

	_, err := NewVerifyTest(t.TempDir(), Options{
		AllowedCommands: []string{"pytest"},
		DefaultTimeout:  1,
	}).Execute(context.Background(), mustJSON(t, map[string]any{
		"command": "pwd",
	}))
	assertPrimitiveError(t, err, ErrPermission)
}

func TestStateRestoreRestoresTrackedAndIgnoredFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	file := filepath.Join(dir, "main.txt")
	if err := os.WriteFile(file, []byte("before\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	checkpointCallResult, err := NewStateCheckpoint(dir).Execute(context.Background(), mustJSON(t, map[string]any{
		"label": "before-change",
	}))
	if err != nil {
		t.Fatalf("create checkpoint: %v", err)
	}
	checkpoint := decodeResult[CheckpointResult](t, checkpointCallResult)

	if err := os.WriteFile(file, []byte("after\n"), 0o644); err != nil {
		t.Fatalf("rewrite file: %v", err)
	}
	cacheDir := filepath.Join(dir, "__pycache__")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}
	cacheFile := filepath.Join(cacheDir, "main.pyc")
	if err := os.WriteFile(cacheFile, []byte("ignored"), 0o644); err != nil {
		t.Fatalf("write cache file: %v", err)
	}

	restoreCallResult, err := NewStateRestore(dir).Execute(context.Background(), mustJSON(t, map[string]any{
		"checkpoint_id": checkpoint.CheckpointID,
	}))
	if err != nil {
		t.Fatalf("restore checkpoint: %v", err)
	}

	restore := decodeResult[RestoreResult](t, restoreCallResult)
	content, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read restored file: %v", err)
	}
	if string(content) != "before\n" {
		t.Fatalf("expected restored content, got %q", string(content))
	}
	if _, err := os.Stat(cacheFile); !os.IsNotExist(err) {
		t.Fatalf("expected ignored cache file to be removed, stat err=%v", err)
	}
	if restore.FilesChanged < 2 {
		t.Fatalf("expected at least 2 restored files, got %d", restore.FilesChanged)
	}
	if !containsString(restore.RestoredFiles, "main.txt") {
		t.Fatalf("expected restored_files to contain main.txt, got %v", restore.RestoredFiles)
	}
	if !containsPrefix(restore.RestoredFiles, "__pycache__") {
		t.Fatalf("expected restored_files to include ignored files, got %v", restore.RestoredFiles)
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()

	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return data
}

func decodeResult[T any](t *testing.T, result Result) T {
	t.Helper()

	data, err := json.Marshal(result.Data)
	if err != nil {
		t.Fatalf("marshal result data: %v", err)
	}

	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal result data: %v", err)
	}
	return out
}

func assertPrimitiveError(t *testing.T, err error, expected ErrorCode) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected error %s, got nil", expected)
	}

	primitiveErr, ok := err.(*PrimitiveError)
	if !ok {
		t.Fatalf("expected PrimitiveError, got %T", err)
	}
	if primitiveErr.Code != expected {
		t.Fatalf("expected error code %s, got %s", expected, primitiveErr.Code)
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func containsPrefix(items []string, prefix string) bool {
	for _, item := range items {
		if strings.HasPrefix(item, prefix) {
			return true
		}
	}
	return false
}
