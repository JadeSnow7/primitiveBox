// Package rpc implements a JSON-RPC 2.0 server over HTTP.
package rpc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	pathpkg "path"
	"strconv"
	"strings"
	"sync"
	"time"

	"primitivebox/internal/audit"
	"primitivebox/internal/eventing"
	"primitivebox/internal/primitive"
	"primitivebox/internal/runtrace"
	"primitivebox/internal/sandbox"
)

// Request represents a JSON-RPC 2.0 request.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      any             `json:"id"`
}

// Response represents a JSON-RPC 2.0 response.
type Response struct {
	JSONRPC string `json:"jsonrpc"`
	Result  any    `json:"result,omitempty"`
	Error   *Error `json:"error,omitempty"`
	ID      any    `json:"id"`
}

// Error represents a JSON-RPC 2.0 error.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// Server is the JSON-RPC 2.0 HTTP server that dispatches calls to primitives.
type Server struct {
	registry   *primitive.Registry
	auditor    *audit.Logger
	manager    *sandbox.Manager
	eventBus   *eventing.Bus
	eventStore eventing.Store

	listener   net.Listener
	server     *http.Server
	httpClient *http.Client
	uiFS       fs.FS
	mu         sync.Mutex
}

// NewServer creates a new JSON-RPC server bound to the given primitive registry.
func NewServer(registry *primitive.Registry, auditor *audit.Logger, manager *sandbox.Manager) *Server {
	return &Server{
		registry: registry,
		auditor:  auditor,
		manager:  manager,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// AttachEventing wires the event bus/store used by streaming and inspector APIs.
func (s *Server) AttachEventing(bus *eventing.Bus, store eventing.Store) {
	s.eventBus = bus
	s.eventStore = store
}

// AttachUI wires embedded inspector UI assets.
func (s *Server) AttachUI(uiFS fs.FS) {
	s.uiFS = uiFS
}

// ListenAndServe starts the HTTP server on the given address.
func (s *Server) ListenAndServe(addr string) error {
	s.server = &http.Server{
		Handler:      s.Handler(),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0,
	}

	var err error
	s.listener, err = net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	log.Printf("[RPC] Server listening on %s", s.listener.Addr().String())
	return s.server.Serve(s.listener)
}

// Handler returns the HTTP handler for the JSON-RPC server.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/rpc", s.handleRPC)
	mux.HandleFunc("/rpc/stream", s.handleRPCStream)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/primitives", s.handleListPrimitives)
	mux.HandleFunc("/sandboxes", s.handleSandboxes)
	mux.HandleFunc("/sandboxes/", s.handleSandboxRoute)
	mux.HandleFunc("/api/v1/", s.handleAPI)
	if s.uiFS != nil {
		mux.HandleFunc("/", s.handleUI)
	}
	return mux
}

// Addr returns the actual listen address.
func (s *Server) Addr() string {
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return ""
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}

func (s *Server) handleRPC(w http.ResponseWriter, r *http.Request) {
	s.handleRPCRequest(w, r, false)
}

func (s *Server) handleRPCStream(w http.ResponseWriter, r *http.Request) {
	s.handleRPCRequest(w, r, true)
}

func (s *Server) handleRPCRequest(w http.ResponseWriter, r *http.Request, stream bool) {
	var req Request
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Printf("[RPC] Recovered from panic in %s: %v", req.Method, recovered)
			if stream {
				s.writeStreamEvent(w, "error", map[string]any{
					"message": fmt.Sprintf("panic while handling %s", req.Method),
				})
				return
			}
			s.writeResponse(w, Response{
				JSONRPC: "2.0",
				Error: &Error{
					Code:    CodeInternalError,
					Message: "internal server error",
					Data:    fmt.Sprintf("panic while handling %s", req.Method),
				},
				ID: req.ID,
			})
		}
	}()

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if stream {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		s.writeResponse(w, Response{
			JSONRPC: "2.0",
			Error:   &Error{Code: CodeParseError, Message: "invalid JSON: " + err.Error()},
			ID:      nil,
		})
		return
	}

	if req.JSONRPC != "2.0" {
		if stream {
			http.Error(w, "jsonrpc must be '2.0'", http.StatusBadRequest)
			return
		}
		s.writeResponse(w, Response{
			JSONRPC: "2.0",
			Error:   &Error{Code: CodeInvalidRequest, Message: "jsonrpc must be '2.0'"},
			ID:      req.ID,
		})
		return
	}

	prim, ok := s.registry.Get(req.Method)
	if !ok {
		if stream {
			http.Error(w, "unknown primitive: "+req.Method, http.StatusNotFound)
			return
		}
		s.writeResponse(w, Response{
			JSONRPC: "2.0",
			Error:   &Error{Code: CodeMethodNotFound, Message: "unknown primitive: " + req.Method},
			ID:      req.ID,
		})
		return
	}

	var sinks []eventing.Sink
	if s.eventBus != nil {
		sinks = append(sinks, eventing.SinkFunc(func(ctx context.Context, evt eventing.Event) {
			if evt.Method == "" {
				evt.Method = req.Method
			}
			s.eventBus.Publish(ctx, evt)
		}))
	}

	if stream {
		if !supportsStreaming(w) {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		setSSEHeaders(w)
		sinks = append(sinks, streamSink{writer: w, method: req.Method})
		s.writeStreamEvent(w, "started", map[string]any{
			"method": req.Method,
			"id":     req.ID,
		})
	}

	ctx := eventing.WithSink(r.Context(), eventing.NewMultiSink(sinks...))
	ctx, traceRecorder := runtrace.WithRecorder(ctx)
	if s.eventBus != nil {
		s.eventBus.Publish(ctx, eventing.Event{
			Type:    "rpc.started",
			Source:  "rpc",
			Method:  req.Method,
			Message: req.Method,
			Data: eventing.MustJSON(map[string]any{
				"id": req.ID,
			}),
		})
	}
	start := time.Now()
	result, err := prim.Execute(ctx, req.Params)
	duration := time.Since(start)

	if s.auditor != nil {
		s.auditor.LogCall(req.Method, req.Params, result.Data, err, duration)
	}
	if s.eventBus != nil {
		eventType := "rpc.completed"
		if err != nil {
			eventType = "rpc.error"
		}
		s.eventBus.Publish(ctx, eventing.Event{
			Type:    eventType,
			Source:  "rpc",
			Method:  req.Method,
			Message: req.Method,
			Data: eventing.MustJSON(map[string]any{
				"duration_ms": duration.Milliseconds(),
				"success":     err == nil,
			}),
		})
	}

	if err != nil {
		if traceRecord, ok := traceRecorder.Record(); ok {
			if encoded, encodeErr := runtrace.EncodeHeader(traceRecord); encodeErr == nil {
				w.Header().Set(runtrace.HeaderTraceStep, encoded)
			}
			if store, ok := s.eventStore.(runtrace.Store); ok {
				_ = store.RecordTraceStep(ctx, traceRecord)
			}
		}
		if stream {
			s.writeStreamEvent(w, "error", map[string]any{
				"method":  req.Method,
				"message": err.Error(),
			})
			return
		}
		s.writePrimitiveError(w, req.ID, err)
		return
	}

	if stream {
		if traceRecord, ok := traceRecorder.Record(); ok {
			if encoded, encodeErr := runtrace.EncodeHeader(traceRecord); encodeErr == nil {
				w.Header().Set(runtrace.HeaderTraceStep, encoded)
			}
			if store, ok := s.eventStore.(runtrace.Store); ok {
				_ = store.RecordTraceStep(ctx, traceRecord)
			}
		}
		s.writeStreamEvent(w, "completed", map[string]any{
			"method":      req.Method,
			"result":      result,
			"duration_ms": duration.Milliseconds(),
		})
		return
	}

	if traceRecord, ok := traceRecorder.Record(); ok {
		if encoded, encodeErr := runtrace.EncodeHeader(traceRecord); encodeErr == nil {
			w.Header().Set(runtrace.HeaderTraceStep, encoded)
		}
		if store, ok := s.eventStore.(runtrace.Store); ok {
			_ = store.RecordTraceStep(ctx, traceRecord)
		}
	}
	s.writeResponse(w, Response{
		JSONRPC: "2.0",
		Result:  result,
		ID:      req.ID,
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleListPrimitives(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"primitives": s.registry.Schemas(),
	})
}

func (s *Server) handleSandboxes(w http.ResponseWriter, r *http.Request) {
	if s.manager == nil {
		http.Error(w, "sandbox manager unavailable", http.StatusNotImplemented)
		return
	}
	if r.URL.Path != "/sandboxes" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sandboxes, err := s.manager.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"sandboxes": sandboxes})
}

