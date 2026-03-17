package cvr

import (
	"context"
	"encoding/json"
	"time"
)

type TestSuiteStrategy struct {
	MinTests   int    // 最少测试数，低于此值视为 suite 配置错误
	Command    string // 测试命令，如 "go test ./..."
	Filter     string // 测试过滤器（可选）
	TimeoutSec int    // 超时秒数，默认 60
}

func (s *TestSuiteStrategy) Name() string        { return "test_suite" }
func (s *TestSuiteStrategy) Description() string { return "run test suite and verify all pass" }

func (s *TestSuiteStrategy) Run(ctx context.Context, exec StrategyExecutor, result ExecuteResult, manifest *CheckpointManifest) (StrategyResult, error) {
	start := time.Now()
	timeoutSec := s.TimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = 60
	}

	params := map[string]any{
		"command":   s.Command,
		"filter":    s.Filter,
		"timeout_s": timeoutSec,
	}

	verifyResult, err := exec.Execute(ctx, "verify.test", params)
	if err != nil {
		return StrategyResult{}, err
	}

	data := unwrapExecuteData(verifyResult.Data)
	passed := rawBool(data["passed"])
	total := rawInt(data["total"])
	failures := rawStringSlice(data["failures"])

	outcome := StrategyResult{
		DurationMs: time.Since(start).Milliseconds(),
	}

	if s.MinTests > 0 && total < s.MinTests {
		outcome.Outcome = VerifyOutcomeFailed
		outcome.Message = "test suite too small"
		outcome.RecoverHint = RecoverHintEscalate
		return outcome, nil
	}

	if !passed {
		details, _ := json.Marshal(map[string]any{"failures": failures})
		outcome.Outcome = VerifyOutcomeFailed
		outcome.Message = "test suite failed"
		outcome.Details = details
		if manifest != nil && manifest.Intent.Reversible {
			outcome.RecoverHint = RecoverHintRollback
		} else {
			outcome.RecoverHint = RecoverHintEscalate
		}
		return outcome, nil
	}

	outcome.Outcome = VerifyOutcomePassed
	outcome.Message = "test suite passed"
	return outcome, nil
}

func unwrapExecuteData(data map[string]json.RawMessage) map[string]json.RawMessage {
	if len(data) == 0 {
		return map[string]json.RawMessage{}
	}
	if nested, ok := data["result"]; ok {
		var unwrapped map[string]json.RawMessage
		if err := json.Unmarshal(nested, &unwrapped); err == nil {
			return unwrapped
		}
	}
	return data
}

func rawBool(raw json.RawMessage) bool {
	var value bool
	_ = json.Unmarshal(raw, &value)
	return value
}

func rawInt(raw json.RawMessage) int {
	var value int
	_ = json.Unmarshal(raw, &value)
	return value
}

func rawStringSlice(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var value []string
	_ = json.Unmarshal(raw, &value)
	return value
}
