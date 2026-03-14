// Package rpc implements a JSON-RPC 2.0 server over HTTP.
package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"primitivebox/internal/audit"
	"primitivebox/internal/primitive"
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
	registry *primitive.Registry
	auditor  *audit.Logger
	manager  *sandbox.Manager

	listener   net.Listener
	server     *http.Server
	httpClient *http.Client
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

// ListenAndServe starts the HTTP server on the given address.
func (s *Server) ListenAndServe(addr string) error {
	s.server = &http.Server{
		Handler:      s.Handler(),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
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
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/primitives", s.handleListPrimitives)
	mux.HandleFunc("/sandboxes", s.handleSandboxes)
	mux.HandleFunc("/sandboxes/", s.handleSandboxRoute)
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
	var req Request
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Printf("[RPC] Recovered from panic in %s: %v", req.Method, recovered)
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
		s.writeResponse(w, Response{
			JSONRPC: "2.0",
			Error:   &Error{Code: CodeParseError, Message: "invalid JSON: " + err.Error()},
			ID:      nil,
		})
		return
	}

	if req.JSONRPC != "2.0" {
		s.writeResponse(w, Response{
			JSONRPC: "2.0",
			Error:   &Error{Code: CodeInvalidRequest, Message: "jsonrpc must be '2.0'"},
			ID:      req.ID,
		})
		return
	}

	prim, ok := s.registry.Get(req.Method)
	if !ok {
		s.writeResponse(w, Response{
			JSONRPC: "2.0",
			Error:   &Error{Code: CodeMethodNotFound, Message: "unknown primitive: " + req.Method},
			ID:      req.ID,
		})
		return
	}

	start := time.Now()
	result, err := prim.Execute(r.Context(), req.Params)
	duration := time.Since(start)

	if s.auditor != nil {
		s.auditor.LogCall(req.Method, req.Params, result.Data, err, duration)
	}

	if err != nil {
		s.writePrimitiveError(w, req.ID, err)
		return
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
	if err != nil {
		http.Error(w, fmt.Sprintf("sandbox proxy request failed: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
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

func (s *Server) writeResponse(w http.ResponseWriter, resp Response) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("[RPC] Failed to encode response: %v", err)
	}
}
