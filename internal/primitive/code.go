package primitive

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// --------------------------------------------------------------------------
// code.search — Search code using ripgrep (or fallback to grep)
// --------------------------------------------------------------------------

type CodeSearch struct {
	resolver workspacePathResolver
}

func NewCodeSearch(workspaceDir string) *CodeSearch {
	return &CodeSearch{resolver: newWorkspacePathResolver(workspaceDir)}
}

func (c *CodeSearch) Name() string     { return "code.search" }
func (c *CodeSearch) Category() string { return "code" }
func (c *CodeSearch) Schema() Schema {
	return Schema{
		Name:        "code.search",
		Description: "Search code in workspace using pattern matching (ripgrep or grep)",
		Input: json.RawMessage(`{
			"type":"object",
			"properties":{
				"query":{"type":"string"},
				"path":{"type":"string"},
				"regex":{"type":"boolean"},
				"case_sensitive":{"type":"boolean"},
				"max_results":{"type":"integer","default":50}
			},
			"required":["query"]
		}`),
		Output: json.RawMessage(`{"type":"object","properties":{"matches":{"type":"array"},"total":{"type":"integer"}}}`),
	}
}

type codeSearchParams struct {
	Query         string `json:"query"`
	Path          string `json:"path,omitempty"`
	Regex         bool   `json:"regex,omitempty"`
	CaseSensitive bool   `json:"case_sensitive,omitempty"`
	MaxResults    int    `json:"max_results,omitempty"`
}

type codeSearchMatch struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Content string `json:"content"`
}

func (c *CodeSearch) Execute(ctx context.Context, params json.RawMessage) (Result, error) {
	var p codeSearchParams
	if err := json.Unmarshal(params, &p); err != nil {
		return Result{}, &PrimitiveError{Code: ErrValidation, Message: "invalid params: " + err.Error()}
	}

	if p.Query == "" {
		return Result{}, &PrimitiveError{Code: ErrValidation, Message: "query is required"}
	}

	if p.MaxResults <= 0 {
		p.MaxResults = 50
	}

	var err error
	searchDir := c.resolver.Root()
	if p.Path != "" {
		searchDir, err = c.resolver.Resolve(p.Path)
		if err != nil {
			return Result{}, err
		}
	}

	// Try ripgrep first, fallback to grep
	var matches []codeSearchMatch

	if _, lookErr := exec.LookPath("rg"); lookErr == nil {
		matches, err = c.searchWithRipgrep(ctx, p, searchDir)
	} else {
		matches, err = c.searchWithGrep(ctx, p, searchDir)
	}

	if err != nil {
		return Result{}, &PrimitiveError{Code: ErrExecution, Message: "search failed: " + err.Error()}
	}

	// Limit results
	total := len(matches)
	if len(matches) > p.MaxResults {
		matches = matches[:p.MaxResults]
	}

	return Result{
		Data: map[string]any{
			"matches": matches,
			"total":   total,
		},
	}, nil
}

