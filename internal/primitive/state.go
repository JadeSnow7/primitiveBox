package primitive

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"primitivebox/internal/eventing"
)

// --------------------------------------------------------------------------
// state.checkpoint — Create a Git-based snapshot of the workspace
// --------------------------------------------------------------------------

type StateCheckpoint struct {
	workspaceDir string
}

func NewStateCheckpoint(workspaceDir string) *StateCheckpoint {
	return &StateCheckpoint{workspaceDir: newWorkspacePathResolver(workspaceDir).Root()}
}

func (s *StateCheckpoint) Name() string     { return "state.checkpoint" }
func (s *StateCheckpoint) Category() string { return "state" }
func (s *StateCheckpoint) Schema() Schema {
	return Schema{
		Name:        "state.checkpoint",
		Description: "Create a Git-based snapshot of the current workspace state",
		Input:       json.RawMessage(`{"type":"object","properties":{"label":{"type":"string"}}}`),
		Output:      json.RawMessage(`{"type":"object","properties":{"checkpoint_id":{"type":"string"},"timestamp":{"type":"string"}}}`),
	}
}

type checkpointParams struct {
	Label string `json:"label,omitempty"`
}

type CheckpointResult struct {
	CheckpointID string `json:"checkpoint_id"`
	Timestamp    string `json:"timestamp"`
	Label        string `json:"label,omitempty"`
}

func (s *StateCheckpoint) Execute(ctx context.Context, params json.RawMessage) (Result, error) {
	var p checkpointParams
	if params != nil {
		if err := json.Unmarshal(params, &p); err != nil {
			return Result{}, &PrimitiveError{Code: ErrValidation, Message: "invalid params: " + err.Error()}
		}
	}

	label := p.Label
	if label == "" {
		label = fmt.Sprintf("checkpoint-%d", time.Now().UnixMilli())
	}

	// Ensure git is initialized
	if err := ensureGitInit(s.workspaceDir); err != nil {
		return Result{}, &PrimitiveError{Code: ErrExecution, Message: "git init failed: " + err.Error()}
	}

	// Stage all changes
	if err := gitExec(s.workspaceDir, "add", "-A"); err != nil {
		return Result{}, &PrimitiveError{Code: ErrExecution, Message: "git add failed: " + err.Error()}
	}

	// Commit with checkpoint label
	commitMsg := fmt.Sprintf("checkpoint: %s", label)
	if err := gitExec(s.workspaceDir, "commit", "-m", commitMsg, "--allow-empty"); err != nil {
		return Result{}, &PrimitiveError{Code: ErrExecution, Message: "git commit failed: " + err.Error()}
	}

	// Get commit hash
	hash, err := gitOutput(s.workspaceDir, "rev-parse", "HEAD")
	if err != nil {
		return Result{}, &PrimitiveError{Code: ErrExecution, Message: "cannot get commit hash: " + err.Error()}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	result := CheckpointResult{
		CheckpointID: strings.TrimSpace(hash),
		Timestamp:    now,
		Label:        label,
	}
	eventing.Emit(ctx, eventing.Event{
		Type:    "checkpoint.created",
		Source:  "primitive",
		Method:  s.Name(),
		Message: label,
		Data:    eventing.MustJSON(result),
	})

	return Result{
		Data: result,
	}, nil
}

// --------------------------------------------------------------------------
// state.restore — Restore workspace to a previous checkpoint
// --------------------------------------------------------------------------

type StateRestore struct {
	workspaceDir string
}

func NewStateRestore(workspaceDir string) *StateRestore {
	return &StateRestore{workspaceDir: newWorkspacePathResolver(workspaceDir).Root()}
}

func (s *StateRestore) Name() string     { return "state.restore" }
func (s *StateRestore) Category() string { return "state" }
func (s *StateRestore) Schema() Schema {
	return Schema{
		Name:        "state.restore",
		Description: "Restore workspace files to a previous Git-based checkpoint",
		Input:       json.RawMessage(`{"type":"object","properties":{"checkpoint_id":{"type":"string"}},"required":["checkpoint_id"]}`),
		Output:      json.RawMessage(`{"type":"object","properties":{"restored_to":{"type":"string"},"files_changed":{"type":"integer"},"restored_files":{"type":"array","items":{"type":"string"}}}}`),
	}
}

type restoreParams struct {
	CheckpointID string `json:"checkpoint_id"`
}

type RestoreResult struct {
	RestoredTo    string   `json:"restored_to"`
	FilesChanged  int      `json:"files_changed"`
	RestoredFiles []string `json:"restored_files,omitempty"`
}

func (s *StateRestore) Execute(ctx context.Context, params json.RawMessage) (Result, error) {
	var p restoreParams
	if err := json.Unmarshal(params, &p); err != nil {
		return Result{}, &PrimitiveError{Code: ErrValidation, Message: "invalid params: " + err.Error()}
	}

	if p.CheckpointID == "" {
		return Result{}, &PrimitiveError{Code: ErrValidation, Message: "checkpoint_id is required"}
	}

	target := p.CheckpointID
	if target == "latest" {
		target = "HEAD~1"
	}

	resolvedTarget, err := gitOutput(s.workspaceDir, "rev-parse", "--verify", target)
	if err != nil {
		return Result{}, &PrimitiveError{Code: ErrExecution, Message: "restore failed: " + err.Error()}
	}
	resolvedTarget = strings.TrimSpace(resolvedTarget)

	trackedFiles := gitLines(s.workspaceDir, "diff", "--name-only", resolvedTarget, "--", ".")
	untrackedFiles, err := previewCleanedFiles(s.workspaceDir)
	if err != nil {
		return Result{}, &PrimitiveError{Code: ErrExecution, Message: "restore preview failed: " + err.Error()}
	}

	restoredFiles := appendUnique(nil, trackedFiles...)
	restoredFiles = appendUnique(restoredFiles, untrackedFiles...)

	if err := ensureGitRestoreUnlocked(s.workspaceDir); err != nil {
		return Result{}, &PrimitiveError{Code: ErrExecution, Message: "restore failed: " + err.Error()}
	}
	if err := gitExec(s.workspaceDir, "restore", "--source", resolvedTarget, "--staged", "--worktree", "."); err != nil {
		return Result{}, &PrimitiveError{Code: ErrExecution, Message: "restore failed: " + err.Error()}
	}
	if err := gitExec(s.workspaceDir, "clean", "-fdx"); err != nil {
		return Result{}, &PrimitiveError{Code: ErrExecution, Message: "restore failed: " + err.Error()}
	}

	result := RestoreResult{
		RestoredTo:    resolvedTarget,
		FilesChanged:  len(restoredFiles),
		RestoredFiles: restoredFiles,
	}
	eventing.Emit(ctx, eventing.Event{
		Type:    "checkpoint.restored",
		Source:  "primitive",
		Method:  s.Name(),
		Message: resolvedTarget,
		Data:    eventing.MustJSON(result),
	})

	return Result{Data: result}, nil
}

// --------------------------------------------------------------------------
// state.list — List all checkpoints
// --------------------------------------------------------------------------

type StateList struct {
	workspaceDir string
}

func NewStateList(workspaceDir string) *StateList {
	return &StateList{workspaceDir: newWorkspacePathResolver(workspaceDir).Root()}
}

func (s *StateList) Name() string     { return "state.list" }
func (s *StateList) Category() string { return "state" }
func (s *StateList) Schema() Schema {
	return Schema{
		Name:        "state.list",
		Description: "List all workspace checkpoints",
		Input:       json.RawMessage(`{"type":"object","properties":{}}`),
		Output:      json.RawMessage(`{"type":"object","properties":{"checkpoints":{"type":"array"}}}`),
	}
}

type checkpointEntry struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Timestamp string `json:"timestamp"`
}

