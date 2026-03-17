package primitive

import (
	"context"
	"strings"
	"testing"
)

func TestAppRegistry_DuplicateRejected(t *testing.T) {
	t.Parallel()

	registry := NewInMemoryAppRegistry()
	first := AppPrimitiveManifest{
		AppID:      "app-one",
		Name:       "myapp.greet",
		SocketPath: "/tmp/app-one.sock",
	}
	second := AppPrimitiveManifest{
		AppID:      "app-two",
		Name:       "myapp.greet",
		SocketPath: "/tmp/app-two.sock",
	}

	if err := registry.Register(context.Background(), first); err != nil {
		t.Fatalf("first register: %v", err)
	}
	err := registry.Register(context.Background(), second)
	if err == nil {
		t.Fatal("expected duplicate registration error")
	}
	if !strings.Contains(err.Error(), first.Name) {
		t.Fatalf("expected error to contain primitive name %q, got %v", first.Name, err)
	}
	if !strings.Contains(err.Error(), first.AppID) {
		t.Fatalf("expected error to contain app id %q, got %v", first.AppID, err)
	}
}

func TestAppRegistry_SameAppIDReregistrationAllowed(t *testing.T) {
	t.Parallel()

	registry := NewInMemoryAppRegistry()
	first := AppPrimitiveManifest{
		AppID:      "app-one",
		Name:       "myapp.greet",
		SocketPath: "/tmp/app-one.sock",
	}
	updated := AppPrimitiveManifest{
		AppID:      "app-one",
		Name:       "myapp.greet",
		SocketPath: "/tmp/app-one-v2.sock",
	}

	if err := registry.Register(context.Background(), first); err != nil {
		t.Fatalf("initial register: %v", err)
	}
	if err := registry.Register(context.Background(), updated); err != nil {
		t.Fatalf("same-app re-register: %v", err)
	}
	got, err := registry.Get(context.Background(), "myapp.greet")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil || got.SocketPath != updated.SocketPath {
		t.Fatalf("expected updated SocketPath %q, got %v", updated.SocketPath, got)
	}
}

func TestAppRegistry_UnregisterThenReregister(t *testing.T) {
	t.Parallel()

	registry := NewInMemoryAppRegistry()
	manifest := AppPrimitiveManifest{
		AppID:      "app-one",
		Name:       "myapp.greet",
		SocketPath: "/tmp/app-one.sock",
	}
	replacement := AppPrimitiveManifest{
		AppID:      "app-two",
		Name:       "myapp.greet",
		SocketPath: "/tmp/app-two.sock",
	}

	if err := registry.Register(context.Background(), manifest); err != nil {
		t.Fatalf("initial register: %v", err)
	}
	if err := registry.Unregister(context.Background(), manifest.AppID, manifest.Name); err != nil {
		t.Fatalf("unregister: %v", err)
	}
	if err := registry.Register(context.Background(), replacement); err != nil {
		t.Fatalf("reregister: %v", err)
	}
}