func (c *CodeSearch) searchWithRipgrep(ctx context.Context, p codeSearchParams, dir string) ([]codeSearchMatch, error) {
	args := []string{"--line-number", "--no-heading", "--color=never"}

	if !p.CaseSensitive {
		args = append(args, "--ignore-case")
	}
	if !p.Regex {
		args = append(args, "--fixed-strings")
	}
	args = append(args, fmt.Sprintf("--max-count=%d", p.MaxResults))
	args = append(args, p.Query, dir)

	cmd := exec.CommandContext(ctx, "rg", args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	_ = cmd.Run() // ripgrep returns non-zero when no matches

	return parseGrepOutput(stdout.String(), c.resolver.Root()), nil
}

func (c *CodeSearch) searchWithGrep(ctx context.Context, p codeSearchParams, dir string) ([]codeSearchMatch, error) {
	args := []string{"-rn", "--color=never"}

	if !p.CaseSensitive {
		args = append(args, "-i")
	}
	if !p.Regex {
		args = append(args, "-F")
	}
	args = append(args, p.Query, dir)

	cmd := exec.CommandContext(ctx, "grep", args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	_ = cmd.Run()

	return parseGrepOutput(stdout.String(), c.resolver.Root()), nil
}

func parseGrepOutput(output, baseDir string) []codeSearchMatch {
	var matches []codeSearchMatch
	scanner := bufio.NewScanner(strings.NewReader(output))

	for scanner.Scan() {
		line := scanner.Text()
		// Format: file:line:content
		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 3 {
			continue
		}

		file := parts[0]
		// Make path relative to workspace
		if strings.HasPrefix(file, baseDir) {
			file = strings.TrimPrefix(file, baseDir+"/")
		}

		lineNum, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}

		matches = append(matches, codeSearchMatch{
			File:    file,
			Line:    lineNum,
			Content: strings.TrimSpace(parts[2]),
		})
	}
	return matches
}

// --------------------------------------------------------------------------
// code.symbols — Extract symbols (functions, classes, methods) from a file
// --------------------------------------------------------------------------

// CodeSymbols extracts top-level declarations using language-native regex patterns.
// Supports Python, Go, JavaScript/TypeScript, Rust, and Java.
// When Tree-sitter is integrated in a future iteration, this can be swapped in-place.
type CodeSymbols struct {
	resolver workspacePathResolver
}

func NewCodeSymbols(workspaceDir string) *CodeSymbols {
	return &CodeSymbols{resolver: newWorkspacePathResolver(workspaceDir)}
}

func (c *CodeSymbols) Name() string     { return "code.symbols" }
func (c *CodeSymbols) Category() string { return "code" }
func (c *CodeSymbols) Schema() Schema {
	return Schema{
		Name:        "code.symbols",
		Description: "Extract top-level symbols (functions, classes, methods) from a source file to provide a structural outline",
		Input: json.RawMessage(`{
			"type":"object",
			"properties":{
				"path":{"type":"string","description":"File path relative to workspace"},
				"kinds":{"type":"array","items":{"type":"string"},"description":"Filter by kind: function, class, method (default: all)"}
			},
			"required":["path"]
		}`),
		Output: json.RawMessage(`{"type":"object","properties":{"symbols":{"type":"array"},"language":{"type":"string"},"total":{"type":"integer"}}}`),
	}
}

type codeSymbolsParams struct {
	Path  string   `json:"path"`
	Kinds []string `json:"kinds,omitempty"`
}

// Symbol represents a detected code declaration.
type Symbol struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"` // "function", "class", "method", "trait", "interface"
	StartLine int    `json:"start_line"`
	Signature string `json:"signature,omitempty"`
}

// symbolPattern is a compiled regex with a target kind.
type symbolPattern struct {
	kind    string
	pattern *regexp.Regexp
}

// langPattern bundles language name with detection patterns.
type langPattern struct {
	name     string
	patterns []symbolPattern
}

