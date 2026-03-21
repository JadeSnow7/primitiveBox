package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"primitivebox/internal/cvr"
	"primitivebox/internal/primitive"
)

type appDeclaredVerifyStrategy struct {
	method string
	verify primitive.AppPrimitiveVerify
	params json.RawMessage
}

func newAppDeclaredVerifyStrategy(method string, verify primitive.AppPrimitiveVerify, params json.RawMessage) cvr.VerifyStrategy {
	return &appDeclaredVerifyStrategy{
		method: method,
		verify: verify,
		params: append(json.RawMessage(nil), params...),
	}
}

func (s *appDeclaredVerifyStrategy) Name() string {
	return "app_declared:" + s.verify.Strategy
}

func (s *appDeclaredVerifyStrategy) Description() string {
	return fmt.Sprintf("run app-declared %s verification for %s", s.verify.Strategy, s.method)
}

func (s *appDeclaredVerifyStrategy) Run(ctx context.Context, exec cvr.StrategyExecutor, result cvr.ExecuteResult, manifest *cvr.CheckpointManifest) (cvr.StrategyResult, error) {
	start := time.Now()
	var (
		verifyResult cvr.ExecuteResult
		err          error
	)

	switch s.verify.Strategy {
	case "primitive":
		verifyResult, err = exec.Execute(ctx, s.verify.Primitive, s.params)
	case "command":
		verifyResult, err = exec.Execute(ctx, "verify.command", map[string]any{
			"command": s.verify.Command,
		})
	default:
		return cvr.StrategyResult{
			Outcome:     cvr.VerifyOutcomeError,
			Message:     "unsupported app verify strategy: " + s.verify.Strategy,
			RecoverHint: cvr.RecoverHintRetry,
			DurationMs:  time.Since(start).Milliseconds(),
		}, nil
	}

	outcome := cvr.StrategyResult{
		DurationMs: time.Since(start).Milliseconds(),
	}
	if err != nil {
		if err == context.DeadlineExceeded || ctx.Err() == context.DeadlineExceeded {
			outcome.Outcome = cvr.VerifyOutcomeTimeout
			outcome.RecoverHint = cvr.RecoverHintRetry
		} else {
			outcome.Outcome = cvr.VerifyOutcomeFailed
			if manifest != nil && manifest.Intent.Reversible {
				outcome.RecoverHint = cvr.RecoverHintRollback
			} else {
				outcome.RecoverHint = cvr.RecoverHintEscalate
			}
		}
		outcome.Message = err.Error()
		return outcome, nil
	}

	payload := unwrapVerifyPayload(verifyResult.Data)
	passed, explicit := verifyPayloadPassed(payload)
	if !verifyResult.Success {
		passed = false
		explicit = true
	}

	if !explicit {
		outcome.Outcome = cvr.VerifyOutcomePassed
		outcome.Message = "app verification passed"
		return outcome, nil
	}
	if passed {
		outcome.Outcome = cvr.VerifyOutcomePassed
		outcome.Message = verifyPayloadMessage(payload, "app verification passed")
		return outcome, nil
	}

	outcome.Outcome = cvr.VerifyOutcomeFailed
	outcome.Message = verifyPayloadMessage(payload, "app verification failed")
	if manifest != nil && manifest.Intent.Reversible {
		outcome.RecoverHint = cvr.RecoverHintRollback
	} else {
		outcome.RecoverHint = cvr.RecoverHintEscalate
	}
	details, marshalErr := json.Marshal(map[string]any{
		"verify_strategy": s.verify.Strategy,
		"verify_method":   s.verify.Primitive,
	})
	if marshalErr == nil {
		outcome.Details = details
	}
	return outcome, nil
}

func unwrapVerifyPayload(data map[string]json.RawMessage) map[string]json.RawMessage {
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

func verifyPayloadPassed(payload map[string]json.RawMessage) (bool, bool) {
	for _, key := range []string{"passed", "ok", "consistent"} {
		raw, ok := payload[key]
		if !ok {
			continue
		}
		var passed bool
		if err := json.Unmarshal(raw, &passed); err == nil {
			return passed, true
		}
	}
	return false, false
}

func verifyPayloadMessage(payload map[string]json.RawMessage, fallback string) string {
	for _, key := range []string{"summary", "message"} {
		raw, ok := payload[key]
		if !ok {
			continue
		}
		var message string
		if err := json.Unmarshal(raw, &message); err == nil && message != "" {
			return message
		}
	}

	if raw, ok := payload["problems"]; ok {
		var problems []string
		if err := json.Unmarshal(raw, &problems); err == nil && len(problems) > 0 {
			return problems[0]
		}
	}

	return fallback
}
