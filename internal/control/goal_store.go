package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"primitivebox/internal/eventing"
)

// Goal status constants.
type GoalStatus string

const (
	GoalCreated   GoalStatus = "created"
	GoalExecuting GoalStatus = "executing"
	GoalVerifying GoalStatus = "verifying"
	GoalCompleted GoalStatus = "completed"
	GoalFailed    GoalStatus = "failed"
	GoalPaused    GoalStatus = "paused"
)

// Goal step status constants.
type GoalStepStatus string

const (
	GoalStepPending        GoalStepStatus = "pending"
	GoalStepRunning        GoalStepStatus = "running"
	GoalStepPassed         GoalStepStatus = "passed"
	GoalStepFailed         GoalStepStatus = "failed"
	GoalStepSkipped        GoalStepStatus = "skipped"
	GoalStepAwaitingReview GoalStepStatus = "awaiting_review"
	GoalStepRolledBack     GoalStepStatus = "rolled_back"
)

// Goal verification status constants.
type GoalVerificationStatus string

const (
	GoalVerificationPending GoalVerificationStatus = "pending"
	GoalVerificationRunning GoalVerificationStatus = "running"
	GoalVerificationPassed  GoalVerificationStatus = "passed"
	GoalVerificationFailed  GoalVerificationStatus = "failed"
)

// GoalBindingType is one of the three supported cross-package binding kinds.
type GoalBindingType string

const (
	GoalBindingServiceEndpoint GoalBindingType = "service_endpoint"
	GoalBindingNetworkExposure GoalBindingType = "network_exposure"
	GoalBindingCredential      GoalBindingType = "credential"
)

// GoalBindingStatus tracks binding lifecycle.
type GoalBindingStatus string

const (
	GoalBindingPending  GoalBindingStatus = "pending"
	GoalBindingResolved GoalBindingStatus = "resolved"
	GoalBindingFailed   GoalBindingStatus = "failed"
)

// Goal event type constants.
const (
	EventGoalCreated             = "goal.created"
	EventGoalStatusChanged       = "goal.status_changed"
	EventGoalVerificationStarted = "goal.verification_started"
	EventGoalVerificationPassed  = "goal.verification_passed"
	EventGoalVerificationFailed  = "goal.verification_failed"
	EventGoalReplayStarted       = "goal.replay_started"
	EventGoalReplayCompleted     = "goal.replay_completed"
	EventGoalBindingResolved     = "goal.binding_resolved"
	EventGoalReviewRequested     = "goal.review_requested"
	EventGoalReviewApproved      = "goal.review_approved"
	EventGoalReviewRejected      = "goal.review_rejected"
	EventGoalResumed             = "goal.resumed"
)

// GoalReviewStatus tracks the lifecycle of a step-level review request.
type GoalReviewStatus string

const (
	GoalReviewPending  GoalReviewStatus = "pending"
	GoalReviewApproved GoalReviewStatus = "approved"
	GoalReviewRejected GoalReviewStatus = "rejected"
)

// ErrReviewConflict is returned by DecideGoalReview when the review already
// has the opposite decision (e.g. approving a rejected review).
var ErrReviewConflict = errors.New("conflicting review decision")

// GoalReview records a human-in-the-loop gate for a high-risk goal step.
type GoalReview struct {
	ID             string           `json:"id"`
	GoalID         string           `json:"goal_id"`
	StepID         string           `json:"step_id"`
	Status         GoalReviewStatus `json:"status"`
	Primitive      string           `json:"primitive"`
	RiskLevel      string           `json:"risk_level"`
	Reversible     bool             `json:"reversible"`
	SideEffect     string           `json:"side_effect,omitempty"`
	DecisionReason string           `json:"decision_reason,omitempty"`
	CreatedAt      int64            `json:"created_at"`
	UpdatedAt      int64            `json:"updated_at"`
}

// Goal is the top-level composition task unit.
type Goal struct {
	ID          string     `json:"id"`
	Description string     `json:"description"`
	Status      GoalStatus `json:"status"`
	Packages    []string   `json:"packages"`
	SandboxIDs  []string   `json:"sandbox_ids"`
	CreatedAt   int64      `json:"created_at"` // unix ms
	UpdatedAt   int64      `json:"updated_at"` // unix ms
}