var langPatterns = map[string]langPattern{
	".py": {name: "python", patterns: []symbolPattern{
		{kind: "class", pattern: regexp.MustCompile(`^class\s+(\w+)`)},
		{kind: "method", pattern: regexp.MustCompile(`^\s{4,}def\s+(\w+)`)},
		{kind: "function", pattern: regexp.MustCompile(`^def\s+(\w+)`)},
	}},
	".go": {name: "go", patterns: []symbolPattern{
		{kind: "method", pattern: regexp.MustCompile(`^func\s+\([^)]+\)\s+(\w+)\s*\(`)},
		{kind: "function", pattern: regexp.MustCompile(`^func\s+(\w+)\s*\(`)},
		{kind: "class", pattern: regexp.MustCompile(`^type\s+(\w+)\s+struct`)},
	}},
	".js": {name: "javascript", patterns: []symbolPattern{
		{kind: "class", pattern: regexp.MustCompile(`^(?:export\s+)?class\s+(\w+)`)},
		{kind: "function", pattern: regexp.MustCompile(`^(?:export\s+)?(?:async\s+)?function\s+(\w+)`)},
		{kind: "method", pattern: regexp.MustCompile(`^\s{2,}(?:async\s+)?(\w+)\s*\(`)},
	}},
	".ts": {name: "typescript", patterns: []symbolPattern{
		{kind: "class", pattern: regexp.MustCompile(`^(?:export\s+)?class\s+(\w+)`)},
		{kind: "interface", pattern: regexp.MustCompile(`^(?:export\s+)?interface\s+(\w+)`)},
		{kind: "function", pattern: regexp.MustCompile(`^(?:export\s+)?(?:async\s+)?function\s+(\w+)`)},
		{kind: "method", pattern: regexp.MustCompile(`^\s{2,}(?:async\s+)?(\w+)\s*\(`)},
	}},
	".rs": {name: "rust", patterns: []symbolPattern{
		{kind: "trait", pattern: regexp.MustCompile(`^(?:pub\s+)?trait\s+(\w+)`)},
		{kind: "class", pattern: regexp.MustCompile(`^(?:pub\s+)?struct\s+(\w+)`)},
		{kind: "method", pattern: regexp.MustCompile(`^\s{4,}(?:pub\s+)?(?:async\s+)?fn\s+(\w+)`)},
		{kind: "function", pattern: regexp.MustCompile(`^(?:pub\s+)?(?:async\s+)?fn\s+(\w+)`)},
	}},
	".java": {name: "java", patterns: []symbolPattern{
		{kind: "interface", pattern: regexp.MustCompile(`^(?:public\s+)?interface\s+(\w+)`)},
		{kind: "class", pattern: regexp.MustCompile(`^(?:public\s+)?(?:abstract\s+)?class\s+(\w+)`)},
		{kind: "method", pattern: regexp.MustCompile(`^\s{4,}(?:public|private|protected)?(?:\s+static)?\s+\w+\s+(\w+)\s*\(`)},
	}},
}

func (c *CodeSymbols) Execute(ctx context.Context, params json.RawMessage) (Result, error) {
	var p codeSymbolsParams
	if err := json.Unmarshal(params, &p); err != nil {
		return Result{}, &PrimitiveError{Code: ErrValidation, Message: "invalid params: " + err.Error()}
	}
	if p.Path == "" {
		return Result{}, &PrimitiveError{Code: ErrValidation, Message: "path is required"}
	}

	absPath, err := c.resolver.Resolve(p.Path)
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

	ext := strings.ToLower(filepath.Ext(absPath))
	lang, ok := langPatterns[ext]
	if !ok {
		return Result{Data: map[string]any{
			"symbols":  []Symbol{},
			"language": "unknown",
			"total":    0,
			"note":     fmt.Sprintf("unsupported extension %q; supported: .py .go .js .ts .rs .java", ext),
		}}, nil
	}

	// Build kind filter set
	kindFilter := map[string]bool{}
	for _, k := range p.Kinds {
		kindFilter[k] = true
	}

	var symbols []Symbol
	lines := strings.Split(string(data), "\n")

	for i, line := range lines {
		for _, sp := range lang.patterns {
			if len(kindFilter) > 0 && !kindFilter[sp.kind] {
				continue
			}
			m := sp.pattern.FindStringSubmatch(line)
			if len(m) >= 2 {
				symbols = append(symbols, Symbol{
					Name:      m[1],
					Kind:      sp.kind,
					StartLine: i + 1,
					Signature: strings.TrimSpace(line),
				})
				break // Don't double-match the same line
			}
		}
	}

	return Result{Data: map[string]any{
		"symbols":  symbols,
		"language": lang.name,
		"total":    len(symbols),
	}}, nil
}
