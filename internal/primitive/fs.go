package primitive

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"primitivebox/internal/cvr"
	"primitivebox/internal/eventing"
	"primitivebox/internal/primitive/astdiff"
	"primitivebox/internal/runtimectx"
)

// --------------------------------------------------------------------------
// fs.read — Read file content (supports partial reads)
// --------------------------------------------------------------------------

type FSRead struct {
	resolver workspacePathResolver
}

func NewFSRead(workspaceDir string) *FSRead {
	return &FSRead{resolver: newWorkspacePathResolver(workspaceDir)}
}

func (f *FSRead) Name() string     { return "fs.read" }
func (f *FSRead) Category() string { return "fs" }
func (f *FSRead) Schema() Schema {
	return Schema{
		Name:        "fs.read",
		Description: "Read file content with optional line range",
		Input:       json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"start_line":{"type":"integer"},"end_line":{"type":"integer"}},"required":["path"]}`),
		Output:      json.RawMessage(`{"type":"object","properties":{"content":{"type":"string"},"total_lines":{"type":"integer"},"encoding":{"type":"string"}}}`),
	}
}

type fsReadParams struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
}

type fsReadResult struct {
	Content    string `json:"content"`
	TotalLines int    `json:"total_lines"`
	Encoding   string `json:"encoding"`
}

func (f *FSRead) Execute(ctx context.Context, params json.RawMessage) (Result, error) {
	var p fsReadParams
	if err := json.Unmarshal(params, &p); err != nil {
		return Result{}, &PrimitiveError{Code: ErrValidation, Message: "invalid params: " + err.Error()}
	}

	absPath, err := f.resolver.Resolve(p.Path)
	if err != nil {
		return Result{}, err
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return Result{}, &PrimitiveError{Code: ErrNotFound, Message: "file not found: " + p.Path}
		}
		return Result{}, &PrimitiveError{Code: ErrExecution, Message: err.Error()}
	}

	content := string(data)
	var lines []string
	totalLines := 0
	if content != "" {
		lines = strings.Split(content, "\n")
		totalLines = len(lines)
	}

	// Apply line range filter
	if p.StartLine > 0 || p.EndLine > 0 {
		start := p.StartLine
		end := p.EndLine
		if start < 1 {
			start = 1
		}
		if end < 1 || end > totalLines {
			end = totalLines
		}
		if totalLines == 0 {
			content = ""
		} else {
			if start > totalLines {
				start = totalLines
			}
			if start > end {
				return Result{}, &PrimitiveError{
					Code:    ErrValidation,
					Message: fmt.Sprintf("invalid line range: start_line %d is greater than end_line %d", p.StartLine, p.EndLine),
				}
			}
			// Convert to 0-indexed
			content = strings.Join(lines[start-1:end], "\n")
		}
	}

	return Result{
		Data: fsReadResult{
			Content:    content,
			TotalLines: totalLines,
			Encoding:   "utf-8",
		},
	}, nil
}

// --------------------------------------------------------------------------
// fs.write — Write file content (search-replace or full overwrite)
// --------------------------------------------------------------------------

type FSWrite struct {
	resolver workspacePathResolver
}

func NewFSWrite(workspaceDir string) *FSWrite {
	return &FSWrite{resolver: newWorkspacePathResolver(workspaceDir)}
}

func (f *FSWrite) Name() string     { return "fs.write" }
func (f *FSWrite) Category() string { return "fs" }
func (f *FSWrite) Schema() Schema {
	return Schema{
		Name:        "fs.write",
		Description: "Write file content via full overwrite or search-and-replace",
		Input: json.RawMessage(`{
			"type":"object",
			"properties":{
				"path":{"type":"string"},
				"content":{"type":"string"},
				"mode":{"type":"string","enum":["overwrite","search_replace"]},
				"search":{"type":"string"},
				"replace":{"type":"string"},
				"create_dirs":{"type":"boolean"}
			},
			"required":["path"]
		}`),
		Output: json.RawMessage(`{
			"type":"object",
			"properties":{
				"bytes_written":{"type":"integer"},
				"diff":{"type":"string"},
				"symbol_changes":{"type":"array"},
				"effect_log":{"type":"array"}
			}
		}`),
	}
}

type fsWriteParams struct {
	Path       string `json:"path"`
	Content    string `json:"content,omitempty"`
	Mode       string `json:"mode,omitempty"` // "overwrite" (default) or "search_replace"
	Search     string `json:"search,omitempty"`
	Replace    string `json:"replace,omitempty"`
	CreateDirs bool   `json:"create_dirs,omitempty"`
}

type FSWriteResult struct {
	BytesWritten  int                    `json:"bytes_written"`
	Diff          string                 `json:"diff,omitempty"`
	SymbolChanges []astdiff.SymbolChange `json:"symbol_changes,omitempty"`
	EffectLog     []cvr.EffectEntry      `json:"effect_log,omitempty"`
}

func (f *FSWrite) Execute(ctx context.Context, params json.RawMessage) (Result, error) {
	var p fsWriteParams
	if err := json.Unmarshal(params, &p); err != nil {
		return Result{}, &PrimitiveError{Code: ErrValidation, Message: "invalid params: " + err.Error()}
	}

	absPath, err := f.resolver.Resolve(p.Path)
	if err != nil {
		return Result{}, err
	}

	// Create parent directories if requested
	if p.CreateDirs {
		if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			return Result{}, &PrimitiveError{Code: ErrExecution, Message: "cannot create directories: " + err.Error()}
		}
	}

	var newContent string
	var oldContent string
	var oldBytes []byte

	// Read existing content for diff generation
	if existing, err := os.ReadFile(absPath); err == nil {
		oldBytes = append([]byte(nil), existing...)
		oldContent = string(existing)
	}

	switch p.Mode {
	case "search_replace":
		if p.Search == "" {
			return Result{}, &PrimitiveError{Code: ErrValidation, Message: "search text is required for search_replace mode"}
		}
		if !strings.Contains(oldContent, p.Search) {
			return Result{}, &PrimitiveError{
				Code:    ErrNotFound,
				Message: "search text not found in file",
				Details: map[string]string{"search": truncate(p.Search, 100)},
			}
		}
		// Validate unique match to prevent ambiguous replacements
		count := strings.Count(oldContent, p.Search)
		if count > 1 {
			return Result{}, &PrimitiveError{
				Code:    ErrValidation,
				Message: fmt.Sprintf("search text found %d times, must be unique (provide more context)", count),
			}
		}
		newContent = strings.Replace(oldContent, p.Search, p.Replace, 1)

	default: // "overwrite" or empty
		newContent = p.Content
	}

	// Write the file
	if err := os.WriteFile(absPath, []byte(newContent), 0o644); err != nil {
		return Result{}, &PrimitiveError{Code: ErrExecution, Message: err.Error()}
	}

	// Generate simple diff
	diff := generateSimpleDiff(oldContent, newContent)
	payload := map[string]any{
		"bytes_written": len(newContent),
		"diff":          diff,
	}

	effectEntry := cvr.EffectEntry{
		Kind:         "file_write",
		Target:       p.Path,
		ReversibleBy: "state.restore",
	}

	var warning string
	if strings.HasSuffix(p.Path, ".go") {
		changes, diffErr := astdiff.Diff(oldBytes, []byte(newContent))
		if diffErr != nil {
			warning = "ast diff failed: " + diffErr.Error()
			log.Printf("warning: fs.write semantic diff skipped for %s: %v", p.Path, diffErr)
		} else {
			payload["symbol_changes"] = changes
			symbols := collectSymbols(changes)
			if len(symbols) > 0 {
				effectEntry.AffectedSymbols = symbols
				if intent, ok := runtimectx.IntentFromContext(ctx); ok && intent != nil {
					intent.AffectedScopes = mergeUniqueStrings(intent.AffectedScopes, symbols)
				}
			}
		}
	}
	payload["effect_log"] = []cvr.EffectEntry{effectEntry}

	return Result{
		Data:    payload,
		Diff:    diff,
		Warning: warning,
	}, nil
}

// --------------------------------------------------------------------------
// fs.list — List directory contents
// --------------------------------------------------------------------------

type FSList struct {
	resolver workspacePathResolver
}

func NewFSList(workspaceDir string) *FSList {
	return &FSList{resolver: newWorkspacePathResolver(workspaceDir)}
}

func (f *FSList) Name() string     { return "fs.list" }
func (f *FSList) Category() string { return "fs" }
func (f *FSList) Schema() Schema {
	return Schema{
		Name:        "fs.list",
		Description: "List directory contents with optional recursive traversal and glob pattern",
		Input:       json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"recursive":{"type":"boolean"},"pattern":{"type":"string"}},"required":["path"]}`),
		Output:      json.RawMessage(`{"type":"object","properties":{"entries":{"type":"array"}}}`),
	}
}

