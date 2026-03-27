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
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"primitivebox/internal/cvr"
	"primitivebox/internal/primitive"
)

const defaultAppID = "pb-mcp-bridge"

// ---------------------------------------------------------------------------
// CLI config
// ---------------------------------------------------------------------------

type bridgeConfig struct {
	socketPath  string
	rpcEndpoint string
	appID       string
	noRegister  bool
	mcpCommand  string
	mcpArgs     []string
}

// ---------------------------------------------------------------------------
// App adapter wire types (mirror pb-os-adapter)
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// MCP tool descriptor
// ---------------------------------------------------------------------------

type mcpTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// ---------------------------------------------------------------------------
// MCP Content-Length framing (LSP-style)
// ---------------------------------------------------------------------------

// readMCPMessage reads one Content-Length framed message from r.
// Format: "Content-Length: N\r\n\r\n" followed by N bytes of JSON.
func readMCPMessage(r *bufio.Reader) ([]byte, error) {
	var header []byte
	for {
		b, err := r.ReadByte()
		if err != nil {
			return nil, fmt.Errorf("mcp read header: %w", err)
		}
		header = append(header, b)
		if len(header) >= 4 && bytes.Equal(header[len(header)-4:], []byte("\r\n\r\n")) {
			break
		}
	}

	headerStr := string(header)
	const clPrefix = "Content-Length: "
	idx := strings.Index(headerStr, clPrefix)
	if idx < 0 {
		return nil, fmt.Errorf("mcp: missing Content-Length header in %q", headerStr)
	}
	rest := headerStr[idx+len(clPrefix):]
	end := strings.Index(rest, "\r\n")
	if end < 0 {
		return nil, fmt.Errorf("mcp: malformed Content-Length line")
	}
	var length int
	if _, err := fmt.Sscanf(rest[:end], "%d", &length); err != nil {
		return nil, fmt.Errorf("mcp: parse Content-Length: %w", err)
	}
	if length <= 0 || length > 64*1024*1024 {
		return nil, fmt.Errorf("mcp: Content-Length %d out of range", length)
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, fmt.Errorf("mcp read body: %w", err)
	}
	return body, nil
}

// writeMCPMessage prepends Content-Length framing and writes payload to w.
func writeMCPMessage(w io.Writer, payload []byte) error {
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(payload))
	if _, err := io.WriteString(w, header); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// ---------------------------------------------------------------------------
// MCP client
// ---------------------------------------------------------------------------

type mcpClient struct {
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	stdout       *bufio.Reader
	stdoutCloser io.ReadCloser

	// callMu serializes complete request-response cycles so that concurrent
	// Unix socket connections cannot interleave reads from the MCP server's
	// stdout. Held from the moment the request is written until the response
	// is fully read (or the context is cancelled and stdin is closed).
	callMu sync.Mutex
	nextID atomic.Int64
	doneCh chan struct{}
	doneMu sync.Once
}

func newMCPClient(command string, args []string) (*mcpClient, error) {
	cmd := exec.Command(command, args...)
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp start: %w", err)
	}
	return &mcpClient{
		cmd:          cmd,
		stdin:        stdin,
		stdout:       bufio.NewReaderSize(stdout, 64*1024),
		stdoutCloser: stdout,
		doneCh:       make(chan struct{}),
	}, nil
}

func (c *mcpClient) markDone() {
	c.doneMu.Do(func() {
		close(c.doneCh)
	})
}

// watchCrash blocks until the MCP server process exits, then closes doneCh.
// Must be called in a separate goroutine.
func (c *mcpClient) watchCrash() {
	if c.cmd != nil {
		_ = c.cmd.Wait()
	}
	c.markDone()
}

