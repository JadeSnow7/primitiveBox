package astdiff

import (
	"strings"
	"testing"
)

func TestASTDiff_SignatureChange(t *testing.T) {
	t.Parallel()

	before := []byte("package sample\n\nfunc Foo(x int) error { return nil }\n")
	after := []byte("package sample\n\nimport \"context\"\n\nfunc Foo(ctx context.Context, x int) error { return nil }\n")

	changes, err := Diff(before, after)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %+v", changes)
	}
	if changes[0].Kind != "func_signature" {
		t.Fatalf("expected func_signature, got %+v", changes[0])
	}
	if !strings.Contains(changes[0].Symbol, "Foo") {
		t.Fatalf("expected Foo in symbol, got %q", changes[0].Symbol)
	}
}

func TestASTDiff_NoChange(t *testing.T) {
	t.Parallel()

	content := []byte("package sample\n\nfunc Foo() {}\n")
	changes, err := Diff(content, content)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if changes == nil {
		t.Fatalf("expected non-nil slice")
	}
	if len(changes) != 0 {
		t.Fatalf("expected no changes, got %+v", changes)
	}
}

func TestASTDiff_TypeAdded(t *testing.T) {
	t.Parallel()

	before := []byte("package sample\n")
	after := []byte("package sample\n\ntype Bar struct{}\n")

	changes, err := Diff(before, after)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %+v", changes)
	}
	if changes[0].Kind != "type_added" {
		t.Fatalf("expected type_added, got %+v", changes[0])
	}
}

func TestASTDiff_ParseError(t *testing.T) {
	t.Parallel()

	_, err := Diff([]byte("this is not valid go"), []byte("package sample\n"))
	if err == nil {
		t.Fatalf("expected parse error")
	}
}