type fsListParams struct {
	Path      string `json:"path"`
	Recursive bool   `json:"recursive,omitempty"`
	Pattern   string `json:"pattern,omitempty"`
}

type fsListEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
}

func (f *FSList) Execute(ctx context.Context, params json.RawMessage) (Result, error) {
	var p fsListParams
	if err := json.Unmarshal(params, &p); err != nil {
		return Result{}, &PrimitiveError{Code: ErrValidation, Message: "invalid params: " + err.Error()}
	}

	absPath, err := f.resolver.Resolve(p.Path)
	if err != nil {
		return Result{}, err
	}

	var entries []fsListEntry

	if p.Recursive {
		err := filepath.Walk(absPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // Skip errors
			}
			if !f.resolver.IsWithinRoot(path) {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			relPath, _ := filepath.Rel(f.resolver.Root(), path)
			if p.Pattern != "" {
				if matched, _ := filepath.Match(p.Pattern, info.Name()); !matched {
					return nil
				}
			}
			entries = append(entries, fsListEntry{
				Name:  info.Name(),
				Path:  relPath,
				IsDir: info.IsDir(),
				Size:  info.Size(),
			})
			return nil
		})
		if err != nil {
			return Result{}, &PrimitiveError{Code: ErrExecution, Message: err.Error()}
		}
	} else {
		dirEntries, err := os.ReadDir(absPath)
		if err != nil {
			return Result{}, &PrimitiveError{Code: ErrExecution, Message: err.Error()}
		}
		for _, de := range dirEntries {
			info, _ := de.Info()
			if info == nil {
				continue
			}
			if p.Pattern != "" {
				if matched, _ := filepath.Match(p.Pattern, de.Name()); !matched {
					continue
				}
			}
			entryPath := filepath.Join(absPath, de.Name())
			if !f.resolver.IsWithinRoot(entryPath) {
				continue
			}
			relPath, _ := filepath.Rel(f.resolver.Root(), entryPath)
			entries = append(entries, fsListEntry{
				Name:  de.Name(),
				Path:  relPath,
				IsDir: de.IsDir(),
				Size:  info.Size(),
			})
		}
	}

	return Result{Data: map[string]any{"entries": entries}}, nil
}

