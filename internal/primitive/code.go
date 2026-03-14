package primitive

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
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

		lineNum := 0
		fmt.Sscanf(parts[1], "%d", &lineNum)

		matches = append(matches, codeSearchMatch{
			File:    file,
			Line:    lineNum,
			Content: strings.TrimSpace(parts[2]),
		})
	}
	return matches
}
