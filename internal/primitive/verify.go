package primitive

import (
	"context"
	"encoding/json"
	"strings"
)

// --------------------------------------------------------------------------
// verify.test — Run tests and return structured pass/fail results
// --------------------------------------------------------------------------

type VerifyTest struct {
	shell *ShellExec
}

func NewVerifyTest(workspaceDir string, options Options) *VerifyTest {
	resolver := newWorkspacePathResolver(workspaceDir)
	return &VerifyTest{
		shell: NewShellExec(resolver.Root(), options),
	}
}

func (v *VerifyTest) Name() string     { return "verify.test" }
func (v *VerifyTest) Category() string { return "verify" }
func (v *VerifyTest) Schema() Schema {
	return Schema{
		Name:        "verify.test",
		Description: "Run test command and return structured pass/fail results",
		Input: json.RawMessage(`{
			"type":"object",
			"properties":{
				"command":{"type":"string","default":"pytest"},
				"timeout_s":{"type":"integer"}
			}
		}`),
		Output: json.RawMessage(`{
			"type":"object",
			"properties":{
				"passed":{"type":"boolean"},
				"total":{"type":"integer"},
				"failures":{"type":"integer"},
				"errors":{"type":"integer"},
				"output":{"type":"string"},
				"summary":{"type":"string"}
			}
		}`),
	}
}

type verifyTestParams struct {
	Command string `json:"command,omitempty"`
	Timeout int    `json:"timeout_s,omitempty"`
}

type VerifyTestResult struct {
	Passed   bool   `json:"passed"`
	Total    int    `json:"total"`
	Failures int    `json:"failures"`
	Errors   int    `json:"errors"`
	Output   string `json:"output"`
	Summary  string `json:"summary"`
}

func (v *VerifyTest) Execute(ctx context.Context, params json.RawMessage) (Result, error) {
	var p verifyTestParams
	if params != nil {
		if err := json.Unmarshal(params, &p); err != nil {
			return Result{}, &PrimitiveError{Code: ErrValidation, Message: "invalid params: " + err.Error()}
		}
	}

	if p.Command == "" {
		p.Command = "pytest"
	}
	shellParams, _ := json.Marshal(shellExecParams{
		Command: p.Command,
		Timeout: p.Timeout,
	})

	shellResult, err := v.shell.Execute(ctx, shellParams)
	if err != nil {
		return Result{}, err
	}

	// Parse shell result
	shellData, _ := json.Marshal(shellResult.Data)
	var execResult shellExecResult
	_ = json.Unmarshal(shellData, &execResult)

	// Determine pass/fail
	passed := execResult.ExitCode == 0

	// Generate summary (truncated for LLM context)
	summary := generateTestSummary(execResult.Stdout, execResult.Stderr, passed)

	return Result{
		Data: VerifyTestResult{
			Passed:   passed,
			Total:    0, // Could parse from output for specific frameworks
			Failures: 0,
			Errors:   0,
			Output:   truncateOutput(execResult.Stdout+"\n"+execResult.Stderr, 5000),
			Summary:  summary,
		},
		Duration: execResult.DurationMs,
	}, nil
}

// generateTestSummary creates a concise test summary for LLM consumption.
func generateTestSummary(stdout, stderr string, passed bool) string {
	var sb strings.Builder

	if passed {
		sb.WriteString("✅ All tests passed.\n")
	} else {
		sb.WriteString("❌ Tests failed.\n")
	}

	// Try to extract last few meaningful lines
	lines := strings.Split(stdout, "\n")
	if len(lines) > 5 {
		sb.WriteString("Last output lines:\n")
		for _, line := range lines[len(lines)-5:] {
			if strings.TrimSpace(line) != "" {
				sb.WriteString("  " + line + "\n")
			}
		}
	}

	if stderr != "" && !passed {
		errLines := strings.Split(stderr, "\n")
		if len(errLines) > 3 {
			errLines = errLines[len(errLines)-3:]
		}
		sb.WriteString("Errors:\n")
		for _, line := range errLines {
			if strings.TrimSpace(line) != "" {
				sb.WriteString("  " + line + "\n")
			}
		}
	}

	return sb.String()
}