// --------------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------------

func generateSimpleDiff(old, new string) string {
	if old == "" {
		return "+++ (new file)"
	}
	oldLines := strings.Split(old, "\n")
	newLines := strings.Split(new, "\n")

	var diff strings.Builder
	diff.WriteString(fmt.Sprintf("--- old (%d lines)\n", len(oldLines)))
	diff.WriteString(fmt.Sprintf("+++ new (%d lines)\n", len(newLines)))

	// Simple line-by-line comparison
	maxLen := len(oldLines)
	if len(newLines) > maxLen {
		maxLen = len(newLines)
	}

	changedLines := 0
	for i := 0; i < maxLen; i++ {
		var oldLine, newLine string
		if i < len(oldLines) {
			oldLine = oldLines[i]
		}
		if i < len(newLines) {
			newLine = newLines[i]
		}
		if oldLine != newLine {
			if i < len(oldLines) {
				diff.WriteString(fmt.Sprintf("-%s\n", oldLine))
			}
			if i < len(newLines) {
				diff.WriteString(fmt.Sprintf("+%s\n", newLine))
			}
			changedLines++
		}
	}

	if changedLines == 0 {
		return "(no changes)"
	}
	return diff.String()
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func collectSymbols(changes []astdiff.SymbolChange) []string {
	if len(changes) == 0 {
		return nil
	}
	symbols := make([]string, 0, len(changes))
	seen := make(map[string]struct{}, len(changes))
	for _, change := range changes {
		if change.Symbol == "" {
			continue
		}
		if _, ok := seen[change.Symbol]; ok {
			continue
		}
		seen[change.Symbol] = struct{}{}
		symbols = append(symbols, change.Symbol)
	}
	return symbols
}

func mergeUniqueStrings(existing []string, values []string) []string {
	if len(values) == 0 {
		return existing
	}
	seen := make(map[string]struct{}, len(existing)+len(values))
	merged := make([]string, 0, len(existing)+len(values))
	for _, value := range existing {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		merged = append(merged, value)
	}
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		merged = append(merged, value)
	}
	return merged
}

