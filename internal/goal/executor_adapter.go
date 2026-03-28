// Package goal bridges the goal control-plane (GoalStore) to the orchestrator
// engine by providing a PrimitiveExecutor backed by sandbox.Router.
package goal

import (
	"context"
	"encoding/json"
	"fmt"

	"primitivebox/internal/orchestrator"
	"primitivebox/internal/sandbox"
)

// RouterExecutorAdapter implements orchestrator.PrimitiveExecutor by delegating
// primitive calls to a sandbox.Router. It converts primitive.Result to
// orchestrator.StepResult so the orchestrator engine can drive CVR coordination.
type RouterExecutorAdapter struct {
	router *sandbox.Router
}

// NewRouterExecutorAdapter creates a new adapter wrapping the given router.
func NewRouterExecutorAdapter(router *sandbox.Router) *RouterExecutorAdapter {
	return &RouterExecutorAdapter{router: router}
}

// Execute routes the method call through the sandbox.Router and converts the
// result to the orchestrator's StepResult type.
func (a *RouterExecutorAdapter) Execute(ctx context.Context, method string, params json.RawMessage) (*orchestrator.StepResult, error) {
	result, err := a.router.Route(ctx, method, params)
	if err != nil {
		return &orchestrator.StepResult{
			Success: false,
			Error: &orchestrator.StepError{
				Kind:    orchestrator.FailureEnvironment,
				Code:    "ROUTE_ERROR",
				Message: err.Error(),
				Summary: truncate(err.Error(), 200),
			},
		}, err
	}

	dataJSON, marshalErr := json.Marshal(result.Data)
	if marshalErr != nil {
		dataJSON = json.RawMessage("{}")
	}

	sr := &orchestrator.StepResult{
		Success:  true,
		Data:     dataJSON,
		Duration: result.Duration,
	}
	return sr, nil
}

// ListPrimitives returns nil; the router's registry is authoritative.
func (a *RouterExecutorAdapter) ListPrimitives() []string {
	return nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return fmt.Sprintf("%s...", s[:max])
}
