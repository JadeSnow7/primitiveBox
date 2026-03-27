package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"primitivebox/internal/cvr"
	"primitivebox/internal/primitive"
)

const (
	defaultAppID     = "pb-test-adapter"
	defaultNamespace = "demo"
)

type adapterConfig struct {
	socketPath  string
	rpcEndpoint string
	appID       string
	namespace   string
	noRegister  bool
}

type adapterState struct {
	mu    sync.Mutex
	value string
}

type appRPCRequest struct {
	ID     any             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type appRPCResponse struct {
	ID     any          `json:"id"`
	Result any          `json:"result,omitempty"`
	Error  *appRPCError `json:"error,omitempty"`
}

type appRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type httpRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      any             `json:"id"`
}

type httpRPCResponse struct {
	JSONRPC string `json:"jsonrpc"`
	Result  any    `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    any    `json:"data,omitempty"`
	} `json:"error,omitempty"`
	ID any `json:"id"`
}

func main() {
	cfg := parseFlags()
	if err := run(cfg); err != nil {
		log.Fatal(err)
	}
}

func parseFlags() adapterConfig {
	cfg := adapterConfig{}
	flag.StringVar(&cfg.socketPath, "socket", filepath.Join(os.TempDir(), "pb-test-app.sock"), "Unix socket path for primitive dispatch")
	flag.StringVar(&cfg.rpcEndpoint, "rpc-endpoint", "", "Sandbox-local PrimitiveBox HTTP endpoint used for app.register")
	flag.StringVar(&cfg.appID, "app-id", defaultAppID, "Override app_id for registered primitives")
	flag.StringVar(&cfg.namespace, "namespace", defaultNamespace, "Override primitive namespace prefix")
	flag.BoolVar(&cfg.noRegister, "no-register", false, "Skip app.register calls and only serve the Unix socket")
	flag.Parse()
	return cfg
}

func run(cfg adapterConfig) error {
	manifests := buildManifestSet(cfg.appID, cfg.namespace, cfg.socketPath)
	listener, err := listenUnix(cfg.socketPath)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()

	state := &adapterState{}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- serve(ctx, listener, state)
	}()

	if !cfg.noRegister {
		if strings.TrimSpace(cfg.rpcEndpoint) == "" {
			return errors.New("--rpc-endpoint is required unless --no-register is set")
		}
		for _, manifest := range manifests {
			if err := registerPrimitive(ctx, cfg.rpcEndpoint, manifest); err != nil {
				return fmt.Errorf("register %s: %w", manifest.Name, err)
			}
		}
	}

	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			return err
		}
		return nil
	}
}

func buildManifestSet(appID, namespace, socketPath string) []primitive.AppPrimitiveManifest {
	qualify := func(name string) string {
		return strings.TrimSpace(namespace) + "." + name
	}

	return []primitive.AppPrimitiveManifest{
		{
			AppID:       appID,
			Name:        qualify("echo"),
			Description: "Echo a message through the app primitive transport.",
			InputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"message": map[string]any{"type": "string"},
				},
				"required": []string{"message"},
			}),
			OutputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"message": map[string]any{"type": "string"},
					"adapter": map[string]any{"type": "string"},
				},
				"required": []string{"message", "adapter"},
			}),
			SocketPath: socketPath,
			Intent: cvr.PrimitiveIntent{
				Category:       cvr.IntentQuery,
				Reversible:     true,
				RiskLevel:      cvr.RiskLow,
				AffectedScopes: []string{"app:test"},
			},
		},
		{
			AppID:       appID,
			Name:        qualify("fail"),
			Description: "Return a deliberate adapter-side failure for protocol validation.",
			InputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"reason": map[string]any{"type": "string"},
				},
				"required": []string{"reason"},
			}),
			OutputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"ok": map[string]any{"type": "boolean"},
				},
			}),
			SocketPath: socketPath,
			Intent: cvr.PrimitiveIntent{
				Category:       cvr.IntentMutation,
				Reversible:     true,
				RiskLevel:      cvr.RiskLow,
				AffectedScopes: []string{"app:test"},
			},
		},
		{
			AppID:       appID,
			Name:        qualify("set"),
			Description: "Store one in-memory value to demonstrate declared verify and rollback metadata.",
			InputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"value":       map[string]any{"type": "string"},
					"verify_fail": map[string]any{"type": "boolean"},
				},
				"required": []string{"value"},
			}),
			OutputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"stored":         map[string]any{"type": "boolean"},
					"value":          map[string]any{"type": "string"},
					"previous_value": map[string]any{"type": "string"},
				},
				"required": []string{"stored", "value"},
			}),
			SocketPath: socketPath,
			Verify: &primitive.AppPrimitiveVerify{
				Strategy:  "primitive",
				Primitive: qualify("verify_set"),
			},
			Rollback: &primitive.AppPrimitiveRollback{
				Strategy:  "primitive",
				Primitive: qualify("rollback_set"),
			},
			Intent: cvr.PrimitiveIntent{
				Category:       cvr.IntentMutation,
				Reversible:     true,
				RiskLevel:      cvr.RiskMedium,
				AffectedScopes: []string{"app:test"},
			},
		},
		{
			AppID:       appID,
			Name:        qualify("state"),
			Description: "Read the current adapter-managed value.",
			InputSchema: mustJSON(map[string]any{
				"type": "object",
			}),
			OutputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"value": map[string]any{"type": "string"},
				},
				"required": []string{"value"},
			}),
			SocketPath: socketPath,
			Intent: cvr.PrimitiveIntent{
				Category:       cvr.IntentQuery,
				Reversible:     true,
				RiskLevel:      cvr.RiskLow,
				AffectedScopes: []string{"app:test"},
			},
		},
		{
			AppID:       appID,
			Name:        qualify("verify_set"),
			Description: "Verify the adapter-managed value matches the requested value.",
			InputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"value":       map[string]any{"type": "string"},
					"verify_fail": map[string]any{"type": "boolean"},
				},
				"required": []string{"value"},
			}),
			OutputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"passed":  map[string]any{"type": "boolean"},
					"message": map[string]any{"type": "string"},
				},
				"required": []string{"passed", "message"},
			}),
			SocketPath: socketPath,
			Intent: cvr.PrimitiveIntent{
				Category:       cvr.IntentVerification,
				Reversible:     true,
				RiskLevel:      cvr.RiskLow,
				AffectedScopes: []string{"app:test"},
			},
		},
		{
			AppID:       appID,
			Name:        qualify("rollback_set"),
			Description: "Restore the previous adapter-managed value using rollback payload metadata.",
			InputSchema: mustJSON(map[string]any{
				"type": "object",
			}),
			OutputSchema: mustJSON(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"rolled_back": map[string]any{"type": "boolean"},
					"value":       map[string]any{"type": "string"},
				},
				"required": []string{"rolled_back", "value"},
			}),
			SocketPath: socketPath,
			Intent: cvr.PrimitiveIntent{
				Category:       cvr.IntentRollback,
				Reversible:     true,
				RiskLevel:      cvr.RiskLow,
				AffectedScopes: []string{"app:test"},
			},
		},
	}
}

func listenUnix(socketPath string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return nil, err
	}
	_ = os.Remove(socketPath)
	return net.Listen("unix", socketPath)
}

func serve(ctx context.Context, listener net.Listener, state *adapterState) error {
	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
			}
			return err
		}
		go handleConn(ctx, conn, state)
	}
}

func handleConn(ctx context.Context, conn net.Conn, state *adapterState) {
	defer conn.Close()

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		_ = writeAppResponse(conn, appRPCResponse{
			ID:    0,
			Error: &appRPCError{Code: -32600, Message: "invalid request"},
		})
		return
	}

	var req appRPCRequest
	if err := json.Unmarshal(line, &req); err != nil {
		_ = writeAppResponse(conn, appRPCResponse{
			ID:    0,
			Error: &appRPCError{Code: -32600, Message: "invalid request"},
		})
		return
	}

	result, rpcErr := dispatch(ctx, state, req.Method, req.Params)
	resp := appRPCResponse{ID: req.ID, Result: result}
	if rpcErr != nil {
		resp.Result = nil
		resp.Error = rpcErr
	}
	_ = writeAppResponse(conn, resp)
}

func dispatch(ctx context.Context, state *adapterState, method string, raw json.RawMessage) (any, *appRPCError) {
	switch method {
	case "demo.echo":
		var in struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(raw, &in); err != nil || strings.TrimSpace(in.Message) == "" {
			return nil, &appRPCError{Code: -32602, Message: "message is required"}
		}
		return map[string]any{"message": in.Message, "adapter": defaultAppID}, nil
	case "demo.fail":
		var in struct {
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal(raw, &in); err != nil || strings.TrimSpace(in.Reason) == "" {
			return nil, &appRPCError{Code: -32602, Message: "reason is required"}
		}
		return nil, &appRPCError{Code: 4100, Message: "deliberate failure: " + in.Reason}
	case "demo.set":
		var in struct {
			Value      string `json:"value"`
			VerifyFail bool   `json:"verify_fail"`
		}
		if err := json.Unmarshal(raw, &in); err != nil || strings.TrimSpace(in.Value) == "" {
			return nil, &appRPCError{Code: -32602, Message: "value is required"}
		}
		state.mu.Lock()
		previous := state.value
		state.value = in.Value
		state.mu.Unlock()
		return map[string]any{
			"stored":         true,
			"value":          in.Value,
			"previous_value": previous,
		}, nil
	case "demo.state":
		state.mu.Lock()
		value := state.value
		state.mu.Unlock()
		return map[string]any{"value": value}, nil
	case "demo.verify_set":
		var in struct {
			Value      string `json:"value"`
			VerifyFail bool   `json:"verify_fail"`
		}
		if err := json.Unmarshal(raw, &in); err != nil || strings.TrimSpace(in.Value) == "" {
			return nil, &appRPCError{Code: -32602, Message: "value is required"}
		}
		if in.VerifyFail {
			return map[string]any{"passed": false, "message": "forced verify failure"}, nil
		}
		state.mu.Lock()
		value := state.value
		state.mu.Unlock()
		if value != in.Value {
			return map[string]any{"passed": false, "message": "adapter state mismatch"}, nil
		}
		return map[string]any{"passed": true, "message": "adapter state matches"}, nil
	case "demo.rollback_set":
		var in struct {
			Result struct {
				PreviousValue string `json:"previous_value"`
			} `json:"result"`
		}
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, &appRPCError{Code: -32602, Message: "invalid rollback payload"}
		}
		state.mu.Lock()
		state.value = in.Result.PreviousValue
		value := state.value
		state.mu.Unlock()
		return map[string]any{"rolled_back": true, "value": value}, nil
	default:
		_ = ctx
		return nil, &appRPCError{Code: -32601, Message: "method not found: " + method}
	}
}

func writeAppResponse(w io.Writer, resp appRPCResponse) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
}

func registerPrimitive(ctx context.Context, endpoint string, manifest primitive.AppPrimitiveManifest) error {
	body, err := json.Marshal(httpRPCRequest{
		JSONRPC: "2.0",
		Method:  "app.register",
		Params:  mustJSON(manifest),
		ID:      "register-" + manifest.Name,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(endpoint, "/")+"/rpc", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-PB-Origin", "sandbox")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var rpcResp httpRPCResponse
	if err := json.Unmarshal(raw, &rpcResp); err != nil {
		return fmt.Errorf("decode register response: %w", err)
	}
	if rpcResp.Error != nil {
		return fmt.Errorf("%s", rpcResp.Error.Message)
	}
	return nil
}

func mustJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}