// GoalStep is a single primitive execution within a goal.
type GoalStep struct {
	ID           string          `json:"id"`
	GoalID       string          `json:"goal_id"`
	Primitive    string          `json:"primitive"`
	Input        json.RawMessage `json:"input"`
	Output       json.RawMessage `json:"output,omitempty"`
	Status       GoalStepStatus  `json:"status"`
	CheckpointID string          `json:"checkpoint_id,omitempty"`
	RiskLevel    string          `json:"risk_level,omitempty"` // "low" (default) or "high"
	Reversible   bool            `json:"reversible"`
	Seq          int             `json:"seq"`
	CreatedAt    int64           `json:"created_at"`
	UpdatedAt    int64           `json:"updated_at"`
}

// GoalVerification tracks a single verification run for a goal.
// CheckType and CheckParams describe how to execute the verification:
//   - "primitive_call": call a primitive through the host or sandbox RPC path
//   - "http_probe":     perform an explicit HTTP reachability probe
//   - "json_assert":    assert JSON data from a step or prior verification
type GoalVerification struct {
	ID          string                 `json:"id"`
	GoalID      string                 `json:"goal_id"`
	StepID      string                 `json:"step_id,omitempty"`
	Status      GoalVerificationStatus `json:"status"`
	Verdict     string                 `json:"verdict,omitempty"`
	Evidence    json.RawMessage        `json:"evidence,omitempty"`
	CheckType   string                 `json:"check_type,omitempty"`
	CheckParams json.RawMessage        `json:"check_params,omitempty"`
	CreatedAt   int64                  `json:"created_at"`
	UpdatedAt   int64                  `json:"updated_at"`
}

// GoalBinding is a typed cross-package dependency declaration within a goal.
type GoalBinding struct {
	ID            string            `json:"id"`
	GoalID        string            `json:"goal_id"`
	BindingType   GoalBindingType   `json:"binding_type"`
	SourceRef     string            `json:"source_ref"`
	TargetRef     string            `json:"target_ref"`
	Status        GoalBindingStatus `json:"status"`
	ResolvedValue string            `json:"resolved_value,omitempty"`
	FailureReason string            `json:"failure_reason,omitempty"`
	Metadata      json.RawMessage   `json:"metadata,omitempty"`
	CreatedAt     int64             `json:"created_at"`
	UpdatedAt     int64             `json:"updated_at"`
}

// GoalReplayEntry represents a single step in a goal replay result.
type GoalReplayEntry struct {
	Seq          int             `json:"seq"`
	StepID       string          `json:"step_id,omitempty"`
	Primitive    string          `json:"primitive"`
	Input        json.RawMessage `json:"input,omitempty"`
	Output       json.RawMessage `json:"output,omitempty"`
	Status       string          `json:"status"`
	CheckpointID string          `json:"checkpoint_id,omitempty"`
	Skipped      bool            `json:"skipped,omitempty"`
}

// GoalReplayResult is returned by the replay endpoint.
type GoalReplayResult struct {
	GoalID  string            `json:"goal_id"`
	Mode    string            `json:"mode"`
	Entries []GoalReplayEntry `json:"entries"`
}

