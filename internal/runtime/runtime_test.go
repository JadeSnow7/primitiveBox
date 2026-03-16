package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"primitivebox/internal/primitive"
)

func TestLoadManifestsIncludesEmbeddedRepo(t *testing.T) {
	t.Parallel()

	manifests, err := loadManifests(t.TempDir())
	if err != nil {
		t.Fatalf("load manifests: %v", err)
	}
	found := false
	for _, manifest := range manifests {
		if manifest.Name == "repo" {
			found = true
			if len(manifest.Primitives) == 0 {
				t.Fatalf("expected repo manifest primitives")
			}
		}
	}
	if !found {
		t.Fatalf("expected embedded repo manifest")
	}
}

func TestSerialExecutorPreservesSubmissionOrder(t *testing.T) {
	t.Parallel()

	exec := NewSerialExecutor()
	defer exec.Close()

	order := make(chan int, 2)
	started := make(chan struct{})

	go func() {
		_, _ = exec.Do(context.Background(), func() (primitive.Result, error) {
			close(started)
			time.Sleep(100 * time.Millisecond)
			order <- 1
			return primitive.Result{}, nil
		})
	}()

	<-started
	go func() {
		_, _ = exec.Do(context.Background(), func() (primitive.Result, error) {
			order <- 2
			return primitive.Result{}, nil
		})
	}()

	first := <-order
	second := <-order
	if first != 1 || second != 2 {
		t.Fatalf("expected FIFO order 1,2 got %d,%d", first, second)
	}
}

func TestRuntimeRestoresWorkspaceAfterSideEffectTimeout(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	target := filepath.Join(workspace, "main.txt")
	if err := os.WriteFile(target, []byte("before\n"), 0o644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}

	rt := &Runtime{
		config:       Config{WorkspaceDir: workspace, SandboxID: "sb-timeout"},
		raw:          make(map[string]primitive.Primitive),
		registry:     primitive.NewRegistry(),
		executor:     NewSerialExecutor(),
		traceWriter:  NewTraceWriter(filepath.Join(workspace, ".logs")),
		checkpointer: primitive.NewStateCheckpoint(workspace),
		restorer:     primitive.NewStateRestore(workspace),
		schemas:      make(map[string]primitive.Schema),
	}
	defer rt.executor.Close()

	rt.raw["repo.patch_symbol"] = timeoutWritePrimitive{path: target}
	rt.schemas["repo.patch_symbol"] = primitive.EnrichSchema(primitive.Schema{
		Name:               "repo.patch_symbol",
		Namespace:          "repo",
		SideEffect:         primitive.SideEffectWrite,
		CheckpointRequired: true,
		TimeoutMs:          50,
		Source:             primitive.SourceApp,
		Adapter:            "repo",
	})

	_, err := rt.Execute(context.Background(), "repo.patch_symbol", json.RawMessage(`{}`))
	if err == nil {
		t.Fatalf("expected timeout error")
	}

	content, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatalf("read restored file: %v", readErr)
	}
	if string(content) != "before\n" {
		t.Fatalf("expected workspace restore after timeout, got %q", string(content))
	}
}

type timeoutWritePrimitive struct {
	path string
}

func (t timeoutWritePrimitive) Name() string     { return "repo.patch_symbol" }
func (t timeoutWritePrimitive) Category() string { return "repo" }
func (t timeoutWritePrimitive) Schema() primitive.Schema {
	return primitive.Schema{Name: "repo.patch_symbol"}
}
func (t timeoutWritePrimitive) Execute(ctx context.Context, params json.RawMessage) (primitive.Result, error) {
	if err := os.WriteFile(t.path, []byte("after-timeout\n"), 0o644); err != nil {
		return primitive.Result{}, err
	}
	<-ctx.Done()
	return primitive.Result{}, ctx.Err()
}