func (s *Server) handleSandboxRoute(w http.ResponseWriter, r *http.Request) {
	if s.manager == nil {
		http.Error(w, "sandbox manager unavailable", http.StatusNotImplemented)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/sandboxes/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}

	sandboxID := parts[0]
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		sb, err := s.manager.Inspect(r.Context(), sandboxID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sb)
		return
	}

	switch parts[1] {
	case "health":
		s.handleSandboxHealth(w, r, sandboxID)
	case "primitives":
		s.proxySandboxRequest(w, r, sandboxID, "/primitives", "sandbox.primitives")
	case "rpc":
		if len(parts) > 2 && parts[2] == "stream" {
			s.proxySandboxStreamRequest(w, r, sandboxID)
			return
		}
		s.proxySandboxRequest(w, r, sandboxID, "/rpc", "sandbox.rpc")
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleSandboxHealth(w http.ResponseWriter, r *http.Request, sandboxID string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.proxySandboxRequest(w, r, sandboxID, "/health", "sandbox.health")
}

func (s *Server) proxySandboxRequest(w http.ResponseWriter, r *http.Request, sandboxID, targetPath, auditMethod string) {
	sb, err := s.manager.Inspect(r.Context(), sandboxID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if sb.Status != sandbox.StatusRunning {
		http.Error(w, fmt.Sprintf("sandbox %s is not running", sandboxID), http.StatusConflict)
		return
	}
	if sb.RPCEndpoint == "" {
		http.Error(w, fmt.Sprintf("sandbox %s has no rpc endpoint", sandboxID), http.StatusBadGateway)
		return
	}

	targetURL := sb.RPCEndpoint + targetPath
	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(r.Body)
	}
	proxiedRequest := decodeRPCRequest(body)
	if s.eventBus != nil && proxiedRequest != nil {
		s.eventBus.Publish(r.Context(), eventing.Event{
			Type:      "rpc.started",
			Source:    "rpc",
			SandboxID: sandboxID,
			Method:    proxiedRequest.Method,
			Message:   proxiedRequest.Method,
			Data: eventing.MustJSON(map[string]any{
				"id": proxiedRequest.ID,
			}),
		})
	}

	req, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, bytes.NewReader(body))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	req.Header.Set("Content-Type", r.Header.Get("Content-Type"))

	start := time.Now()
	resp, err := s.httpClient.Do(req)
	duration := time.Since(start)
	if s.auditor != nil {
		var input json.RawMessage
		if len(body) > 0 {
			input = append(json.RawMessage(nil), body...)
		}
		s.auditor.LogCallWithMetadata(auditMethod, input, nil, err, duration, map[string]string{
			"sandbox_id": sandboxID,
			"target_rpc": targetURL,
		})
	}
	if s.eventBus != nil {
		s.eventBus.Publish(r.Context(), eventing.Event{
			Type:      "sandbox.proxy",
			Source:    "rpc",
			SandboxID: sandboxID,
			Method:    auditMethod,
			Message:   targetPath,
			Data: eventing.MustJSON(map[string]any{
				"duration_ms": duration.Milliseconds(),
				"success":     err == nil,
			}),
		})
	}
	if err != nil {
		s.publishProxyRPCResult(r.Context(), sandboxID, proxiedRequest, duration, 0, nil, err)
		http.Error(w, fmt.Sprintf("sandbox proxy request failed: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if encoded := resp.Header.Get(runtrace.HeaderTraceStep); encoded != "" {
		if record, decodeErr := runtrace.DecodeHeader(encoded); decodeErr == nil {
			if record.SandboxID == "" {
				record.SandboxID = sandboxID
			}
			if store, ok := s.eventStore.(runtrace.Store); ok {
				_ = store.RecordTraceStep(r.Context(), record)
			}
		}
	}

	_ = s.manager.Touch(r.Context(), sandboxID)
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)

	if !shouldCaptureProxyRPCResponse(targetPath, resp) {
		s.publishProxyRPCResult(r.Context(), sandboxID, proxiedRequest, duration, resp.StatusCode, nil, nil)
		_, _ = io.Copy(w, resp.Body)
		return
	}

	responseBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		s.publishProxyRPCResult(r.Context(), sandboxID, proxiedRequest, duration, resp.StatusCode, nil, readErr)
		http.Error(w, fmt.Sprintf("sandbox proxy response failed: %v", readErr), http.StatusBadGateway)
		return
	}
	var rpcResp Response
	if json.Unmarshal(responseBody, &rpcResp) == nil {
		s.publishProxyRPCResult(r.Context(), sandboxID, proxiedRequest, duration, resp.StatusCode, &rpcResp, nil)
	} else {
		s.publishProxyRPCResult(r.Context(), sandboxID, proxiedRequest, duration, resp.StatusCode, nil, nil)
	}
	_, _ = w.Write(responseBody)
}