// GoalStore persists and retrieves goal composition state.
// Every mutating method performs the SQLite write first, then publishes the event.
type GoalStore interface {
	CreateGoal(ctx context.Context, g *Goal, bus *eventing.Bus) error
	// CreateGoalFull atomically inserts a goal and all its initial steps in a
	// single transaction. It emits goal.created after the commit.
	CreateGoalFull(ctx context.Context, g *Goal, steps []*GoalStep, bus *eventing.Bus) error
	GetGoal(ctx context.Context, id string) (*Goal, bool, error)
	ListGoals(ctx context.Context) ([]*Goal, error)
	UpdateGoalStatus(ctx context.Context, id string, status GoalStatus, bus *eventing.Bus) error

	AppendGoalStep(ctx context.Context, step *GoalStep, bus *eventing.Bus) error
	UpdateGoalStepStatus(ctx context.Context, stepID string, status GoalStepStatus, output json.RawMessage, bus *eventing.Bus) error
	ListGoalSteps(ctx context.Context, goalID string) ([]*GoalStep, error)

	AppendGoalVerification(ctx context.Context, v *GoalVerification, bus *eventing.Bus) error
	UpdateGoalVerification(ctx context.Context, id string, status GoalVerificationStatus, verdict string, evidence json.RawMessage, bus *eventing.Bus) error
	ListGoalVerifications(ctx context.Context, goalID string) ([]*GoalVerification, error)

	AppendGoalBinding(ctx context.Context, b *GoalBinding, bus *eventing.Bus) error
	ResolveGoalBinding(ctx context.Context, bindingID, resolvedValue string, bus *eventing.Bus) error
	FailGoalBinding(ctx context.Context, bindingID, reason string, bus *eventing.Bus) error
	ListGoalBindings(ctx context.Context, goalID string) ([]*GoalBinding, error)

	// ReplayGoal loads all steps for a goal ordered by seq and emits replay events.
	ReplayGoal(ctx context.Context, goalID string, bus *eventing.Bus) error

	// Review CRUD — human-in-the-loop gates for high-risk steps.
	CreateGoalReview(ctx context.Context, r *GoalReview, bus *eventing.Bus) error
	// DecideGoalReview applies an approve or reject decision. Returns nil on
	// same-decision repeat (idempotent). Returns ErrReviewConflict when the
	// requested decision contradicts the existing one.
	DecideGoalReview(ctx context.Context, reviewID string, status GoalReviewStatus, reason string, bus *eventing.Bus) error
	ListGoalReviews(ctx context.Context, goalID string) ([]*GoalReview, error)
	GetGoalReview(ctx context.Context, reviewID string) (*GoalReview, bool, error)
}

// SQLiteGoalStore implements GoalStore backed by a shared *sql.DB.
type SQLiteGoalStore struct {
	db *sql.DB
}

// NewSQLiteGoalStore creates a goal store backed by the given database.
func NewSQLiteGoalStore(db *sql.DB) *SQLiteGoalStore {
	return &SQLiteGoalStore{db: db}
}

// ── Goal CRUD ─────────────────────────────────────────────────────────────────