// roundTrip sends a JSON-RPC request and waits for the matching response.
// callMu is held for the full send+recv cycle; on context cancellation stdin
// is closed to unblock the reader goroutine before returning.
func (c *mcpClient) roundTrip(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.callMu.Lock()
	defer c.callMu.Unlock()

	select {
	case <-c.doneCh:
		return nil, errors.New("mcp server unavailable")
	default:
	}

	id := c.nextID.Add(1)
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if err := writeMCPMessage(c.stdin, payload); err != nil {
		return nil, fmt.Errorf("mcp write %s: %w", method, err)
	}

	type readResult struct {
		data json.RawMessage
		err  error
	}
	ch := make(chan readResult, 1)
	go func() {
		data, err := readMCPMessage(c.stdout)
		ch <- readResult{data, err}
	}()

	select {
	case <-ctx.Done():
		// Cancelation tears down the MCP session so blocked readers/writers
		// cannot leak behind a dead request boundary.
		c.close()
		<-ch // wait so the goroutine completes before callMu is released
		return nil, ctx.Err()
	case <-c.doneCh:
		<-ch
		return nil, errors.New("mcp server unavailable")
	case res := <-ch:
		if res.err != nil {
			return nil, fmt.Errorf("mcp read %s response: %w", method, res.err)
		}
		return res.data, nil
	}
}

// sendNotification sends a JSON-RPC notification (no response expected).
func (c *mcpClient) sendNotification(method string, params any) error {
	c.callMu.Lock()
	defer c.callMu.Unlock()
	msg := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		msg["params"] = params
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return writeMCPMessage(c.stdin, payload)
}

// mcpError wraps the JSON-RPC error object returned by the server.
type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *mcpError) Error() string {
	return fmt.Sprintf("mcp error %d: %s", e.Code, e.Message)
}

// initialize performs the MCP handshake and returns the sanitized server name.
func (c *mcpClient) initialize(ctx context.Context) (string, error) {
	raw, err := c.roundTrip(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "pb-mcp-bridge", "version": "0.1.0"},
	})
	if err != nil {
		return "", err
	}

	var envelope struct {
		ID     int64 `json:"id"`
		Result struct {
			ServerInfo struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		} `json:"result"`
		Error *mcpError `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return "", fmt.Errorf("parse initialize response: %w", err)
	}
	if envelope.Error != nil {
		return "", envelope.Error
	}

	name := envelope.Result.ServerInfo.Name
	if name == "" {
		name = "unknown"
	}

	// Notifications are fire-and-forget; they must not acquire callMu via
	// sendNotification while we still hold it from roundTrip — but roundTrip
	// has already returned here, so callMu is released. Safe to call.
	if err := c.sendNotification("notifications/initialized", nil); err != nil {
		return "", fmt.Errorf("send initialized: %w", err)
	}
	return sanitizeServerName(name), nil
}

// listTools calls tools/list and returns the tool slice.
func (c *mcpClient) listTools(ctx context.Context) ([]mcpTool, error) {
	raw, err := c.roundTrip(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}

	var envelope struct {
		Result struct {
			Tools []mcpTool `json:"tools"`
		} `json:"result"`
		Error *mcpError `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("parse tools/list response: %w", err)
	}
	if envelope.Error != nil {
		return nil, envelope.Error
	}
	return envelope.Result.Tools, nil
}

