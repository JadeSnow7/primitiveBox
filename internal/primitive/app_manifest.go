package primitive

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"primitivebox/internal/cvr"
)

type AppPrimitiveManifest struct {
	AppID            string              `json:"app_id"`
	Name             string              `json:"name"`
	Description      string              `json:"description"`
	InputSchema      json.RawMessage     `json:"input_schema"`
	OutputSchema     json.RawMessage     `json:"output_schema"`
	SocketPath       string              `json:"socket_path"`
	VerifyEndpoint   string              `json:"verify_endpoint,omitempty"`
	RollbackEndpoint string              `json:"rollback_endpoint,omitempty"`
	Intent           cvr.PrimitiveIntent `json:"intent"`
}

type AppPrimitiveRegistry interface {
	Register(ctx context.Context, manifest AppPrimitiveManifest) error
	Unregister(ctx context.Context, appID, name string) error
	Get(ctx context.Context, name string) (*AppPrimitiveManifest, error)
	List(ctx context.Context) ([]AppPrimitiveManifest, error)
}

type inMemoryAppRegistry struct {
	mu        sync.RWMutex
	manifests map[string]AppPrimitiveManifest
}

func NewInMemoryAppRegistry() AppPrimitiveRegistry {
	return &inMemoryAppRegistry{
		manifests: make(map[string]AppPrimitiveManifest),
	}
}

func (r *inMemoryAppRegistry) Register(ctx context.Context, manifest AppPrimitiveManifest) error {
	_ = ctx

	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, exists := r.manifests[manifest.Name]; exists && existing.AppID != manifest.AppID {
		return fmt.Errorf(
			"app_primitive_conflict: %q is already registered by app %q",
			manifest.Name,
			existing.AppID,
		)
	}

	r.manifests[manifest.Name] = cloneAppPrimitiveManifest(manifest)
	return nil
}

func (r *inMemoryAppRegistry) Unregister(ctx context.Context, appID, name string) error {
	_ = ctx
	_ = appID

	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.manifests, name)
	return nil
}

func (r *inMemoryAppRegistry) Get(ctx context.Context, name string) (*AppPrimitiveManifest, error) {
	_ = ctx

	r.mu.RLock()
	defer r.mu.RUnlock()

	manifest, ok := r.manifests[name]
	if !ok {
		return nil, nil
	}
	cloned := cloneAppPrimitiveManifest(manifest)
	return &cloned, nil
}

func (r *inMemoryAppRegistry) List(ctx context.Context) ([]AppPrimitiveManifest, error) {
	_ = ctx

	r.mu.RLock()
	defer r.mu.RUnlock()

	items := make([]AppPrimitiveManifest, 0, len(r.manifests))
	for _, manifest := range r.manifests {
		items = append(items, cloneAppPrimitiveManifest(manifest))
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Name < items[j].Name
	})
	return items, nil
}

func cloneAppPrimitiveManifest(manifest AppPrimitiveManifest) AppPrimitiveManifest {
	manifest.InputSchema = cloneJSON(manifest.InputSchema)
	manifest.OutputSchema = cloneJSON(manifest.OutputSchema)
	manifest.Intent.AffectedScopes = append([]string(nil), manifest.Intent.AffectedScopes...)
	return manifest
}
