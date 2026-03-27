package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"primitivebox/internal/cvr"
)

// ---------------------------------------------------------------------------
// 1. LSP framing round-trip
// ---------------------------------------------------------------------------

func TestReadWriteMCPMessage(t *testing.T) {
	payload := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)

	var buf bytes.Buffer
	if err := writeMCPMessage(&buf, payload); err != nil {
		t.Fatalf("writeMCPMessage: %v", err)
	}

	got, err := readMCPMessage(bufio.NewReader(&buf))
	if err != nil {
		t.Fatalf("readMCPMessage: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("round-trip mismatch:\n got  %q\n want %q", got, payload)
	}
}

func TestReadWriteMCPMessageMultiple(t *testing.T) {
	messages := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"result":{"tools":[]}}`,
	}
	var buf bytes.Buffer
	for _, msg := range messages {
		if err := writeMCPMessage(&buf, []byte(msg)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	r := bufio.NewReader(&buf)
	for i, want := range messages {
		got, err := readMCPMessage(r)
		if err != nil {
			t.Fatalf("message %d read: %v", i, err)
		}
		if string(got) != want {
			t.Errorf("message %d: got %q, want %q", i, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// 2. Server name sanitization
// ---------------------------------------------------------------------------

func TestSanitizeServerName(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"GitHub MCP", "github_mcp"},
		{"my-server", "my_server"},
		{"test", "test"},
		{"GitHub", "github"},
		{"  Hello World  ", "hello_world"},
		{"123abc", "123abc"},
		{"---", "unknown"},
		{"", "unknown"},
		{"foo-bar_baz", "foo_bar_baz"},
		{"Notion API", "notion_api"},
	}
	for _, tc := range cases {
		got := sanitizeServerName(tc.input)
		if got != tc.want {
			t.Errorf("sanitizeServerName(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// In-process mock MCP server helpers
// ---------------------------------------------------------------------------

// mockMCPPair returns two bidirectional pipe ends.
//
//	client writes to clientW → server reads from serverR
//	server writes to serverW → client reads from clientR
func mockMCPPair(t *testing.T) (serverR io.ReadCloser, serverW io.WriteCloser, clientR io.ReadCloser, clientW io.WriteCloser) {
	t.Helper()
	serverR, clientW = io.Pipe()
	clientR, serverW = io.Pipe()
	return
}

// serverRead reads and unmarshals one MCP message from r.
func serverRead(t *testing.T, r *bufio.Reader) map[string]any {
	t.Helper()
	data, err := readMCPMessage(r)
	if err != nil {
		t.Fatalf("mock server read: %v", err)
	}
	var msg map[string]any
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("mock server unmarshal: %v", err)
	}
	return msg
}

// serverRespond writes one Content-Length framed JSON response to w.
func serverRespond(t *testing.T, w io.Writer, obj any) {
	t.Helper()
	payload, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("marshal mock response: %v", err)
	}
	if err := writeMCPMessage(w, payload); err != nil {
		t.Fatalf("write mock response: %v", err)
	}
}

// buildTestClient constructs an mcpClient backed by in-process pipes (no real exec.Cmd).
func buildTestClient(clientR io.ReadCloser, clientW io.WriteCloser) *mcpClient {
	return &mcpClient{
		stdin:  clientW,
		stdout: bufio.NewReader(clientR),
		doneCh: make(chan struct{}),
	}
}

// ---------------------------------------------------------------------------
// 3. initialize handshake
// ---------------------------------------------------------------------------

func TestMCPClientInitialize(t *testing.T) {
	serverR, serverW, clientR, clientW := mockMCPPair(t)
	t.Cleanup(func() {
		serverR.Close()
		serverW.Close()
		clientR.Close()
		clientW.Close()
	})

	serverBR := bufio.NewReader(serverR)
	go func() {
		msg := serverRead(t, serverBR)
		if msg["method"] != "initialize" {
			t.Errorf("expected 'initialize', got %q", msg["method"])
			return
		}
		serverRespond(t, serverW, map[string]any{
			"jsonrpc": "2.0",
			"id":      msg["id"],
			"result": map[string]any{
				"protocolVersion": "2024-11-05",
				"serverInfo":      map[string]any{"name": "GitHub MCP", "version": "1.0"},
				"capabilities":    map[string]any{},
			},
		})
		// Consume the notifications/initialized notification.
		_ = serverRead(t, serverBR)
	}()

	client := buildTestClient(clientR, clientW)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	serverName, err := client.initialize(ctx)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if serverName != "github_mcp" {
		t.Errorf("serverName = %q, want %q", serverName, "github_mcp")
	}
}

// ---------------------------------------------------------------------------
// 4. tools/list + manifest generation
// ---------------------------------------------------------------------------

func TestMCPClientListTools(t *testing.T) {
	serverR, serverW, clientR, clientW := mockMCPPair(t)
	t.Cleanup(func() {
		serverR.Close()
		serverW.Close()
		clientR.Close()
		clientW.Close()
	})

	serverBR := bufio.NewReader(serverR)
	go func() {
		msg := serverRead(t, serverBR)
		if msg["method"] != "tools/list" {
			t.Errorf("expected 'tools/list', got %q", msg["method"])
			return
		}
		serverRespond(t, serverW, map[string]any{
			"jsonrpc": "2.0",
			"id":      msg["id"],
			"result": map[string]any{
				"tools": []any{
					map[string]any{
						"name":        "echo",
						"description": "Echo back input",
						"inputSchema": map[string]any{
							"type":       "object",
							"properties": map[string]any{"message": map[string]any{"type": "string"}},
							"required":   []string{"message"},
						},
					},
					map[string]any{
						"name":        "ping",
						"description": "Ping",
						"inputSchema": map[string]any{"type": "object"},
					},
				},
			},
		})
	}()

	client := buildTestClient(clientR, clientW)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tools, err := client.listTools(ctx)
	if err != nil {
		t.Fatalf("listTools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
	if tools[0].Name != "echo" {
		t.Errorf("tools[0].Name = %q, want %q", tools[0].Name, "echo")
	}
	if tools[1].Name != "ping" {
		t.Errorf("tools[1].Name = %q, want %q", tools[1].Name, "ping")
	}

	// Verify manifest for the first tool.
	m := buildToolManifest("pb-mcp-bridge", "/tmp/test.sock", "test", tools[0])
	if m.Name != "mcp.test.echo" {
		t.Errorf("manifest.Name = %q, want %q", m.Name, "mcp.test.echo")
	}
	if m.Intent.Category != cvr.IntentMutation {
		t.Errorf("intent.category = %q, want mutation", m.Intent.Category)
	}
	if m.Intent.Reversible {
		t.Error("intent.reversible must be false")
	}
	if m.Intent.RiskLevel != cvr.RiskHigh {
		t.Errorf("intent.risk_level = %q, want high", m.Intent.RiskLevel)
	}
}

// ---------------------------------------------------------------------------
// 5. tools/call proxy
// ---------------------------------------------------------------------------

func TestMCPClientCallTool(t *testing.T) {
	serverR, serverW, clientR, clientW := mockMCPPair(t)
	t.Cleanup(func() {
		serverR.Close()
		serverW.Close()
		clientR.Close()
		clientW.Close()
	})

	serverBR := bufio.NewReader(serverR)
	go func() {
		msg := serverRead(t, serverBR)
		if msg["method"] != "tools/call" {
			t.Errorf("expected 'tools/call', got %q", msg["method"])
			return
		}
		params, _ := msg["params"].(map[string]any)
		args, _ := params["arguments"].(map[string]any)
		text, _ := args["message"].(string)
		serverRespond(t, serverW, map[string]any{
			"jsonrpc": "2.0",
			"id":      msg["id"],
			"result": map[string]any{
				"content": []any{
					map[string]any{"type": "text", "text": text},
				},
			},
		})
	}()

	client := buildTestClient(clientR, clientW)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := client.callTool(ctx, "echo", json.RawMessage(`{"message":"hello"}`))
	if err != nil {
		t.Fatalf("callTool: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("parse callTool result: %v", err)
	}
	content, _ := parsed["content"].([]any)
	if len(content) == 0 {
		t.Fatal("expected content array in result")
	}
	item, _ := content[0].(map[string]any)
	if item["text"] != "hello" {
		t.Errorf("content[0].text = %v, want %q", item["text"], "hello")
	}
}

// ---------------------------------------------------------------------------
// 6. dispatch routing
// ---------------------------------------------------------------------------

func TestDispatchMethodNotFound(t *testing.T) {
	_, _, clientR, clientW := mockMCPPair(t)
	t.Cleanup(func() { clientR.Close(); clientW.Close() })
	client := buildTestClient(clientR, clientW)

	ctx := context.Background()
	_, rpcErr := dispatch(ctx, "github", client, "mcp.wrong_server.create_issue", json.RawMessage(`{}`))
	if rpcErr == nil {
		t.Fatal("expected error for wrong server prefix")
	}
	if rpcErr.Code != -32601 {
		t.Errorf("error code = %d, want -32601", rpcErr.Code)
	}
	if !strings.Contains(rpcErr.Message, "method not found") {
		t.Errorf("error message = %q, expected 'method not found'", rpcErr.Message)
	}
}

func TestDispatchNonMCPMethodFails(t *testing.T) {
	_, _, clientR, clientW := mockMCPPair(t)
	t.Cleanup(func() { clientR.Close(); clientW.Close() })
	client := buildTestClient(clientR, clientW)

	ctx := context.Background()
	_, rpcErr := dispatch(ctx, "test", client, "process.spawn", json.RawMessage(`{}`))
	if rpcErr == nil || rpcErr.Code != -32601 {
		t.Errorf("expected -32601 for non-mcp method, got %v", rpcErr)
	}
}

func TestMCPClientRoundTripReturnsWhenServerDies(t *testing.T) {
	serverR, serverW, clientR, clientW := mockMCPPair(t)
	t.Cleanup(func() {
		serverR.Close()
		serverW.Close()
		clientR.Close()
		clientW.Close()
	})

	serverBR := bufio.NewReader(serverR)
	client := buildTestClient(clientR, clientW)

	go func() {
		_ = serverRead(t, serverBR)
		_ = serverW.Close()
		client.markDone()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := client.roundTrip(ctx, "tools/list", map[string]any{})
	if err == nil {
		t.Fatal("expected roundTrip error after MCP server death")
	}
	if !strings.Contains(err.Error(), "unavailable") && !strings.Contains(err.Error(), "read") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleConnRecoversFromDispatchPanic(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() {
		_ = serverConn.Close()
		_ = clientConn.Close()
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		handleConn(context.Background(), serverConn, "github", nil)
	}()

	_, err := clientConn.Write([]byte("{\"id\":\"req-1\",\"method\":\"mcp.github.echo\",\"params\":{}}\n"))
	if err != nil {
		t.Fatalf("write request: %v", err)
	}

	line, err := bufio.NewReader(clientConn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	var resp appRPCResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != -32603 {
		t.Fatalf("expected structured internal error, got %+v", resp)
	}

	<-done
}

// ---------------------------------------------------------------------------
// 7. buildToolManifest — intent security properties
// ---------------------------------------------------------------------------

func TestBuildToolManifest(t *testing.T) {
	tool := mcpTool{
		Name:        "create_issue",
		Description: "Creates a GitHub issue",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}`),
	}

	m := buildToolManifest("pb-mcp-bridge", "/tmp/pb-mcp.sock", "github", tool)

	if m.Name != "mcp.github.create_issue" {
		t.Errorf("Name = %q, want %q", m.Name, "mcp.github.create_issue")
	}
	if m.AppID != "pb-mcp-bridge" {
		t.Errorf("AppID = %q, want %q", m.AppID, "pb-mcp-bridge")
	}
	if m.SocketPath != "/tmp/pb-mcp.sock" {
		t.Errorf("SocketPath = %q", m.SocketPath)
	}
	if m.Intent.Category != cvr.IntentMutation {
		t.Errorf("Intent.Category = %q, want mutation", m.Intent.Category)
	}
	if m.Intent.Reversible {
		t.Error("Intent.Reversible must be false (ensures CVR checkpoint)")
	}
	if m.Intent.RiskLevel != cvr.RiskHigh {
		t.Errorf("Intent.RiskLevel = %q, want high", m.Intent.RiskLevel)
	}
	if len(m.Intent.AffectedScopes) != 1 || m.Intent.AffectedScopes[0] != "app:mcp" {
		t.Errorf("Intent.AffectedScopes = %v, want [app:mcp]", m.Intent.AffectedScopes)
	}

	// InputSchema must be valid JSON and preserve the original schema.
	var schema map[string]any
	if err := json.Unmarshal(m.InputSchema, &schema); err != nil {
		t.Fatalf("InputSchema invalid JSON: %v", err)
	}
	if schema["type"] != "object" {
		t.Errorf("InputSchema.type = %v, want object", schema["type"])
	}

	// Empty description falls back to generated string.
	noDesc := buildToolManifest("x", "/s", "srv", mcpTool{Name: "foo"})
	if noDesc.Description == "" {
		t.Error("empty description should be generated")
	}

	// Nil InputSchema falls back to {"type":"object"}.
	noSchema := buildToolManifest("x", "/s", "srv", mcpTool{Name: "bar", InputSchema: nil})
	if string(noSchema.InputSchema) != `{"type":"object"}` {
		t.Errorf("nil inputSchema fallback = %q", noSchema.InputSchema)
	}
}