func (s *Server) proxySandboxStreamRequest(w http.ResponseWriter, r *http.Request, sandboxID string) {
	sb, err := s.manager.Inspect(r.Context(), sandboxID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if sb.Status != sandbox.StatusRunning {
		http.Error(w, fmt.Sprintf("sandbox %s is not running", sandboxID), http.StatusConflict)
		return
	}
	if sb.RPCEndpoint == "" {
		http.Error(w, fmt.Sprintf("sandbox %s has no rpc endpoint", sandboxID), http.StatusBadGateway)
		return
	}
	if !supportsStreaming(w) {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	body, _ := io.ReadAll(r.Body)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, sb.RPCEndpoint+"/rpc/stream", bytes.NewReader(body))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		http.Error(w, fmt.Sprintf("sandbox proxy request failed: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	setSSEHeaders(w)
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	flusher := w.(http.Flusher)
	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			_, _ = w.Write(line)
			flusher.Flush()
		}
		if err != nil {
			break
		}
	}
	_ = s.manager.Touch(r.Context(), sandboxID)
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/api/v1/") {
		http.NotFound(w, r)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/v1/")
	switch {
	case path == "sandboxes":
		s.handleAPISandboxes(w, r)
	case path == "events":
		s.handleAPIEvents(w, r)
	case path == "events/stream":
		s.handleAPIEventStream(w, r)
	case strings.HasPrefix(path, "sandboxes/"):
		s.handleAPISandboxDetail(w, r, strings.TrimPrefix(path, "sandboxes/"))
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleAPISandboxes(w http.ResponseWriter, r *http.Request) {
	if s.manager == nil {
		http.Error(w, "sandbox manager unavailable", http.StatusNotImplemented)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	items, err := s.manager.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"sandboxes": items})
}

func (s *Server) handleAPISandboxDetail(w http.ResponseWriter, r *http.Request, path string) {
	if s.manager == nil {
		http.Error(w, "sandbox manager unavailable", http.StatusNotImplemented)
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	sandboxID := parts[0]
	if len(parts) == 1 {
		sb, err := s.manager.Inspect(r.Context(), sandboxID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		writeJSON(w, sb)
		return
	}

	switch parts[1] {
	case "tree":
		result, err := s.callSandboxPrimitive(r.Context(), sandboxID, "fs.list", map[string]any{
			"path":      defaultString(r.URL.Query().Get("path"), "."),
			"recursive": queryBool(r.URL.Query().Get("recursive"), true),
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, result)
	case "checkpoints":
		result, err := s.callSandboxPrimitive(r.Context(), sandboxID, "state.list", map[string]any{})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, result)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleAPIEvents(w http.ResponseWriter, r *http.Request) {
	if s.eventStore == nil {
		http.Error(w, "event store unavailable", http.StatusNotImplemented)
		return
	}
	filter := eventing.ListFilter{
		SandboxID: r.URL.Query().Get("sandbox_id"),
		Method:    r.URL.Query().Get("method"),
		Type:      r.URL.Query().Get("type"),
		Limit:     queryInt(r.URL.Query().Get("limit"), 100),
	}
	events, err := s.eventStore.ListEvents(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"events": events})
}

func (s *Server) handleAPIEventStream(w http.ResponseWriter, r *http.Request) {
	if s.eventBus == nil {
		http.Error(w, "event bus unavailable", http.StatusNotImplemented)
		return
	}
	if !supportsStreaming(w) {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	setSSEHeaders(w)
	ch, cancel := s.eventBus.Subscribe(64)
	defer cancel()
	for {
		select {
		case <-r.Context().Done():
			return
		case evt := <-ch:
			s.writeStreamEvent(w, evt.Type, evt)
		}
	}
}

func (s *Server) callSandboxPrimitive(ctx context.Context, sandboxID, method string, params map[string]any) (any, error) {
	sb, err := s.manager.Inspect(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	if sb.Status != sandbox.StatusRunning {
		return nil, fmt.Errorf("sandbox %s is not running", sandboxID)
	}

	body, _ := json.Marshal(Request{
		JSONRPC: "2.0",
		Method:  method,
		Params:  eventing.MustJSON(params),
		ID:      "inspector",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sb.RPCEndpoint+"/rpc", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var rpcResp Response
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, err
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("%s", rpcResp.Error.Message)
	}
	return rpcResp.Result, nil
}

func (s *Server) writePrimitiveError(w http.ResponseWriter, id any, err error) {
	errCode := CodeInternalError
	if pe, ok := err.(*primitive.PrimitiveError); ok {
		switch pe.Code {
		case primitive.ErrNotFound, primitive.ErrPermission, primitive.ErrValidation:
			errCode = CodeInvalidParams
		case primitive.ErrTimeout:
			errCode = CodeInternalError
		}
	}
	s.writeResponse(w, Response{
		JSONRPC: "2.0",
		Error:   &Error{Code: errCode, Message: err.Error()},
		ID:      id,
	})
}

func decodeRPCRequest(body []byte) *Request {
	if len(body) == 0 {
		return nil
	}
	var req Request
	if err := json.Unmarshal(body, &req); err != nil {
		return nil
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		return nil
	}
	return &req
}

func shouldCaptureProxyRPCResponse(targetPath string, resp *http.Response) bool {
	if targetPath != "/rpc" || resp == nil {
		return false
	}
	if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "application/json") {
		return false
	}
	const maxInspectableRPCBody = 2 << 20
	return resp.ContentLength >= 0 && resp.ContentLength <= maxInspectableRPCBody
}

func (s *Server) publishProxyRPCResult(ctx context.Context, sandboxID string, req *Request, duration time.Duration, statusCode int, resp *Response, transportErr error) {
	if s.eventBus == nil || req == nil {
		return
	}
	if transportErr != nil {
		s.eventBus.Publish(ctx, eventing.Event{
			Type:      "rpc.error",
			Source:    "rpc",
			SandboxID: sandboxID,
			Method:    req.Method,
			Message:   transportErr.Error(),
			Data: eventing.MustJSON(map[string]any{
				"duration_ms": duration.Milliseconds(),
				"status_code": statusCode,
			}),
		})
		return
	}
	if resp != nil && resp.Error != nil {
		s.eventBus.Publish(ctx, eventing.Event{
			Type:      "rpc.error",
			Source:    "rpc",
			SandboxID: sandboxID,
			Method:    req.Method,
			Message:   resp.Error.Message,
			Data: eventing.MustJSON(map[string]any{
				"duration_ms": duration.Milliseconds(),
				"status_code": statusCode,
				"code":        resp.Error.Code,
			}),
		})
		return
	}
	s.eventBus.Publish(ctx, eventing.Event{
		Type:      "rpc.completed",
		Source:    "rpc",
		SandboxID: sandboxID,
		Method:    req.Method,
		Message:   req.Method,
		Data: eventing.MustJSON(map[string]any{
			"duration_ms": duration.Milliseconds(),
			"status_code": statusCode,
			"success":     statusCode < http.StatusBadRequest,
		}),
	})
}

func (s *Server) writeResponse(w http.ResponseWriter, resp Response) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("[RPC] Failed to encode response: %v", err)
	}
}

func (s *Server) writeStreamEvent(w http.ResponseWriter, name string, payload any) {
	writeSSEEvent(w, name, payload)
}

func writeSSEEvent(w http.ResponseWriter, name string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "event: %s\n", name)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

type streamSink struct {
	writer http.ResponseWriter
	method string
}

func (s streamSink) Emit(ctx context.Context, evt eventing.Event) {
	_ = ctx
	if evt.Method == "" {
		evt.Method = s.method
	}
	name := "progress"
	switch evt.Type {
	case "shell.started":
		name = "started"
	case "shell.output":
		if evt.Stream == "stderr" {
			name = "stderr"
		} else {
			name = "stdout"
		}
	case "shell.completed", "rpc.completed":
		name = "completed"
	case "rpc.error":
		name = "error"
	default:
		switch {
		case strings.HasSuffix(evt.Type, ".started"):
			name = "started"
		case strings.HasSuffix(evt.Type, ".completed"):
			name = "completed"
		case strings.HasSuffix(evt.Type, ".error"):
			name = "error"
		}
	}
	data := map[string]any{
		"type":      evt.Type,
		"method":    evt.Method,
		"stream":    evt.Stream,
		"message":   evt.Message,
		"timestamp": evt.Timestamp,
	}
	if evt.Data != nil {
		data["data"] = json.RawMessage(evt.Data)
	}
	writeSSEEvent(s.writer, name, data)
}

func supportsStreaming(w http.ResponseWriter) bool {
	_, ok := w.(http.Flusher)
	return ok
}

func setSSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
}

func copyHeaders(dst, src http.Header) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	if s.uiFS == nil {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cleanPath := strings.TrimPrefix(pathClean(r.URL.Path), "/")
	if cleanPath == "" {
		cleanPath = "index.html"
	}
	if file, err := s.uiFS.Open(cleanPath); err == nil {
		file.Close()
		http.FileServer(http.FS(s.uiFS)).ServeHTTP(w, r)
		return
	}

	index, err := fs.ReadFile(s.uiFS, "index.html")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(index)
}

func pathClean(path string) string {
	if path == "" {
		return "/"
	}
	cleaned := pathpkg.Clean("/" + strings.TrimPrefix(path, "/"))
	if cleaned == "." {
		return "/"
	}
	return cleaned
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func queryInt(raw string, fallback int) int {
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func queryBool(raw string, fallback bool) bool {
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return value
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
