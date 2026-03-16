package primitive

import (
	"context"
	"encoding/json"
)

type TestRun struct {
	*VerifyTest
}

func NewTestRun(workspaceDir string, options Options) *TestRun {
	return &TestRun{VerifyTest: NewVerifyTest(workspaceDir, options)}
}

func (t *TestRun) Name() string     { return "test.run" }
func (t *TestRun) Category() string { return "test" }
func (t *TestRun) Schema() Schema {
	return Schema{
		Name:        t.Name(),
		Namespace:   "test",
		Description: "Run a structured test command and return pass/fail details.",
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

type VerifyCommand struct {
	shell *ShellExec
}

func NewVerifyCommand(workspaceDir string, options Options) *VerifyCommand {
	return &VerifyCommand{
		shell: NewShellExec(newWorkspacePathResolver(workspaceDir).Root(), options),
	}
}

func (v *VerifyCommand) Name() string     { return "verify.command" }
func (v *VerifyCommand) Category() string { return "verify" }
func (v *VerifyCommand) Schema() Schema {
	return Schema{
		Name:        v.Name(),
		Namespace:   "verify",
		Description: "Run a generic verification command and return structured success/failure output.",
		Input: json.RawMessage(`{
			"type":"object",
			"properties":{
				"command":{"type":"string"},
				"timeout_s":{"type":"integer"}
			},
			"required":["command"]
		}`),
		Output: json.RawMessage(`{
			"type":"object",
			"properties":{
				"passed":{"type":"boolean"},
				"exit_code":{"type":"integer"},
				"timed_out":{"type":"boolean"},
				"stdout":{"type":"string"},
				"stderr":{"type":"string"},
				"summary":{"type":"string"}
			}
		}`),
	}
}

type verifyCommandParams struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout_s,omitempty"`
}

type VerifyCommandResult struct {
	Passed   bool   `json:"passed"`
	ExitCode int    `json:"exit_code"`
	TimedOut bool   `json:"timed_out"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	Summary  string `json:"summary"`
}

func (v *VerifyCommand) Execute(ctx context.Context, params json.RawMessage) (Result, error) {
	var p verifyCommandParams
	if err := json.Unmarshal(params, &p); err != nil {
		return Result{}, &PrimitiveError{Code: ErrValidation, Message: "invalid params: " + err.Error()}
	}
	if p.Command == "" {
		return Result{}, &PrimitiveError{Code: ErrValidation, Message: "command is required"}
	}

	shellParams, _ := json.Marshal(shellExecParams{
		Command: p.Command,
		Timeout: p.Timeout,
	})

	shellResult, err := v.shell.Execute(ctx, shellParams)
	if err != nil {
		return Result{}, err
	}
	shellData, _ := json.Marshal(shellResult.Data)
	var execResult shellExecResult
	_ = json.Unmarshal(shellData, &execResult)

	passed := execResult.ExitCode == 0 && !execResult.TimedOut
	summary := generateTestSummary(execResult.Stdout, execResult.Stderr, passed)

	return Result{
		Data: VerifyCommandResult{
			Passed:   passed,
			ExitCode: execResult.ExitCode,
			TimedOut: execResult.TimedOut,
			Stdout:   execResult.Stdout,
			Stderr:   execResult.Stderr,
			Summary:  summary,
		},
		Duration: execResult.DurationMs,
	}, nil
}