func (s *StateList) Execute(ctx context.Context, params json.RawMessage) (Result, error) {
	// Get log of checkpoint commits
	output, err := gitOutput(s.workspaceDir, "log", "--oneline", "--format=%H|%s|%ci", "-20")
	if err != nil {
		return Result{Data: map[string]any{"checkpoints": []checkpointEntry{}}}, nil
	}

	var checkpoints []checkpointEntry
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) < 3 {
			continue
		}
		label := strings.TrimPrefix(parts[1], "checkpoint: ")
		checkpoints = append(checkpoints, checkpointEntry{
			ID:        parts[0],
			Label:     label,
			Timestamp: parts[2],
		})
	}

	return Result{Data: map[string]any{"checkpoints": checkpoints}}, nil
}

// --------------------------------------------------------------------------
// Git Helper Functions
// --------------------------------------------------------------------------

func ensureGitInit(dir string) error {
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git not found in PATH")
	}
	// Check if already initialized
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = dir
	if err := cmd.Run(); err == nil {
		return nil // Already a git repo
	}
	// Initialize with a standard .gitignore
	if err := gitExec(dir, "init"); err != nil {
		return err
	}
	// Create .gitignore for common build artifacts
	gitignore := `# Auto-generated by PrimitiveBox
node_modules/
__pycache__/
*.pyc
*.pyo
.pytest_cache/
*.o
*.so
*.dylib
.DS_Store
*.class
target/
dist/
build/
.env
`
	gitignorePath := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte(gitignore), 0o644); err != nil {
		return fmt.Errorf("cannot write .gitignore: %w", err)
	}

	// Configure git user for commits
	_ = gitExec(dir, "config", "user.email", "primitivebox@local")
	_ = gitExec(dir, "config", "user.name", "PrimitiveBox")

	// Initial commit
	if err := gitExec(dir, "add", "-A"); err != nil {
		return err
	}
	if err := gitExec(dir, "commit", "-m", "checkpoint: initial", "--allow-empty"); err != nil {
		return err
	}

	return nil
}

func gitExec(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %s (output: %s)", strings.Join(args, " "), err, string(output))
	}
	return nil
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), err)
	}
	return string(output), nil
}

func gitLines(dir string, args ...string) []string {
	output, err := gitOutput(dir, args...)
	if err != nil {
		return nil
	}

	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

func previewCleanedFiles(dir string) ([]string, error) {
	output, err := gitOutput(dir, "clean", "-ndx")
	if err != nil {
		return nil, err
	}

	var files []string
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		const prefix = "Would remove "
		if strings.HasPrefix(line, prefix) {
			files = append(files, strings.TrimSpace(strings.TrimPrefix(line, prefix)))
		}
	}

	return files, nil
}

func ensureGitRestoreUnlocked(dir string) error {
	lockFiles := []string{
		filepath.Join(dir, ".git", "index.lock"),
		filepath.Join(dir, ".git", "HEAD.lock"),
	}
	for _, lockPath := range lockFiles {
		if _, err := os.Stat(lockPath); err == nil {
			return fmt.Errorf("git repository is busy (%s exists)", filepath.Base(lockPath))
		}
	}
	return nil
}

func appendUnique(dst []string, items ...string) []string {
	for _, item := range items {
		if item == "" || slices.Contains(dst, item) {
			continue
		}
		dst = append(dst, item)
	}
	return dst
}
