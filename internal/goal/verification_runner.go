package goal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"primitivebox/internal/control"
	"primitivebox/internal/eventing"
	"primitivebox/internal/orchestrator"
	"primitivebox/internal/sandbox"
)

type VerificationRunner struct {
	store      control.GoalStore
	engine     *orchestrator.Engine
	bus        *eventing.Bus
	manager    *sandbox.Manager
	httpClient *http.Client
}

type VerificationRunResult struct {
	Count  int
	Failed bool
}

func NewVerificationRunner(store control.GoalStore, engine *orchestrator.Engine, bus *eventing.Bus, manager *sandbox.Manager) *VerificationRunner {
	return &VerificationRunner{
		store:   store,
		engine:  engine,
		bus:     bus,
		manager: manager,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (r *VerificationRunner) Run(ctx context.Context, goalID string) (*VerificationRunResult, error) {
	goal, found, err := r.store.GetGoal(ctx, goalID)
	if err != nil {
		return nil, fmt.Errorf("get goal %s: %w", goalID, err)
	}
	if !found {
		return nil, fmt.Errorf("goal %s not found", goalID)
	}

	steps, err := r.store.ListGoalSteps(ctx, goalID)
	if err != nil {
		return nil, fmt.Errorf("list steps: %w", err)
	}
	verifications, err := r.store.ListGoalVerifications(ctx, goalID)
	if err != nil {
		return nil, fmt.Errorf("list verifications: %w", err)
	}
	result := &VerificationRunResult{Count: len(verifications)}
	if len(verifications) == 0 {
		return result, nil
	}

	stepByID := make(map[string]*control.GoalStep, len(steps))
	for _, step := range steps {
		stepByID[step.ID] = step
	}

	verificationByID := make(map[string]*control.GoalVerification, len(verifications))
	for _, verification := range verifications {
		verificationByID[verification.ID] = verification
	}

	for _, verification := range verifications {
		if verification.Status == control.GoalVerificationPassed {
			continue
		}
		if err := r.store.UpdateGoalVerification(ctx, verification.ID, control.GoalVerificationRunning, "", verification.Evidence, r.bus); err != nil {
			return nil, fmt.Errorf("mark verification %s running: %w", verification.ID, err)
		}
		status, verdict, evidence := r.executeVerification(ctx, goal, verification, stepByID, verificationByID)
		if err := r.store.UpdateGoalVerification(ctx, verification.ID, status, verdict, evidence, r.bus); err != nil {
			return nil, fmt.Errorf("persist verification %s: %w", verification.ID, err)
		}
		verification.Status = status
		verification.Verdict = verdict
		verification.Evidence = evidence
		verificationByID[verification.ID] = verification
		if status == control.GoalVerificationFailed {
			result.Failed = true
			return result, nil
		}
	}

	return result, nil
}

func (r *VerificationRunner) executeVerification(
	ctx context.Context,
	goal *control.Goal,
	verification *control.GoalVerification,
	stepByID map[string]*control.GoalStep,
	verificationByID map[string]*control.GoalVerification,
) (control.GoalVerificationStatus, string, json.RawMessage) {
	switch verification.CheckType {
	case "primitive_call":
		return r.executePrimitiveCall(ctx, goal, verification)
	case "http_probe":
		return r.executeHTTPProbe(ctx, verification)
	case "json_assert":
		return r.executeJSONAssert(verification, stepByID, verificationByID)
	default:
		return failureResult(
			fmt.Sprintf("unsupported check_type %q", verification.CheckType),
			map[string]any{
				"verification_id": verification.ID,
				"check_type":      verification.CheckType,
			},
		)
	}
}

func (r *VerificationRunner) executePrimitiveCall(
	ctx context.Context,
	goal *control.Goal,
	verification *control.GoalVerification,
) (control.GoalVerificationStatus, string, json.RawMessage) {
	var params struct {
		Method    string          `json:"method"`
		Params    json.RawMessage `json:"params"`
		SandboxID string          `json:"sandbox_id"`
		Expect    *assertSpec     `json:"expect"`
	}
	if err := json.Unmarshal(verification.CheckParams, &params); err != nil {
		return failureResult("invalid primitive_call check_params", map[string]any{
			"verification_id": verification.ID,
			"error":           err.Error(),
		})
	}
	if strings.TrimSpace(params.Method) == "" {
		return failureResult("primitive_call requires method", map[string]any{
			"verification_id": verification.ID,
		})
	}
	if len(params.Params) == 0 {
		params.Params = json.RawMessage("{}")
	}

	var (
		result *orchestrator.StepResult
		err    error
	)
	if params.SandboxID != "" {
		result, err = r.executeSandboxPrimitive(ctx, params.SandboxID, params.Method, params.Params)
	} else {
		result, err = r.engine.ExecutorExecute(ctx, params.Method, params.Params)
	}
	if err != nil {
		return failureResult(err.Error(), map[string]any{
			"verification_id": verification.ID,
			"check_type":      verification.CheckType,
			"method":          params.Method,
			"sandbox_id":      params.SandboxID,
		})
	}
	if result == nil || !result.Success {
		message := "primitive_call failed"
		if result != nil && result.Error != nil && result.Error.Message != "" {
			message = result.Error.Message
		}
		return failureResult(message, map[string]any{
			"verification_id": verification.ID,
			"check_type":      verification.CheckType,
			"method":          params.Method,
			"sandbox_id":      params.SandboxID,
			"result":          decodeRawJSON(resultData(result)),
		})
	}

	observed := decodeRawJSON(result.Data)
	if params.Expect != nil {
		ok, failure, assertionEvidence := evaluateAssertion(observed, *params.Expect)
		evidence := map[string]any{
			"verification_id": verification.ID,
			"check_type":      verification.CheckType,
			"method":          params.Method,
			"sandbox_id":      params.SandboxID,
			"result":          observed,
			"assertion":       assertionEvidence,
		}
		if !ok {
			return failureResult(failure, evidence)
		}
		return successResult("primitive_call passed", evidence)
	}

	return successResult("primitive_call passed", map[string]any{
		"verification_id":  verification.ID,
		"check_type":       verification.CheckType,
		"method":           params.Method,
		"sandbox_id":       params.SandboxID,
		"result":           observed,
		"goal_sandbox_ids": goal.SandboxIDs,
	})
}

func (r *VerificationRunner) executeSandboxPrimitive(ctx context.Context, sandboxID, method string, params json.RawMessage) (*orchestrator.StepResult, error) {
	if r.manager == nil {
		return nil, fmt.Errorf("sandbox manager unavailable for sandbox verification %s", sandboxID)
	}
	sb, err := r.manager.Inspect(ctx, sandboxID)
	if err != nil {
		return nil, fmt.Errorf("inspect sandbox %s: %w", sandboxID, err)
	}
	if sb.RPCEndpoint == "" {
		return nil, fmt.Errorf("sandbox %s has no rpc endpoint", sandboxID)
	}

	reqBody, err := json.Marshal(sandboxRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		ID:      "verification",
	})
	if err != nil {
		return nil, fmt.Errorf("marshal sandbox rpc request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sb.RPCEndpoint+"/rpc", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("build sandbox rpc request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sandbox rpc request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read sandbox rpc response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sandbox rpc status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var rpcResp sandboxRPCResponse
	if err := json.Unmarshal(body, &rpcResp); err != nil {
		return nil, fmt.Errorf("decode sandbox rpc response: %w", err)
	}
	if rpcResp.Error != nil {
		return &orchestrator.StepResult{
			Success: false,
			Error: &orchestrator.StepError{
				Kind:    orchestrator.FailureUnknown,
				Code:    strconv.Itoa(rpcResp.Error.Code),
				Message: rpcResp.Error.Message,
				Summary: rpcResp.Error.Message,
			},
		}, nil
	}

	var payload struct {
		Data     json.RawMessage `json:"data"`
		Duration int64           `json:"duration_ms"`
	}
	if err := json.Unmarshal(rpcResp.Result, &payload); err != nil {
		return nil, fmt.Errorf("decode sandbox rpc result: %w", err)
	}
	if len(payload.Data) == 0 {
		payload.Data = json.RawMessage("null")
	}
	return &orchestrator.StepResult{Success: true, Data: payload.Data, Duration: payload.Duration}, nil
}

func (r *VerificationRunner) executeHTTPProbe(
	ctx context.Context,
	verification *control.GoalVerification,
) (control.GoalVerificationStatus, string, json.RawMessage) {
	var params struct {
		URL            string            `json:"url"`
		Method         string            `json:"method"`
		Headers        map[string]string `json:"headers"`
		ExpectedStatus int               `json:"expected_status"`
		BodyContains   string            `json:"body_contains"`
	}
	if err := json.Unmarshal(verification.CheckParams, &params); err != nil {
		return failureResult("invalid http_probe check_params", map[string]any{
			"verification_id": verification.ID,
			"error":           err.Error(),
		})
	}
	if strings.TrimSpace(params.URL) == "" {
		return failureResult("http_probe requires url", map[string]any{"verification_id": verification.ID})
	}
	method := params.Method
	if method == "" {
		method = http.MethodGet
	}
	expectedStatus := params.ExpectedStatus
	if expectedStatus == 0 {
		expectedStatus = http.StatusOK
	}

	req, err := http.NewRequestWithContext(ctx, method, params.URL, nil)
	if err != nil {
		return failureResult("invalid http_probe request", map[string]any{
			"verification_id": verification.ID,
			"error":           err.Error(),
		})
	}
	for key, value := range params.Headers {
		req.Header.Set(key, value)
	}
	resp, err := r.httpClient.Do(req)
	if err != nil {
		return failureResult(err.Error(), map[string]any{
			"verification_id": verification.ID,
			"url":             params.URL,
			"method":          method,
		})
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
	if err != nil {
		return failureResult("read http_probe response failed", map[string]any{
			"verification_id": verification.ID,
			"url":             params.URL,
			"error":           err.Error(),
		})
	}

	evidence := map[string]any{
		"verification_id": verification.ID,
		"check_type":      verification.CheckType,
		"url":             params.URL,
		"method":          method,
		"expected_status": expectedStatus,
		"observed_status": resp.StatusCode,
		"body":            string(body),
	}
	if resp.StatusCode != expectedStatus {
		return failureResult(fmt.Sprintf("expected HTTP %d, got %d", expectedStatus, resp.StatusCode), evidence)
	}
	if params.BodyContains != "" && !strings.Contains(string(body), params.BodyContains) {
		evidence["body_contains"] = params.BodyContains
		return failureResult(fmt.Sprintf("response body missing %q", params.BodyContains), evidence)
	}
	if params.BodyContains != "" {
		evidence["body_contains"] = params.BodyContains
	}
	return successResult("http_probe passed", evidence)
}

func (r *VerificationRunner) executeJSONAssert(
	verification *control.GoalVerification,
	stepByID map[string]*control.GoalStep,
	verificationByID map[string]*control.GoalVerification,
) (control.GoalVerificationStatus, string, json.RawMessage) {
	var params struct {
		SourceStepID         string `json:"source_step_id"`
		SourceVerificationID string `json:"source_verification_id"`
		Path                 string `json:"path"`
		Operator             string `json:"operator"`
		Expected             any    `json:"expected"`
	}
	if err := json.Unmarshal(verification.CheckParams, &params); err != nil {
		return failureResult("invalid json_assert check_params", map[string]any{
			"verification_id": verification.ID,
			"error":           err.Error(),
		})
	}

	var source any
	sourceKind := ""
	switch {
	case params.SourceStepID != "":
		step := stepByID[params.SourceStepID]
		if step == nil {
			return failureResult("json_assert source_step_id not found", map[string]any{
				"verification_id": verification.ID,
				"source_step_id":  params.SourceStepID,
			})
		}
		sourceKind = "step"
		source = decodeRawJSON(step.Output)
	case params.SourceVerificationID != "":
		previous := verificationByID[params.SourceVerificationID]
		if previous == nil {
			return failureResult("json_assert source_verification_id not found", map[string]any{
				"verification_id":        verification.ID,
				"source_verification_id": params.SourceVerificationID,
			})
		}
		sourceKind = "verification"
		source = decodeRawJSON(previous.Evidence)
	default:
		return failureResult("json_assert requires source_step_id or source_verification_id", map[string]any{
			"verification_id": verification.ID,
		})
	}

	assertion := assertSpec{Path: params.Path, Operator: params.Operator, Expected: params.Expected}
	ok, failure, assertionEvidence := evaluateAssertion(source, assertion)
	evidence := map[string]any{
		"verification_id":        verification.ID,
		"check_type":             verification.CheckType,
		"source_kind":            sourceKind,
		"source_step_id":         params.SourceStepID,
		"source_verification_id": params.SourceVerificationID,
		"assertion":              assertionEvidence,
	}
	if !ok {
		return failureResult(failure, evidence)
	}
	return successResult("json_assert passed", evidence)
}

type assertSpec struct {
	Path     string `json:"path"`
	Operator string `json:"operator"`
	Expected any    `json:"expected"`
}

func evaluateAssertion(source any, spec assertSpec) (bool, string, map[string]any) {
	operator := spec.Operator
	if operator == "" {
		operator = "eq"
	}
	observed, exists := lookupJSONPath(source, spec.Path)
	evidence := map[string]any{
		"path":     spec.Path,
		"operator": operator,
		"expected": spec.Expected,
		"observed": observed,
		"exists":   exists,
	}

	switch operator {
	case "exists":
		if exists {
			return true, "", evidence
		}
		return false, fmt.Sprintf("path %q does not exist", spec.Path), evidence
	case "not_exists":
		if !exists {
			return true, "", evidence
		}
		return false, fmt.Sprintf("path %q unexpectedly exists", spec.Path), evidence
	}
	if !exists {
		return false, fmt.Sprintf("path %q does not exist", spec.Path), evidence
	}

	switch operator {
	case "eq":
		if reflect.DeepEqual(normalizeJSONValue(observed), normalizeJSONValue(spec.Expected)) {
			return true, "", evidence
		}
		return false, fmt.Sprintf("expected %q to equal %#v", spec.Path, spec.Expected), evidence
	case "neq":
		if !reflect.DeepEqual(normalizeJSONValue(observed), normalizeJSONValue(spec.Expected)) {
			return true, "", evidence
		}
		return false, fmt.Sprintf("expected %q to differ from %#v", spec.Path, spec.Expected), evidence
	case "contains":
		if containsValue(observed, spec.Expected) {
			return true, "", evidence
		}
		return false, fmt.Sprintf("expected %q to contain %#v", spec.Path, spec.Expected), evidence
	case "gt", "gte", "lt", "lte":
		left, leftOK := toFloat64(observed)
		right, rightOK := toFloat64(spec.Expected)
		if !leftOK || !rightOK {
			return false, fmt.Sprintf("operator %s requires numeric values", operator), evidence
		}
		switch operator {
		case "gt":
			return left > right, fmt.Sprintf("expected %q > %v", spec.Path, right), evidence
		case "gte":
			return left >= right, fmt.Sprintf("expected %q >= %v", spec.Path, right), evidence
		case "lt":
			return left < right, fmt.Sprintf("expected %q < %v", spec.Path, right), evidence
		case "lte":
			return left <= right, fmt.Sprintf("expected %q <= %v", spec.Path, right), evidence
		}
	}

	return false, fmt.Sprintf("unsupported operator %q", operator), evidence
}

func lookupJSONPath(source any, path string) (any, bool) {
	if path == "" || path == "." {
		return source, true
	}
	current := source
	for _, token := range splitPath(path) {
		switch node := current.(type) {
		case map[string]any:
			value, ok := node[token]
			if !ok {
				return nil, false
			}
			current = value
		case []any:
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || index >= len(node) {
				return nil, false
			}
			current = node[index]
		default:
			return nil, false
		}
	}
	return current, true
}

func splitPath(path string) []string {
	path = strings.ReplaceAll(path, "[", ".")
	path = strings.ReplaceAll(path, "]", "")
	parts := strings.Split(path, ".")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

func containsValue(observed, expected any) bool {
	switch value := observed.(type) {
	case string:
		return strings.Contains(value, fmt.Sprint(expected))
	case []any:
		for _, item := range value {
			if reflect.DeepEqual(normalizeJSONValue(item), normalizeJSONValue(expected)) {
				return true
			}
		}
	case map[string]any:
		_, ok := value[fmt.Sprint(expected)]
		return ok
	}
	return false
}

func toFloat64(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case int32:
		return float64(v), true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	}
	return 0, false
}

func successResult(verdict string, evidence map[string]any) (control.GoalVerificationStatus, string, json.RawMessage) {
	return control.GoalVerificationPassed, verdict, mustMarshalJSON(evidence)
}

func failureResult(verdict string, evidence map[string]any) (control.GoalVerificationStatus, string, json.RawMessage) {
	return control.GoalVerificationFailed, verdict, mustMarshalJSON(evidence)
}

func mustMarshalJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return data
}

func decodeRawJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw)
	}
	return value
}

func resultData(result *orchestrator.StepResult) json.RawMessage {
	if result == nil {
		return nil
	}
	return result.Data
}

func normalizeJSONValue(value any) any {
	switch v := value.(type) {
	case json.RawMessage:
		return decodeRawJSON(v)
	default:
		return v
	}
}

type sandboxRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	ID      any             `json:"id"`
}

type sandboxRPCResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	Result  json.RawMessage  `json:"result"`
	Error   *sandboxRPCError `json:"error"`
	ID      any              `json:"id"`
}

type sandboxRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
