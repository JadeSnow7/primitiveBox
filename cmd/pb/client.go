package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"
)

// pbClient is an HTTP client for the PrimitiveBox server.
type pbClient struct {
	endpoint string
	http     *http.Client
}

// jsonRPCRequest is a JSON-RPC 2.0 request.
type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
	ID      int    `json:"id"`
}

// jsonRPCResponse is a JSON-RPC 2.0 response.
type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
	ID      int             `json:"id"`
}

// jsonRPCError is a JSON-RPC 2.0 error.
type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func newPBClient(endpoint string) *pbClient {
	return &pbClient{
		endpoint: endpoint,
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// newStreamClient returns a client with no timeout (for SSE streams).
func newStreamClient(endpoint string) *pbClient {
	return &pbClient{
		endpoint: endpoint,
		http:     &http.Client{},
	}
}

// rpcCall sends a JSON-RPC request and returns the response.
func (c *pbClient) rpcCall(ctx context.Context, method string, params any, sandboxID string) (*jsonRPCResponse, error) {
	path := "/rpc"
	if sandboxID != "" {
		path = fmt.Sprintf("/sandboxes/%s/rpc", sandboxID)
	}

	req := jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		ID:      1,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	resp, err := c.post(ctx, path, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var rpcResp jsonRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	return &rpcResp, nil
}

// rpcStream sends a JSON-RPC request and streams SSE responses.
func (c *pbClient) rpcStream(ctx context.Context, method string, params any, sandboxID string, handler func(event, data string) error) error {
	path := "/rpc/stream"
	if sandboxID != "" {
		path = fmt.Sprintf("/sandboxes/%s/rpc/stream", sandboxID)
	}

	req := jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		ID:      1,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return err
	}

	resp, err := c.post(ctx, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return readSSE(resp.Body, handler)
}

// get performs a GET request and returns the response body.
func (c *pbClient) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+path, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body))
	}

	return io.ReadAll(resp.Body)
}

// sseStream opens a GET-based SSE stream.
func (c *pbClient) sseStream(ctx context.Context, path string, handler func(event, data string) error) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return readSSE(resp.Body, handler)
}

func (c *pbClient) post(ctx context.Context, path string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, string(b))
	}

	return resp, nil
}

// readSSE reads Server-Sent Events from a reader and calls handler for each complete event.
func readSSE(r io.Reader, handler func(event, data string) error) error {
	scanner := bufio.NewScanner(r)
	var event, data string

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			// Empty line = dispatch event
			if data != "" {
				if err := handler(event, data); err != nil {
					return err
				}
			}
			event = ""
			data = ""
			continue
		}

		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
	}

	// Flush any remaining event
	if data != "" {
		return handler(event, data)
	}
	return scanner.Err()
}

// resolveEndpoint determines the server URL from flag, env, config, or default.
func resolveEndpoint(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if env := os.Getenv("PB_ENDPOINT"); env != "" {
		return env
	}
	// Try loading config for server host/port
	for _, path := range []string{cfgFile, ".primitivebox.yaml"} {
		if path == "" {
			continue
		}
		if cfg, err := loadConfigSafe(path); err == nil && cfg.Server.Host != "" {
			port := cfg.Server.Port
			if port == 0 {
				port = 8080
			}
			return fmt.Sprintf("http://%s:%d", cfg.Server.Host, port)
		}
	}
	return "http://localhost:8080"
}

func loadConfigSafe(path string) (*configForEndpoint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg configForEndpoint
	if err := json.Unmarshal(data, &cfg); err != nil {
		// Try YAML — we only need server fields
		return nil, err
	}
	return &cfg, nil
}

type configForEndpoint struct {
	Server struct {
		Host string `json:"host" yaml:"host"`
		Port int    `json:"port" yaml:"port"`
	} `json:"server" yaml:"server"`
}

// checkServerReachable verifies the server is running.
func checkServerReachable(endpoint string) error {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(endpoint + "/health")
	if err != nil {
		return fmt.Errorf("pb server is not running at %s\nStart it with: pb server start", endpoint)
	}
	resp.Body.Close()
	return nil
}

// printJSON outputs a value as formatted JSON.
func printJSON(v any) {
	data, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(data))
}

// printRawJSON outputs raw JSON with indentation.
func printRawJSON(data json.RawMessage) {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		fmt.Println(string(data))
		return
	}
	printJSON(v)
}

// newTableWriter creates a tabwriter for aligned output.
func newTableWriter() *tabwriter.Writer {
	return tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
}
