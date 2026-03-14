package primitive

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// --------------------------------------------------------------------------
// shell.exec — Execute a shell command with timeout and output capture
// --------------------------------------------------------------------------

type ShellExec struct {
	workspaceDir    string
	defaultTimeout  int
	allowedCommands []string
}

func NewShellExec(workspaceDir string, options Options) *ShellExec {
	if options.DefaultTimeout <= 0 {
		options.DefaultTimeout = DefaultOptions().DefaultTimeout
	}
	return &ShellExec{
		workspaceDir:    newWorkspacePathResolver(workspaceDir).Root(),
		defaultTimeout:  options.DefaultTimeout,
		allowedCommands: append([]string(nil), options.AllowedCommands...),
	}
}

func (s *ShellExec) Name() string     { return "shell.exec" }
func (s *ShellExec) Category() string { return "shell" }
func (s *ShellExec) Schema() Schema {
	return Schema{
		Name:        "shell.exec",
		Description: "Execute a shell command with timeout protection and output capture",
		Input: json.RawMessage(`{
			"type":"object",
			"properties":{
				"command":{"type":"string"},
				"timeout_s":{"type":"integer"},
				"env":{"type":"object"}
			},
			"required":["command"]
		}`),
		Output: json.RawMessage(`{
			"type":"object",
			"properties":{
				"stdout":{"type":"string"},
				"stderr":{"type":"string"},
				"exit_code":{"type":"integer"},
				"duration_ms":{"type":"integer"},
				"timed_out":{"type":"boolean"}
			}
		}`),
	}
}

type shellExecParams struct {
	Command string            `json:"command"`
	Timeout int               `json:"timeout_s,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

type shellExecResult struct {
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	DurationMs int64  `json:"duration_ms"`
	TimedOut   bool   `json:"timed_out"`
}

func (s *ShellExec) Execute(ctx context.Context, params json.RawMessage) (Result, error) {
	var p shellExecParams
	if err := json.Unmarshal(params, &p); err != nil {
		return Result{}, &PrimitiveError{Code: ErrValidation, Message: "invalid params: " + err.Error()}
	}

	if p.Command == "" {
		return Result{}, &PrimitiveError{Code: ErrValidation, Message: "command is required"}
	}

	// Validate against whitelist (if enabled)
	if len(s.allowedCommands) > 0 {
		baseCmd := strings.Fields(p.Command)[0]
		allowed := false
		for _, ac := range s.allowedCommands {
			if ac == baseCmd {
				allowed = true
				break
			}
		}
		if !allowed {
			return Result{}, &PrimitiveError{
				Code:    ErrPermission,
				Message: fmt.Sprintf("command not in whitelist: %s", baseCmd),
			}
		}
	}

	// Set timeout
	timeout := time.Duration(s.defaultTimeout) * time.Second
	if p.Timeout > 0 {
		timeout = time.Duration(p.Timeout) * time.Second
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Create command
	cmd := exec.CommandContext(execCtx, "sh", "-c", p.Command)
	cmd.Dir = s.workspaceDir

	// Set environment
	if len(p.Env) > 0 {
		env := cmd.Environ()
		for k, v := range p.Env {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}
		cmd.Env = env
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	exitCode := 0
	timedOut := false

	if err != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			timedOut = true
			exitCode = -1
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return Result{}, &PrimitiveError{Code: ErrExecution, Message: err.Error()}
		}
	}

	result := shellExecResult{
		Stdout:     truncateOutput(stdout.String(), 10000),
		Stderr:     truncateOutput(stderr.String(), 5000),
		ExitCode:   exitCode,
		DurationMs: duration.Milliseconds(),
		TimedOut:   timedOut,
	}

	return Result{
		Data:     result,
		Duration: duration.Milliseconds(),
	}, nil
}

// truncateOutput limits output length to prevent context explosion.
func truncateOutput(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	half := maxLen / 2
	return s[:half] + "\n... [truncated] ...\n" + s[len(s)-half:]
}