func (s *SQLiteGoalStore) CreateGoal(ctx context.Context, g *Goal, bus *eventing.Bus) error {
	pkgsJSON, err := json.Marshal(g.Packages)
	if err != nil {
		return fmt.Errorf("marshal packages: %w", err)
	}
	sbIDsJSON, err := json.Marshal(g.SandboxIDs)
	if err != nil {
		return fmt.Errorf("marshal sandbox_ids: %w", err)
	}
	now := time.Now().UnixMilli()
	if g.CreatedAt == 0 {
		g.CreatedAt = now
	}
	if g.UpdatedAt == 0 {
		g.UpdatedAt = now
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO goals (id, description, status, packages_json, sandbox_ids_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, g.ID, g.Description, string(g.Status), string(pkgsJSON), string(sbIDsJSON), g.CreatedAt, g.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert goal: %w", err)
	}
	publishGoalEvent(ctx, bus, EventGoalCreated, g.ID, g)
	return nil
}

// CreateGoalFull atomically inserts the goal and all provided initial steps.
// If steps is empty, it behaves identically to CreateGoal.
func (s *SQLiteGoalStore) CreateGoalFull(ctx context.Context, g *Goal, steps []*GoalStep, bus *eventing.Bus) error {
	pkgsJSON, err := json.Marshal(g.Packages)
	if err != nil {
		return fmt.Errorf("marshal packages: %w", err)
	}
	sbIDsJSON, err := json.Marshal(g.SandboxIDs)
	if err != nil {
		return fmt.Errorf("marshal sandbox_ids: %w", err)
	}
	now := time.Now().UnixMilli()
	if g.CreatedAt == 0 {
		g.CreatedAt = now
	}
	if g.UpdatedAt == 0 {
		g.UpdatedAt = now
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	_, err = tx.ExecContext(ctx, `
		INSERT INTO goals (id, description, status, packages_json, sandbox_ids_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, g.ID, g.Description, string(g.Status), string(pkgsJSON), string(sbIDsJSON), g.CreatedAt, g.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert goal: %w", err)
	}

	for i, step := range steps {
		inputJSON := step.Input
		if inputJSON == nil {
			inputJSON = json.RawMessage("{}")
		}
		if step.CreatedAt == 0 {
			step.CreatedAt = now
		}
		if step.UpdatedAt == 0 {
			step.UpdatedAt = now
		}
		if step.Seq == 0 {
			step.Seq = i + 1
		}
		riskLevel := step.RiskLevel
		if riskLevel == "" {
			riskLevel = "low"
		}
		reversibleInt := 1
		if !step.Reversible {
			reversibleInt = 0
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO goal_steps (id, goal_id, primitive, input_json, status, checkpoint_id, risk_level, reversible, seq, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, step.ID, step.GoalID, step.Primitive, string(inputJSON),
			string(step.Status), step.CheckpointID, riskLevel, reversibleInt, step.Seq, step.CreatedAt, step.UpdatedAt)
		if err != nil {
			return fmt.Errorf("insert goal step %d: %w", i, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	publishGoalEvent(ctx, bus, EventGoalCreated, g.ID, g)
	return nil
}

func (s *SQLiteGoalStore) GetGoal(ctx context.Context, id string) (*Goal, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, description, status, packages_json, sandbox_ids_json, created_at, updated_at
		FROM goals WHERE id = ?
	`, id)
	g, err := scanGoal(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return g, true, nil
}

func (s *SQLiteGoalStore) ListGoals(ctx context.Context) ([]*Goal, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, description, status, packages_json, sandbox_ids_json, created_at, updated_at
		FROM goals ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list goals: %w", err)
	}
	defer rows.Close()
	var result []*Goal
	for rows.Next() {
		g, err := scanGoal(rows.Scan)
		if err != nil {
			return nil, err
		}
		result = append(result, g)
	}
	return result, rows.Err()
}

func (s *SQLiteGoalStore) UpdateGoalStatus(ctx context.Context, id string, status GoalStatus, bus *eventing.Bus) error {
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `
		UPDATE goals SET status = ?, updated_at = ? WHERE id = ?
	`, string(status), now, id)
	if err != nil {
		return fmt.Errorf("update goal status: %w", err)
	}
	publishGoalEvent(ctx, bus, EventGoalStatusChanged, id, map[string]any{"id": id, "status": status})
	return nil
}

func scanGoal(scan func(dest ...any) error) (*Goal, error) {
	var (
		id, description, status string
		pkgsJSON, sbIDsJSON     string
		createdAt, updatedAt    int64
	)
	if err := scan(&id, &description, &status, &pkgsJSON, &sbIDsJSON, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	g := &Goal{
		ID:          id,
		Description: description,
		Status:      GoalStatus(status),
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
	}
	if pkgsJSON != "" && pkgsJSON != "null" {
		if err := json.Unmarshal([]byte(pkgsJSON), &g.Packages); err != nil {
			return nil, fmt.Errorf("unmarshal packages for %s: %w", id, err)
		}
	}
	if g.Packages == nil {
		g.Packages = []string{}
	}
	if sbIDsJSON != "" && sbIDsJSON != "null" {
		if err := json.Unmarshal([]byte(sbIDsJSON), &g.SandboxIDs); err != nil {
			return nil, fmt.Errorf("unmarshal sandbox_ids for %s: %w", id, err)
		}
	}
	if g.SandboxIDs == nil {
		g.SandboxIDs = []string{}
	}
	return g, nil
}

// ── GoalStep CRUD ─────────────────────────────────────────────────────────────

func (s *SQLiteGoalStore) AppendGoalStep(ctx context.Context, step *GoalStep, bus *eventing.Bus) error {
	inputJSON := step.Input
	if inputJSON == nil {
		inputJSON = json.RawMessage("{}")
	}
	now := time.Now().UnixMilli()
	if step.CreatedAt == 0 {
		step.CreatedAt = now
	}
	if step.UpdatedAt == 0 {
		step.UpdatedAt = now
	}
	riskLevel := step.RiskLevel
	if riskLevel == "" {
		riskLevel = "low"
	}
	reversibleInt := 1
	if !step.Reversible {
		reversibleInt = 0
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO goal_steps (id, goal_id, primitive, input_json, status, checkpoint_id, risk_level, reversible, seq, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, step.ID, step.GoalID, step.Primitive, string(inputJSON),
		string(step.Status), step.CheckpointID, riskLevel, reversibleInt, step.Seq, step.CreatedAt, step.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert goal step: %w", err)
	}
	publishGoalEvent(ctx, bus, "goal.step_appended", step.GoalID, step)
	return nil
}

func (s *SQLiteGoalStore) UpdateGoalStepStatus(ctx context.Context, stepID string, status GoalStepStatus, output json.RawMessage, bus *eventing.Bus) error {
	now := time.Now().UnixMilli()
	var outputStr *string
	if output != nil {
		str := string(output)
		outputStr = &str
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE goal_steps SET status = ?, output_json = ?, updated_at = ? WHERE id = ?
	`, string(status), outputStr, now, stepID)
	if err != nil {
		return fmt.Errorf("update goal step status: %w", err)
	}
	publishGoalEvent(ctx, bus, "goal.step_updated", stepID, map[string]any{"id": stepID, "status": status})
	return nil
}

func (s *SQLiteGoalStore) ListGoalSteps(ctx context.Context, goalID string) ([]*GoalStep, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, goal_id, primitive, input_json, output_json, status, checkpoint_id, risk_level, reversible, seq, created_at, updated_at
		FROM goal_steps WHERE goal_id = ? ORDER BY seq ASC
	`, goalID)
	if err != nil {
		return nil, fmt.Errorf("list goal steps: %w", err)
	}
	defer rows.Close()
	var result []*GoalStep
	for rows.Next() {
		step, err := scanGoalStep(rows.Scan)
		if err != nil {
			return nil, err
		}
		result = append(result, step)
	}
	return result, rows.Err()
}

func scanGoalStep(scan func(dest ...any) error) (*GoalStep, error) {
	var (
		id, goalID, primitive, inputJSON string
		outputJSON                       sql.NullString
		status, checkpointID             string
		riskLevel                        sql.NullString
		reversibleInt                    int
		seq                              int
		createdAt, updatedAt             int64
	)
	if err := scan(&id, &goalID, &primitive, &inputJSON, &outputJSON, &status, &checkpointID, &riskLevel, &reversibleInt, &seq, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	step := &GoalStep{
		ID:           id,
		GoalID:       goalID,
		Primitive:    primitive,
		Input:        json.RawMessage(inputJSON),
		Status:       GoalStepStatus(status),
		CheckpointID: checkpointID,
		Reversible:   reversibleInt != 0,
		Seq:          seq,
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
	}
	if riskLevel.Valid {
		step.RiskLevel = riskLevel.String
	}
	if outputJSON.Valid && outputJSON.String != "" {
		step.Output = json.RawMessage(outputJSON.String)
	}
	return step, nil
}

// ── GoalVerification CRUD ─────────────────────────────────────────────────────

func (s *SQLiteGoalStore) AppendGoalVerification(ctx context.Context, v *GoalVerification, bus *eventing.Bus) error {
	evidenceJSON := v.Evidence
	if evidenceJSON == nil {
		evidenceJSON = json.RawMessage("{}")
	}
	checkParamsJSON := v.CheckParams
	if checkParamsJSON == nil {
		checkParamsJSON = json.RawMessage("{}")
	}
	now := time.Now().UnixMilli()
	if v.CreatedAt == 0 {
		v.CreatedAt = now
	}
	if v.UpdatedAt == 0 {
		v.UpdatedAt = now
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO goal_verifications (id, goal_id, step_id, status, verdict, evidence_json, check_type, check_params, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, v.ID, v.GoalID, nullableString(v.StepID), string(v.Status), nullableString(v.Verdict),
		string(evidenceJSON), nullableString(v.CheckType), string(checkParamsJSON), v.CreatedAt, v.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert goal verification: %w", err)
	}
	publishGoalEvent(ctx, bus, "goal.verification_appended", v.GoalID, v)
	return nil
}

func (s *SQLiteGoalStore) UpdateGoalVerification(ctx context.Context, id string, status GoalVerificationStatus, verdict string, evidence json.RawMessage, bus *eventing.Bus) error {
	now := time.Now().UnixMilli()
	evidenceJSON := evidence
	if evidenceJSON == nil {
		evidenceJSON = json.RawMessage("{}")
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE goal_verifications SET status = ?, verdict = ?, evidence_json = ?, updated_at = ? WHERE id = ?
	`, string(status), verdict, string(evidenceJSON), now, id)
	if err != nil {
		return fmt.Errorf("update goal verification: %w", err)
	}
	verification, found, err := s.getGoalVerification(ctx, id)
	if err != nil {
		return fmt.Errorf("load goal verification %s: %w", id, err)
	}
	if !found {
		return fmt.Errorf("goal verification %s not found", id)
	}
	var eventType string
	switch status {
	case GoalVerificationRunning:
		eventType = EventGoalVerificationStarted
	case GoalVerificationPassed:
		eventType = EventGoalVerificationPassed
	case GoalVerificationFailed:
		eventType = EventGoalVerificationFailed
	default:
		eventType = "goal.verification_updated"
	}
	publishGoalEvent(ctx, bus, eventType, verification.GoalID, verification)
	return nil
}

func (s *SQLiteGoalStore) ListGoalVerifications(ctx context.Context, goalID string) ([]*GoalVerification, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, goal_id, step_id, status, verdict, evidence_json, check_type, check_params, created_at, updated_at
		FROM goal_verifications WHERE goal_id = ? ORDER BY created_at ASC
	`, goalID)
	if err != nil {
		return nil, fmt.Errorf("list goal verifications: %w", err)
	}
	defer rows.Close()
	var result []*GoalVerification
	for rows.Next() {
		v, err := scanGoalVerification(rows.Scan)
		if err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}

func (s *SQLiteGoalStore) getGoalVerification(ctx context.Context, id string) (*GoalVerification, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, goal_id, step_id, status, verdict, evidence_json, check_type, check_params, created_at, updated_at
		FROM goal_verifications WHERE id = ?
	`, id)
	v, err := scanGoalVerification(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return v, true, nil
}

func scanGoalVerification(scan func(dest ...any) error) (*GoalVerification, error) {
	var (
		id, goalID, status, evidenceJSON string
		stepID, verdict, checkType       sql.NullString
		checkParams                      sql.NullString
		createdAt, updatedAt             int64
	)
	if err := scan(&id, &goalID, &stepID, &status, &verdict, &evidenceJSON, &checkType, &checkParams, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	v := &GoalVerification{
		ID:        id,
		GoalID:    goalID,
		Status:    GoalVerificationStatus(status),
		Evidence:  json.RawMessage(evidenceJSON),
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}
	if stepID.Valid {
		v.StepID = stepID.String
	}
	if verdict.Valid {
		v.Verdict = verdict.String
	}
	if checkType.Valid {
		v.CheckType = checkType.String
	}
	if checkParams.Valid && checkParams.String != "" && checkParams.String != "{}" {
		v.CheckParams = json.RawMessage(checkParams.String)
	}
	return v, nil
}

// ── GoalBinding CRUD ──────────────────────────────────────────────────────────

func (s *SQLiteGoalStore) AppendGoalBinding(ctx context.Context, b *GoalBinding, bus *eventing.Bus) error {
	metadataJSON := b.Metadata
	if metadataJSON == nil {
		metadataJSON = json.RawMessage("{}")
	}
	now := time.Now().UnixMilli()
	if b.CreatedAt == 0 {
		b.CreatedAt = now
	}
	if b.UpdatedAt == 0 {
		b.UpdatedAt = now
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO goal_bindings (id, goal_id, binding_type, source_ref, target_ref, status, resolved_value, failure_reason, metadata_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, b.ID, b.GoalID, string(b.BindingType), b.SourceRef, b.TargetRef,
		string(b.Status), nullableString(b.ResolvedValue), nullableString(b.FailureReason),
		string(metadataJSON), b.CreatedAt, b.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert goal binding: %w", err)
	}
	publishGoalEvent(ctx, bus, "goal.binding_appended", b.GoalID, b)
	return nil
}

func (s *SQLiteGoalStore) ResolveGoalBinding(ctx context.Context, bindingID, resolvedValue string, bus *eventing.Bus) error {
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `
		UPDATE goal_bindings SET status = ?, resolved_value = ?, updated_at = ? WHERE id = ?
	`, string(GoalBindingResolved), resolvedValue, now, bindingID)
	if err != nil {
		return fmt.Errorf("resolve goal binding: %w", err)
	}
	publishGoalEvent(ctx, bus, EventGoalBindingResolved, bindingID, map[string]any{"id": bindingID, "resolved_value": resolvedValue})
	return nil
}

func (s *SQLiteGoalStore) FailGoalBinding(ctx context.Context, bindingID, reason string, bus *eventing.Bus) error {
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `
		UPDATE goal_bindings SET status = ?, failure_reason = ?, updated_at = ? WHERE id = ?
	`, string(GoalBindingFailed), reason, now, bindingID)
	if err != nil {
		return fmt.Errorf("fail goal binding: %w", err)
	}
	publishGoalEvent(ctx, bus, "goal.binding_failed", bindingID, map[string]any{"id": bindingID, "reason": reason})
	return nil
}

func (s *SQLiteGoalStore) ListGoalBindings(ctx context.Context, goalID string) ([]*GoalBinding, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, goal_id, binding_type, source_ref, target_ref, status, resolved_value, failure_reason, metadata_json, created_at, updated_at
		FROM goal_bindings WHERE goal_id = ? ORDER BY created_at ASC
	`, goalID)
	if err != nil {
		return nil, fmt.Errorf("list goal bindings: %w", err)
	}
	defer rows.Close()
	var result []*GoalBinding
	for rows.Next() {
		b, err := scanGoalBinding(rows.Scan)
		if err != nil {
			return nil, err
		}
		result = append(result, b)
	}
	return result, rows.Err()
}

func scanGoalBinding(scan func(dest ...any) error) (*GoalBinding, error) {
	var (
		id, goalID, bindingType, sourceRef, targetRef, status, metadataJSON string
		resolvedValue, failureReason                                        sql.NullString
		createdAt, updatedAt                                                int64
	)
	if err := scan(&id, &goalID, &bindingType, &sourceRef, &targetRef, &status,
		&resolvedValue, &failureReason, &metadataJSON, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	b := &GoalBinding{
		ID:          id,
		GoalID:      goalID,
		BindingType: GoalBindingType(bindingType),
		SourceRef:   sourceRef,
		TargetRef:   targetRef,
		Status:      GoalBindingStatus(status),
		Metadata:    json.RawMessage(metadataJSON),
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
	}
	if resolvedValue.Valid {
		b.ResolvedValue = resolvedValue.String
	}
	if failureReason.Valid {
		b.FailureReason = failureReason.String
	}
	return b, nil
}

// ── Replay ────────────────────────────────────────────────────────────────────

func (s *SQLiteGoalStore) ReplayGoal(ctx context.Context, goalID string, bus *eventing.Bus) error {
	publishGoalEvent(ctx, bus, EventGoalReplayStarted, goalID, map[string]any{"goal_id": goalID})
	steps, err := s.ListGoalSteps(ctx, goalID)
	if err != nil {
		return fmt.Errorf("replay: list steps: %w", err)
	}
	publishGoalEvent(ctx, bus, EventGoalReplayCompleted, goalID, map[string]any{"goal_id": goalID, "step_count": len(steps)})
	return nil
}

// ── GoalReview CRUD ───────────────────────────────────────────────────────────

func (s *SQLiteGoalStore) CreateGoalReview(ctx context.Context, r *GoalReview, bus *eventing.Bus) error {
	now := time.Now().UnixMilli()
	if r.CreatedAt == 0 {
		r.CreatedAt = now
	}
	if r.UpdatedAt == 0 {
		r.UpdatedAt = now
	}
	reversibleInt := 1
	if !r.Reversible {
		reversibleInt = 0
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO goal_reviews (id, goal_id, step_id, status, primitive, risk_level, reversible, side_effect, decision_reason, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, r.ID, r.GoalID, r.StepID, string(r.Status), r.Primitive, r.RiskLevel,
		reversibleInt, nullableString(r.SideEffect), nullableString(r.DecisionReason),
		r.CreatedAt, r.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert goal review: %w", err)
	}
	publishGoalEvent(ctx, bus, EventGoalReviewRequested, r.GoalID, r)
	return nil
}

func (s *SQLiteGoalStore) DecideGoalReview(ctx context.Context, reviewID string, status GoalReviewStatus, reason string, bus *eventing.Bus) error {
	existing, found, err := s.GetGoalReview(ctx, reviewID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("review %s not found", reviewID)
	}
	// Idempotency: same decision is a no-op.
	if existing.Status == status {
		return nil
	}
	// Conflict: opposite decision.
	if existing.Status != GoalReviewPending {
		return ErrReviewConflict
	}
	now := time.Now().UnixMilli()
	_, err = s.db.ExecContext(ctx, `
		UPDATE goal_reviews SET status = ?, decision_reason = ?, updated_at = ? WHERE id = ?
	`, string(status), nullableString(reason), now, reviewID)
	if err != nil {
		return fmt.Errorf("update goal review: %w", err)
	}
	eventType := EventGoalReviewApproved
	if status == GoalReviewRejected {
		eventType = EventGoalReviewRejected
	}
	publishGoalEvent(ctx, bus, eventType, existing.GoalID,
		map[string]any{"review_id": reviewID, "goal_id": existing.GoalID, "step_id": existing.StepID, "status": status})
	return nil
}

func (s *SQLiteGoalStore) ListGoalReviews(ctx context.Context, goalID string) ([]*GoalReview, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, goal_id, step_id, status, primitive, risk_level, reversible, side_effect, decision_reason, created_at, updated_at
		FROM goal_reviews WHERE goal_id = ? ORDER BY created_at ASC
	`, goalID)
	if err != nil {
		return nil, fmt.Errorf("list goal reviews: %w", err)
	}
	defer rows.Close()
	var result []*GoalReview
	for rows.Next() {
		r, err := scanGoalReview(rows.Scan)
		if err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (s *SQLiteGoalStore) GetGoalReview(ctx context.Context, reviewID string) (*GoalReview, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, goal_id, step_id, status, primitive, risk_level, reversible, side_effect, decision_reason, created_at, updated_at
		FROM goal_reviews WHERE id = ?
	`, reviewID)
	r, err := scanGoalReview(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return r, true, nil
}

func scanGoalReview(scan func(dest ...any) error) (*GoalReview, error) {
	var (
		id, goalID, stepID, status, primitive, riskLevel string
		reversibleInt                                    int
		sideEffect, decisionReason                       sql.NullString
		createdAt, updatedAt                             int64
	)
	if err := scan(&id, &goalID, &stepID, &status, &primitive, &riskLevel, &reversibleInt,
		&sideEffect, &decisionReason, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	r := &GoalReview{
		ID:         id,
		GoalID:     goalID,
		StepID:     stepID,
		Status:     GoalReviewStatus(status),
		Primitive:  primitive,
		RiskLevel:  riskLevel,
		Reversible: reversibleInt != 0,
		CreatedAt:  createdAt,
		UpdatedAt:  updatedAt,
	}
	if sideEffect.Valid {
		r.SideEffect = sideEffect.String
	}
	if decisionReason.Valid {
		r.DecisionReason = decisionReason.String
	}
	return r, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func publishGoalEvent(ctx context.Context, bus *eventing.Bus, eventType, sourceID string, payload any) {
	if bus == nil {
		return
	}
	bus.Publish(ctx, eventing.Event{
		Type:    eventType,
		Source:  "goal",
		Message: sourceID,
		Data:    eventing.MustJSON(payload),
	})
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
