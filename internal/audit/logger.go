// Package audit provides operation logging for all primitive calls.
package audit

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// --------------------------------------------------------------------------
// Audit Logger
// --------------------------------------------------------------------------

// Logger records all primitive calls for compliance and debugging.
type Logger struct {
	mu      sync.Mutex
	entries []Entry
	logFile *os.File
}

// Entry represents a single audit log entry.
type Entry struct {
	Timestamp string            `json:"timestamp"`
	Method    string            `json:"method"`
	Input     json.RawMessage   `json:"input"`
	Output    any               `json:"output,omitempty"`
	Error     string            `json:"error,omitempty"`
	Duration  string            `json:"duration"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// NewLogger creates a new audit logger. If logDir is non-empty, logs are
// also written to a JSONL file for persistent audit trail.
func NewLogger(logDir string) (*Logger, error) {
	l := &Logger{}

	if logDir != "" {
		if err := os.MkdirAll(logDir, 0o755); err != nil {
			return nil, fmt.Errorf("cannot create audit log dir: %w", err)
		}
		logPath := filepath.Join(logDir, fmt.Sprintf("audit-%s.jsonl", time.Now().Format("20060102-150405")))
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return nil, fmt.Errorf("cannot create audit log file: %w", err)
		}
		l.logFile = f
		log.Printf("[Audit] Logging to %s", logPath)
	}

	return l, nil
}

// LogCall records a primitive invocation.
func (l *Logger) LogCall(method string, input json.RawMessage, output any, err error, duration time.Duration) {
	l.LogCallWithMetadata(method, input, output, err, duration, nil)
}

// LogCallWithMetadata records a primitive invocation with optional metadata.
func (l *Logger) LogCallWithMetadata(method string, input json.RawMessage, output any, err error, duration time.Duration, metadata map[string]string) {
	entry := Entry{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Method:    method,
		Input:     input,
		Output:    output,
		Duration:  duration.String(),
		Metadata:  metadata,
	}
	if err != nil {
		entry.Error = err.Error()
	}

	l.mu.Lock()
	l.entries = append(l.entries, entry)

	// Write to file if available
	if l.logFile != nil {
		data, _ := json.Marshal(entry)
		l.logFile.Write(data)
		l.logFile.Write([]byte("\n"))
	}
	l.mu.Unlock()
}

// Entries returns all logged entries.
func (l *Logger) Entries() []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	result := make([]Entry, len(l.entries))
	copy(result, l.entries)
	return result
}

// Close closes the log file.
func (l *Logger) Close() error {
	if l.logFile != nil {
		return l.logFile.Close()
	}
	return nil
}