// callTool proxies a tools/call to the MCP server.
func (c *mcpClient) callTool(ctx context.Context, toolName string, arguments json.RawMessage) (json.RawMessage, error) {
	var args any
	if len(arguments) > 0 && string(arguments) != "null" {
		var parsed any
		if err := json.Unmarshal(arguments, &parsed); err != nil {
			return nil, fmt.Errorf("invalid tool arguments: %w", err)
		}
		args = parsed
	} else {
		args = map[string]any{}
	}

	raw, err := c.roundTrip(ctx, "tools/call", map[string]any{
		"name":      toolName,
		"arguments": args,
	})
	if err != nil {
		return nil, err
	}

	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  *mcpError       `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("parse tools/call response: %w", err)
	}
	if envelope.Error != nil {
		return nil, envelope.Error
	}
	return envelope.Result, nil
}

func (c *mcpClient) close() {
	_ = c.stdin.Close()
	if c.stdoutCloser != nil {
		_ = c.stdoutCloser.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	c.markDone()
}

// ---------------------------------------------------------------------------
// Primitive name sanitization
// ---------------------------------------------------------------------------

var nonAlphanumUnderscore = regexp.MustCompile(`[^a-z0-9_]+`)

func sanitizeServerName(raw string) string {
	lower := strings.ToLower(raw)
	sanitized := nonAlphanumUnderscore.ReplaceAllString(lower, "_")
	sanitized = strings.Trim(sanitized, "_")
	if sanitized == "" {
		return "unknown"
	}
	return sanitized
}

// ---------------------------------------------------------------------------
// Manifest building
// ---------------------------------------------------------------------------

// mcpIntent is the default intent for ALL MCP tools: mutation/high/irreversible.
// We cannot verify what an unknown external tool does, so we default to the
// most conservative posture — this forces every call through the CVR checkpoint
// and Reviewer Gate before execution.
var mcpIntent = cvr.PrimitiveIntent{
	Category:       cvr.IntentMutation,
	Reversible:     false,
	RiskLevel:      cvr.RiskHigh,
	AffectedScopes: []string{"app:mcp"},
}

func buildToolManifest(appID, socketPath, serverName string, tool mcpTool) primitive.AppPrimitiveManifest {
	inputSchema := tool.InputSchema
	if len(inputSchema) == 0 || string(inputSchema) == "null" {
		inputSchema = json.RawMessage(`{"type":"object"}`)
	}
	desc := tool.Description
	if desc == "" {
		desc = fmt.Sprintf("MCP tool %s.%s", serverName, tool.Name)
	}
	return primitive.AppPrimitiveManifest{
		AppID:        appID,
		Name:         "mcp." + serverName + "." + tool.Name,
		Description:  desc,
		InputSchema:  inputSchema,
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		SocketPath:   socketPath,
		Intent:       mcpIntent,
	}
}

// ---------------------------------------------------------------------------
// Unix socket server
// ---------------------------------------------------------------------------

func listenUnix(socketPath string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return nil, err
	}
	_ = os.Remove(socketPath)
	return net.Listen("unix", socketPath)
}

func serve(ctx context.Context, listener net.Listener, serverName string, client *mcpClient) error {
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
		go handleConn(ctx, conn, serverName, client)
	}
}

func handleConn(ctx context.Context, conn net.Conn, serverName string, client *mcpClient) {
	defer conn.Close()
	var req appRPCRequest
	defer func() {
		if recovered := recover(); recovered != nil {
			_ = writeAppResponse(conn, appRPCResponse{
				ID:    req.ID,
				Error: &appRPCError{Code: -32603, Message: fmt.Sprintf("internal adapter error: %v", recovered)},
			})
		}
	}()
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		_ = writeAppResponse(conn, appRPCResponse{
			ID:    0,
			Error: &appRPCError{Code: -32600, Message: "invalid request"},
		})
		return
	}
	if err := json.Unmarshal(line, &req); err != nil {
		_ = writeAppResponse(conn, appRPCResponse{
			ID:    0,
			Error: &appRPCError{Code: -32600, Message: "invalid request"},
		})
		return
	}
	result, rpcErr := dispatch(ctx, serverName, client, req.Method, req.Params)
	resp := appRPCResponse{ID: req.ID, Result: result}
	if rpcErr != nil {
		resp.Result = nil
		resp.Error = rpcErr
	}
	_ = writeAppResponse(conn, resp)
}

// dispatch routes an incoming PrimitiveBox RPC call to the MCP server.
// method must have the form "mcp.<serverName>.<toolName>".
func dispatch(ctx context.Context, serverName string, client *mcpClient, method string, raw json.RawMessage) (any, *appRPCError) {
	prefix := "mcp." + serverName + "."
	if !strings.HasPrefix(method, prefix) {
		return nil, &appRPCError{Code: -32601, Message: "method not found: " + method}
	}
	toolName := strings.TrimPrefix(method, prefix)
	result, err := client.callTool(ctx, toolName, raw)
	if err != nil {
		return nil, &appRPCError{Code: -32603, Message: err.Error()}
	}
	var data any
	if err := json.Unmarshal(result, &data); err != nil {
		return nil, &appRPCError{Code: -32603, Message: "invalid response from MCP server"}
	}
	return data, nil
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

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

	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Do(req)
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

func writeAppResponse(w io.Writer, resp appRPCResponse) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
}

func mustJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

// ---------------------------------------------------------------------------
// Entry point
// ---------------------------------------------------------------------------

func parseFlags() bridgeConfig {
	cfg := bridgeConfig{}
	flag.StringVar(&cfg.socketPath, "socket", filepath.Join(os.TempDir(), "pb-mcp.sock"), "Unix socket path")
	flag.StringVar(&cfg.rpcEndpoint, "rpc-endpoint", "", "PrimitiveBox HTTP endpoint for app.register")
	flag.StringVar(&cfg.appID, "app-id", defaultAppID, "Override app_id for registered primitives")
	flag.BoolVar(&cfg.noRegister, "no-register", false, "Skip app.register calls")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		log.Fatal("usage: pb-mcp-bridge [flags] -- <mcp-server-command> [args...]")
	}
	cfg.mcpCommand = args[0]
	if len(args) > 1 {
		cfg.mcpArgs = args[1:]
	}
	return cfg
}

func main() {
	cfg := parseFlags()
	if err := run(cfg); err != nil {
		log.Fatal(err)
	}
}

func run(cfg bridgeConfig) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	client, err := newMCPClient(cfg.mcpCommand, cfg.mcpArgs)
	if err != nil {
		return fmt.Errorf("start MCP server: %w", err)
	}

	// Cancel ctx when the child MCP server dies.
	ctx, cancelOnCrash := context.WithCancel(ctx)
	defer cancelOnCrash()
	go func() {
		client.watchCrash()
		log.Println("pb-mcp-bridge: MCP server exited, shutting down")
		cancelOnCrash()
	}()

	serverName, err := client.initialize(ctx)
	if err != nil {
		client.close()
		return fmt.Errorf("MCP initialize: %w", err)
	}
	log.Printf("pb-mcp-bridge: connected to MCP server %q", serverName)

	tools, err := client.listTools(ctx)
	if err != nil {
		client.close()
		return fmt.Errorf("MCP tools/list: %w", err)
	}
	log.Printf("pb-mcp-bridge: discovered %d tool(s)", len(tools))

	manifests := make([]primitive.AppPrimitiveManifest, 0, len(tools))
	for _, tool := range tools {
		manifests = append(manifests, buildToolManifest(cfg.appID, cfg.socketPath, serverName, tool))
	}

	listener, err := listenUnix(cfg.socketPath)
	if err != nil {
		client.close()
		return fmt.Errorf("listen unix: %w", err)
	}
	defer func() { _ = listener.Close() }()

	errCh := make(chan error, 1)
	go func() {
		errCh <- serve(ctx, listener, serverName, client)
	}()

	if !cfg.noRegister {
		if strings.TrimSpace(cfg.rpcEndpoint) == "" {
			client.close()
			return errors.New("--rpc-endpoint is required unless --no-register is set")
		}
		for _, manifest := range manifests {
			if err := registerPrimitive(ctx, cfg.rpcEndpoint, manifest); err != nil {
				client.close()
				return fmt.Errorf("register %s: %w", manifest.Name, err)
			}
			log.Printf("pb-mcp-bridge: registered %s", manifest.Name)
		}
	}

	select {
	case <-ctx.Done():
		_ = listener.Close()
		return nil
	case err := <-errCh:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			return err
		}
		return nil
	}
}
