package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"primitivebox/internal/primitive"
)

func TestHandleRPCPropagatesRequestContextCancellation(t *testing.T) {
	t.Parallel()

	originalDispatch := dispatchFn
	defer func() {
		dispatchFn = originalDispatch
	}()

	canceled := make(chan struct{}, 1)
	dispatchFn = func(ctx context.Context, workspace, method string, params json.RawMessage) (primitive.Result, error) {
		if workspace != "/workspace" {
			t.Fatalf("unexpected workspace: %s", workspace)
		}
		if method != "repo.search" {
			t.Fatalf("unexpected method: %s", method)
		}
		<-ctx.Done()
		canceled <- struct{}{}
		return primitive.Result{}, ctx.Err()
	}

	body := bytes.NewBufferString(`{"jsonrpc":"2.0","method":"repo.search","params":{"query":"needle"},"id":"req-1"}`)
	req := httptest.NewRequest(http.MethodPost, "/rpc", body)
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		handleRPC("/workspace", w, req)
		close(done)
	}()

	cancel()

	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch did not observe request cancellation")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after cancellation")
	}
}
