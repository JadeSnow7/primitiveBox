package runtimectx

import (
	"context"

	"primitivebox/internal/cvr"
)

type intentContextKey struct{}

var IntentContextKey = intentContextKey{}

func WithIntent(ctx context.Context, intent *cvr.PrimitiveIntent) context.Context {
	if intent == nil {
		return ctx
	}
	return context.WithValue(ctx, IntentContextKey, intent)
}

func IntentFromContext(ctx context.Context) (*cvr.PrimitiveIntent, bool) {
	intent, ok := ctx.Value(IntentContextKey).(*cvr.PrimitiveIntent)
	return intent, ok
}
