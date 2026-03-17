package primitive

import (
	"context"
	"encoding/json"
	"fmt"
)

// --------------------------------------------------------------------------
// macro.safe_edit — Atomically edit + verify + auto-restore on failure
// --------------------------------------------------------------------------
//
// This compound primitive replaces the 4-hop RPC sequence:
//   state.checkpoint → fs.write → verify.test → (state.restore on fail)
// with a single HTTP call, reducing latency and LLM function invocations.

type MacroSafeEdit struct {
	resolver workspacePathResolver
	options  Options
}

func NewMacroSafeEdit(workspaceDir string, options Options) *MacroSafeEdit {
	return &MacroSafeEdit{
		resolver: newWorkspacePathResolver(workspaceDir),
		options:  options,
	}
}

func (m *MacroSafeEdit) Name() string     { return "macro.safe_edit" }
func (m *MacroSafeEdit) Category() string { return "macro" }
func (m *MacroSafeEdit) Schema() Schema {
	return Schema{
		Name:        "macro.safe_edit",
		Description: "Atomically checkpoint → write → verify → restore-on-fail. Replaces 4 separate RPC calls with one compound operation.",
		Input: json.RawMessage(`{
			"type":"object",
			"properties":{
				"path":{"type":"string","description":"File to edit (relative to workspace)"},
				"content":{"type":"string","description":"New file content (overwrite mode)"},
				"mode":{"type":"string","enum":["overwrite","search_replace"],"default":"overwrite"},
				"search":{"type":"string","description":"Text to find (search_replace mode)"},
				"replace":{"type":"string","description":"Replacement text (search_replace mode)"},
				"test_command":{"type":"string","description":"Command to run to verify the edit (e.g. 'pytest tests/')"},
				"checkpoint_label":{"type":"string","description":"Label for auto-created checkpoint (default: 'macro.safe_edit')"}
			},
			"required":["path","test_command"]
		}`),
		Output: json.RawMessage(`{
			"type":"object",
			"properties":{
				"passed":{"type":"boolean"},
				"rolled_back":{"type":"boolean"},
				"checkpoint_id":{"type":"string"},
				"test_output":{"type":"string"},
				"diff":{"type":"string"}
			}
		}`),
	}
}

type macroSafeEditParams struct {
	Path            string `json:"path"`
	Content         string `json:"content,omitempty"`
	Mode            string `json:"mode,omitempty"`
	Search          string `json:"search,omitempty"`
	Replace         string `json:"replace,omitempty"`
	TestCommand     string `json:"test_command"`
	CheckpointLabel string `json:"checkpoint_label,omitempty"`
}

func (m *MacroSafeEdit) Execute(ctx context.Context, params json.RawMessage) (Result, error) {
	var p macroSafeEditParams
	if err := json.Unmarshal(params, &p); err != nil {
		return Result{}, &PrimitiveError{Code: ErrValidation, Message: "invalid params: " + err.Error()}
	}
	if p.Path == "" {
		return Result{}, &PrimitiveError{Code: ErrValidation, Message: "path is required"}
	}
	if p.TestCommand == "" {
		return Result{}, &PrimitiveError{Code: ErrValidation, Message: "test_command is required"}
	}

	workspaceDir := m.resolver.Root()
	label := p.CheckpointLabel
	if label == "" {
		label = "macro.safe_edit"
	}

	// ── Step 1: Checkpoint ────────────────────────────────────────────────
	checkpointer := NewStateCheckpoint(workspaceDir)
	cpParams, _ := json.Marshal(map[string]any{"label": label})
	cpResult, err := checkpointer.Execute(ctx, cpParams)
	if err != nil {
		return Result{}, fmt.Errorf("checkpoint failed: %w", err)
	}
	cpData, _ := cpResult.Data.(CheckpointResult)
	checkpointID := cpData.CheckpointID

	// ── Step 2: Write ─────────────────────────────────────────────────────
	writer := NewFSWrite(workspaceDir)
	writeParamsMap := map[string]any{
		"path": p.Path,
		"mode": p.Mode,
	}
	if p.Mode == "search_replace" {
		writeParamsMap["search"] = p.Search
		writeParamsMap["replace"] = p.Replace
	} else {
		writeParamsMap["content"] = p.Content
	}
	writeParams, _ := json.Marshal(writeParamsMap)
	writeResult, err := writer.Execute(ctx, writeParams)
	if err != nil {
		return Result{Data: map[string]any{
			"passed":        false,
			"rolled_back":   false,
			"checkpoint_id": checkpointID,
			"test_output":   "",
			"error":         err.Error(),
		}}, nil
	}
	diff := writeResult.Diff
	if diff == "" {
		if payload, ok := writeResult.Data.(map[string]any); ok {
			if rawDiff, ok := payload["diff"].(string); ok {
				diff = rawDiff
			}
		}
	}

	// ── Step 3: Verify ────────────────────────────────────────────────────
	verifier := NewVerifyTest(workspaceDir, m.options)
	verifyParams, _ := json.Marshal(map[string]any{"command": p.TestCommand})
	verifyResult, verifyErr := verifier.Execute(ctx, verifyParams)

	// Extract test output for reporting
	testOutput := ""
	passed := false
	if verifyErr == nil {
		if d, ok := verifyResult.Data.(VerifyTestResult); ok {
			passed = d.Passed
			testOutput = d.Output
		}
	} else {
		testOutput = verifyErr.Error()
	}

	// ── Step 4: Restore on failure ────────────────────────────────────────
	rolledBack := false
	if !passed && checkpointID != "" {
		restorer := NewStateRestore(workspaceDir)
		restoreParams, _ := json.Marshal(map[string]any{"checkpoint_id": checkpointID})
		if _, restoreErr := restorer.Execute(ctx, restoreParams); restoreErr == nil {
			rolledBack = true
		}
	}

	return Result{Data: map[string]any{
		"passed":        passed,
		"rolled_back":   rolledBack,
		"checkpoint_id": checkpointID,
		"test_output":   truncate(testOutput, 4000),
		"diff":          diff,
	}}, nil
}
