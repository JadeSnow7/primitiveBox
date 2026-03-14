package orchestrator

import (
	"sync"
)

// --------------------------------------------------------------------------
// StateTracker: Task state management and execution history
// --------------------------------------------------------------------------

// StateTracker maintains the state of all tasks and their execution chains.
type StateTracker struct {
	mu    sync.RWMutex
	tasks map[string]*Task
}

// NewStateTracker creates a new state tracker.
func NewStateTracker() *StateTracker {
	return &StateTracker{
		tasks: make(map[string]*Task),
	}
}

// TrackTask registers or updates a task in the tracker.
func (st *StateTracker) TrackTask(task *Task) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.tasks[task.ID] = task
}

// GetTask retrieves a task by ID.
func (st *StateTracker) GetTask(taskID string) (*Task, bool) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	t, ok := st.tasks[taskID]
	return t, ok
}

// ListTasks returns all tracked tasks.
func (st *StateTracker) ListTasks() []*Task {
	st.mu.RLock()
	defer st.mu.RUnlock()
	result := make([]*Task, 0, len(st.tasks))
	for _, t := range st.tasks {
		result = append(result, t)
	}
	return result
}

// GetTaskHistory returns the step history for a task as a serializable structure.
func (st *StateTracker) GetTaskHistory(taskID string) []StepRecord {
	st.mu.RLock()
	defer st.mu.RUnlock()

	task, ok := st.tasks[taskID]
	if !ok {
		return nil
	}

	records := make([]StepRecord, 0, len(task.Steps))
	for _, step := range task.Steps {
		records = append(records, StepRecord{
			StepID:       step.ID,
			Primitive:    step.Primitive,
			Status:       string(step.Status),
			DurationMs:   step.Duration.Milliseconds(),
			CheckpointID: step.CheckpointID,
			Error:        step.Error,
		})
	}
	return records
}

// StepRecord is a serializable view of a step's execution for replay and audit.
type StepRecord struct {
	StepID       string `json:"step_id"`
	Primitive    string `json:"primitive"`
	Status       string `json:"status"`
	DurationMs   int64  `json:"duration_ms"`
	CheckpointID string `json:"checkpoint_id,omitempty"`
	Error        string `json:"error,omitempty"`
}
