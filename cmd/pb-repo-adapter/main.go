package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"primitivebox/internal/primitive"
)

var dispatchFn = dispatch

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      any             `json:"id"`
}

type response struct {
	JSONRPC string `json:"jsonrpc"`
	Result  any    `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
	ID any `json:"id"`
}

func main() {
	var workspace string
	var transport string
	var listenAddr string
	var socketPath string

	flag.StringVar(&workspace, "workspace", "/workspace", "Workspace directory")
	flag.StringVar(&transport, "transport", "http", "Adapter transport: http or unix")
	flag.StringVar(&listenAddr, "listen", "127.0.0.1:0", "Listen address for HTTP transport")
	flag.StringVar(&socketPath, "socket", "", "Socket path for unix transport")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/rpc", func(w http.ResponseWriter, r *http.Request) {
		handleRPC(workspace, w, r)
	})

	server := &http.Server{Handler: mux}
	reg := map[string]any{
		"adapter":   "repo",
		"version":   "0.1.0",
		"transport": transport,
	}

	switch transport {
	case "unix":
		if socketPath == "" {
			socketPath = filepath.Join(os.TempDir(), "pb-repo-adapter.sock")
		}
		_ = os.Remove(socketPath)
		listener, err := net.Listen("unix", socketPath)
		if err != nil {
			log.Fatalf("listen unix: %v", err)
		}
		reg["socket"] = socketPath
		printRegistration(reg)
		log.Fatal(server.Serve(listener))
	default:
		listener, err := net.Listen("tcp", listenAddr)
		if err != nil {
			log.Fatalf("listen http: %v", err)
		}
		reg["endpoint"] = "http://" + listener.Addr().String()
		printRegistration(reg)
		log.Fatal(server.Serve(listener))
	}
}

func printRegistration(reg map[string]any) {
	data, _ := json.Marshal(reg)
	fmt.Println(string(data))
}

func handleRPC(workspace string, w http.ResponseWriter, r *http.Request) {
	var req request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, nil, -32700, err.Error())
		return
	}

	result, err := dispatchFn(r.Context(), workspace, req.Method, req.Params)
	if err != nil {
		writeError(w, req.ID, -32603, err.Error())
		return
	}
	_ = json.NewEncoder(w).Encode(response{
		JSONRPC: "2.0",
		Result:  result,
		ID:      req.ID,
	})
}

func dispatch(ctx context.Context, workspace, method string, params json.RawMessage) (primitive.Result, error) {
	switch method {
	case "repo.search":
		return primitive.NewCodeSearch(workspace).Execute(ctx, remapSearchParams(params))
	case "repo.read_symbol":
		return readSymbol(ctx, workspace, params)
	case "repo.patch_symbol":
		return patchSymbol(ctx, workspace, params)
	case "repo.run_tests":
		return primitive.NewTestRun(workspace, primitive.Options{DefaultTimeout: 120, SandboxMode: true}).Execute(ctx, params)
	default:
		return primitive.Result{}, fmt.Errorf("unknown repo method: %s", method)
	}
}

func remapSearchParams(params json.RawMessage) json.RawMessage {
	if len(params) == 0 {
		return json.RawMessage(`{}`)
	}
	return params
}

type symbolParams struct {
	Path   string `json:"path"`
	Symbol string `json:"symbol"`
}

type patchSymbolParams struct {
	Path   string `json:"path"`
	Symbol string `json:"symbol"`
	Body   string `json:"body"`
}

func readSymbol(ctx context.Context, workspace string, params json.RawMessage) (primitive.Result, error) {
	var input symbolParams
	if err := json.Unmarshal(params, &input); err != nil {
		return primitive.Result{}, err
	}
	start, end, kind, err := locateSymbol(ctx, workspace, input.Path, input.Symbol)
	if err != nil {
		return primitive.Result{}, err
	}
	readParams, _ := json.Marshal(map[string]any{
		"path":       input.Path,
		"start_line": start,
		"end_line":   end,
	})
	readResult, err := primitive.NewFSRead(workspace).Execute(ctx, readParams)
	if err != nil {
		return primitive.Result{}, err
	}
	data := map[string]any{
		"path":       input.Path,
		"symbol":     input.Symbol,
		"kind":       kind,
		"start_line": start,
		"end_line":   end,
	}
	if payload, ok := readResult.Data.(primitive.Result); ok {
		data["content"] = payload
	}
	raw, _ := json.Marshal(readResult.Data)
	var readMap map[string]any
	_ = json.Unmarshal(raw, &readMap)
	data["content"] = readMap["content"]
	return primitive.Result{Data: data}, nil
}

func patchSymbol(ctx context.Context, workspace string, params json.RawMessage) (primitive.Result, error) {
	var input patchSymbolParams
	if err := json.Unmarshal(params, &input); err != nil {
		return primitive.Result{}, err
	}
	start, end, _, err := locateSymbol(ctx, workspace, input.Path, input.Symbol)
	if err != nil {
		return primitive.Result{}, err
	}
	readParams, _ := json.Marshal(map[string]any{"path": input.Path})
	readResult, err := primitive.NewFSRead(workspace).Execute(ctx, readParams)
	if err != nil {
		return primitive.Result{}, err
	}
	raw, _ := json.Marshal(readResult.Data)
	var fileData map[string]any
	_ = json.Unmarshal(raw, &fileData)
	content, _ := fileData["content"].(string)
	lines := strings.Split(content, "\n")
	if start < 1 || start > len(lines) {
		return primitive.Result{}, fmt.Errorf("symbol %s not found in %s", input.Symbol, input.Path)
	}
	if end <= 0 || end > len(lines) {
		end = len(lines)
	}

	replacement := strings.Split(strings.TrimSuffix(input.Body, "\n"), "\n")
	newLines := append([]string{}, lines[:start-1]...)
	newLines = append(newLines, replacement...)
	newLines = append(newLines, lines[end:]...)
	newContent := strings.Join(newLines, "\n")

	writeParams, _ := json.Marshal(map[string]any{
		"path":    input.Path,
		"content": newContent,
		"mode":    "overwrite",
	})
	writeResult, err := primitive.NewFSWrite(workspace).Execute(ctx, writeParams)
	if err != nil {
		return primitive.Result{}, err
	}
	rawWrite, _ := json.Marshal(writeResult.Data)
	var writeData map[string]any
	_ = json.Unmarshal(rawWrite, &writeData)
	return primitive.Result{
		Data: map[string]any{
			"path":          input.Path,
			"symbol":        input.Symbol,
			"bytes_written": writeData["bytes_written"],
			"diff":          writeData["diff"],
		},
		Diff:     writeResult.Diff,
		Duration: time.Now().UnixMilli(),
	}, nil
}

func locateSymbol(ctx context.Context, workspace, path, name string) (int, int, string, error) {
	params, _ := json.Marshal(map[string]any{"path": path})
	result, err := primitive.NewCodeSymbols(workspace).Execute(ctx, params)
	if err != nil {
		return 0, 0, "", err
	}
	raw, _ := json.Marshal(result.Data)
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return 0, 0, "", err
	}
	symbols, _ := payload["symbols"].([]any)
	for idx, item := range symbols {
		symbol, _ := item.(map[string]any)
		if symbol["name"] == name {
			start := int(symbol["start_line"].(float64))
			end := 0
			if idx+1 < len(symbols) {
				next, _ := symbols[idx+1].(map[string]any)
				end = int(next["start_line"].(float64)) - 1
			}
			kind, _ := symbol["kind"].(string)
			return start, end, kind, nil
		}
	}
	return 0, 0, "", fmt.Errorf("symbol %s not found in %s", name, path)
}

func writeError(w http.ResponseWriter, id any, code int, message string) {
	_ = json.NewEncoder(w).Encode(response{
		JSONRPC: "2.0",
		Error: &struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}{
			Code:    code,
			Message: message,
		},
		ID: id,
	})
}
