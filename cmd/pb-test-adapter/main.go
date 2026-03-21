// Package main implements a minimal in-process test adapter for PrimitiveBox
// CVR smoke tests. It exposes five primitives over a Unix socket using the
// newline-delimited JSON-RPC protocol that the sandbox Router expects:
//
//	demo.set          – mutate in-memory value; reversible, verify required
//	demo.verify_set   – pass unless current value is "FAIL_VERIFY"
//	demo.rollback_set – restore previous value
//	demo.state        – read current value (query, no side-effects)
//	demo.fail         – always returns an RPC error (irreversible, no rollback)
//
// The adapter prints a single JSON registration line to stdout on startup
// so the smoke test can discover the socket path.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
)

// ---------------------------------------------------------------------------
// In-memory state
// ---------------------------------------------------------------------------

type adapterState struct {
	mu       sync.Mutex
	current  string
	previous string
}

var state = &adapterState{}

// ---------------------------------------------------------------------------
// Wire protocol types (newline-delimited JSON, one request per connection)
// ---------------------------------------------------------------------------

type rpcRequest struct {
	ID     uint64          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type rpcResponse struct {
	ID     uint64          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ---------------------------------------------------------------------------
// Dispatch
// ---------------------------------------------------------------------------

var dispatchFn = dispatch

func dispatch(req rpcRequest) rpcResponse {
	switch req.Method {
	case "demo.set":
		return handleSet(req)
	case "demo.verify_set":
		return handleVerifySet(req)
	case "demo.rollback_set":
		return handleRollbackSet(req)
	case "demo.state":
		return handleState(req)
	case "demo.fail":
		return handleFail(req)
	default:
		return rpcResponse{
			ID:    req.ID,
			Error: &rpcError{Code: -32601, Message: fmt.Sprintf("unknown method: %s", req.Method)},
		}
	}
}

func handleSet(req rpcRequest) rpcResponse {
	var params struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return rpcResponse{ID: req.ID, Error: &rpcError{Code: -32602, Message: "invalid params: " + err.Error()}}
	}
	state.mu.Lock()
	state.previous = state.current
	state.current = params.Value
	state.mu.Unlock()
	result, _ := json.Marshal(map[string]any{"ok": true})
	return rpcResponse{ID: req.ID, Result: result}
}

func handleVerifySet(req rpcRequest) rpcResponse {
	state.mu.Lock()
	current := state.current
	state.mu.Unlock()
	// "FAIL_VERIFY" is the deterministic trigger for verify failure.
	passed := current != "FAIL_VERIFY"
	result, _ := json.Marshal(map[string]any{"passed": passed})
	return rpcResponse{ID: req.ID, Result: result}
}

func handleRollbackSet(req rpcRequest) rpcResponse {
	state.mu.Lock()
	state.current = state.previous
	state.mu.Unlock()
	result, _ := json.Marshal(map[string]any{"rolled_back": true})
	return rpcResponse{ID: req.ID, Result: result}
}

func handleState(req rpcRequest) rpcResponse {
	state.mu.Lock()
	current := state.current
	state.mu.Unlock()
	result, _ := json.Marshal(map[string]any{"value": current})
	return rpcResponse{ID: req.ID, Result: result}
}

func handleFail(_ rpcRequest) rpcResponse {
	return rpcResponse{
		Error: &rpcError{
			Code:    -32603,
			Message: "demo.fail: intentional failure (irreversible, no rollback declared, fail-closed)",
		},
	}
}

// ---------------------------------------------------------------------------
// Connection handler
// ---------------------------------------------------------------------------

func handleConn(conn net.Conn) {
	defer conn.Close()
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		return
	}
	resp := dispatchFn(req)
	data, _ := json.Marshal(resp)
	_, _ = conn.Write(append(data, '\n'))
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func main() {
	var socketPath string
	flag.StringVar(&socketPath, "socket", "/tmp/pb-test-adapter.sock", "Unix socket path to listen on")
	flag.Parse()

	_ = os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Fatalf("listen unix %s: %v", socketPath, err)
	}
	defer listener.Close()

	reg, _ := json.Marshal(map[string]any{
		"adapter": "pb-test-adapter",
		"socket":  socketPath,
	})
	fmt.Println(string(reg))

	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go handleConn(conn)
	}
}
