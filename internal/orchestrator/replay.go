package orchestrator

import (
	"encoding/json"
	"fmt"
	"log"
)

// --------------------------------------------------------------------------
// Replay: Task execution replay for debugging
// --------------------------------------------------------------------------

// ReplayMode controls how a task replay is executed.
type ReplayMode string

const (
	ReplayStepByStep ReplayMode = "step_by_step" // Pause after each step
	ReplaySkipPassed ReplayMode = "skip_passed"   // Skip steps that passed
	ReplayFull       ReplayMode = "full"           // Replay everything
)

// ReplayEntry represents a single step in a replay session.
type ReplayEntry struct {
	StepIndex    int             `json:"step_index"`
	Primitive    string          `json:"primitive"`
	Input        json.RawMessage `json:"input"`
	Output       json.RawMessage `json:"output,omitempty"`
	Status       string          `json:"status"`
	DurationMs   int64           `json:"duration_ms"`
	CheckpointID string          `json:"checkpoint_id,omitempty"`
	Error        string          `json:"error,omitempty"`
	Skipped      bool            `json:"skipped,omitempty"`
}

// Replay generates a replay trace from a completed (or paused) task.
func Replay(task *Task, mode ReplayMode) []ReplayEntry {
	if task == nil {
		return nil
	}

	entries := make([]ReplayEntry, 0, len(task.Steps))

	for i, step := range task.Steps {
		entry := ReplayEntry{
			StepIndex:    i + 1,
			Primitive:    step.Primitive,
			Input:        step.Input,
			Status:       string(step.Status),
			DurationMs:   step.Duration.Milliseconds(),
			CheckpointID: step.CheckpointID,
			Error:        step.Error,
		}

		// Marshal output if available
		if step.Result != nil {
			entry.Output, _ = json.Marshal(step.Result.Data)
		}

		// Skip passed steps in skip_passed mode
		if mode == ReplaySkipPassed && step.Status == StepPassed {
			entry.Skipped = true
		}

		entries = append(entries, entry)
	}

	return entries
}

// PrintReplay outputs a human-readable replay to stdout.
func PrintReplay(task *Task, mode ReplayMode) {
	entries := Replay(task, mode)

	fmt.Printf("\n╔══════════════════════════════════════════════════╗\n")
	fmt.Printf("║  Task Replay: %-34s ║\n", task.ID)
	fmt.Printf("║  Status: %-39s ║\n", task.Status)
	fmt.Printf("║  Steps:  %-39d ║\n", len(task.Steps))
	fmt.Printf("╚══════════════════════════════════════════════════╝\n\n")

	for _, entry := range entries {
		if entry.Skipped {
			log.Printf("  ⏭  Step %d: %s (skipped - passed)", entry.StepIndex, entry.Primitive)
			continue
		}

		statusIcon := "⏳"
		switch entry.Status {
		case string(StepPassed):
			statusIcon = "✅"
		case string(StepFailed):
			statusIcon = "❌"
		case string(StepRolledBack):
			statusIcon = "↩️"
		case string(StepSkipped):
			statusIcon = "⏭"
		}

		fmt.Printf("  %s Step %d: %s (%dms)\n", statusIcon, entry.StepIndex, entry.Primitive, entry.DurationMs)

		if entry.Error != "" {
			fmt.Printf("     Error: %s\n", TruncateErrorForLLM(entry.Error, 200))
		}

		if mode == ReplayStepByStep {
			fmt.Printf("     Input: %s\n", truncateJSON(entry.Input, 100))
			if entry.Output != nil {
				fmt.Printf("     Output: %s\n", truncateJSON(entry.Output, 100))
			}
		}
		fmt.Println()
	}
}

func truncateJSON(data json.RawMessage, maxLen int) string {
	s := string(data)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