// --------------------------------------------------------------------------
// fs.diff — Show uncommitted workspace changes against the last Git checkpoint
// --------------------------------------------------------------------------

type FSDiff struct {
	resolver workspacePathResolver
}

func NewFSDiff(workspaceDir string) *FSDiff {
	return &FSDiff{resolver: newWorkspacePathResolver(workspaceDir)}
}

func (f *FSDiff) Name() string     { return "fs.diff" }
func (f *FSDiff) Category() string { return "fs" }
func (f *FSDiff) Schema() Schema {
	return Schema{
		Name:        "fs.diff",
		Description: "Show uncommitted workspace changes as a unified diff against the last Git checkpoint (HEAD)",
		Input: json.RawMessage(`{
			"type":"object",
			"properties":{
				"path":{"type":"string","description":"Optional file or directory path to diff (relative to workspace). Defaults to whole workspace."},
				"staged":{"type":"boolean","description":"If true, show staged diff (index vs HEAD). Default: working tree vs HEAD."}
			}
		}`),
		Output: json.RawMessage(`{"type":"object","properties":{"diff":{"type":"string"},"changed_files":{"type":"array"},"has_changes":{"type":"boolean"}}}`),
	}
}

type fsDiffParams struct {
	Path   string `json:"path,omitempty"`
	Staged bool   `json:"staged,omitempty"`
}

func (f *FSDiff) Execute(ctx context.Context, params json.RawMessage) (Result, error) {
	var p fsDiffParams
	if err := json.Unmarshal(params, &p); err != nil {
		return Result{}, &PrimitiveError{Code: ErrValidation, Message: "invalid params: " + err.Error()}
	}

	workspaceDir := f.resolver.Root()

	// Check for git
	gitArgs := []string{"diff", "HEAD", "--unified=3"}
	if p.Staged {
		gitArgs = []string{"diff", "--cached", "--unified=3"}
	}

	if p.Path != "" {
		absPath, err := f.resolver.Resolve(p.Path)
		if err != nil {
			return Result{}, err
		}
		gitArgs = append(gitArgs, "--", absPath)
	}

	diffCmd := exec.CommandContext(ctx, "git", gitArgs...)
	diffCmd.Dir = workspaceDir
	var diffOut, diffErr bytes.Buffer
	diffCmd.Stdout = &diffOut
	diffCmd.Stderr = &diffErr
	if err := diffCmd.Run(); err != nil && diffErr.Len() > 0 {
		return Result{}, &PrimitiveError{Code: ErrExecution, Message: "git diff failed: " + diffErr.String()}
	}

	// Get list of changed files
	nameOnlyArgs := []string{"diff", "HEAD", "--name-only"}
	if p.Staged {
		nameOnlyArgs = []string{"diff", "--cached", "--name-only"}
	}
	namesCmd := exec.CommandContext(ctx, "git", nameOnlyArgs...)
	namesCmd.Dir = workspaceDir
	var namesOut bytes.Buffer
	namesCmd.Stdout = &namesOut
	_ = namesCmd.Run()

	changedFiles := []string{}
	for _, file := range strings.Split(namesOut.String(), "\n") {
		file = strings.TrimSpace(file)
		if file != "" {
			changedFiles = append(changedFiles, file)
		}
	}

	diffStr := diffOut.String()
	if len(diffStr) > 32000 {
		diffStr = diffStr[:32000] + "\n... (truncated)"
	}

	payload := map[string]any{
		"diff":          diffStr,
		"changed_files": changedFiles,
		"has_changes":   len(changedFiles) > 0,
	}
	eventing.Emit(ctx, eventing.Event{
		Type:    "fs.diff",
		Source:  "primitive",
		Method:  f.Name(),
		Message: fmt.Sprintf("%d changed files", len(changedFiles)),
		Data:    eventing.MustJSON(payload),
	})

	return Result{Data: payload}, nil
}
