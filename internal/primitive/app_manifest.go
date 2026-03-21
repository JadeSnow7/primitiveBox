package primitive

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"primitivebox/internal/cvr"
)

type AppPrimitiveManifest struct {
	AppID            string                   `json:"app_id"`
	Name             string                   `json:"name"`
	Description      string                   `json:"description"`
	InputSchema      json.RawMessage          `json:"input_schema"`
	OutputSchema     json.RawMessage          `json:"output_schema"`
	SocketPath       string                   `json:"socket_path"`
	Availability     AppPrimitiveAvailability `json:"status,omitempty"`
	VerifyEndpoint   string                   `json:"verify_endpoint,omitempty"`
	Verify           *AppPrimitiveVerify      `json:"verify,omitempty"`
	RollbackEndpoint string                   `json:"rollback_endpoint,omitempty"`
	Rollback         *AppPrimitiveRollback    `json:"rollback,omitempty"`
	Intent           cvr.PrimitiveIntent      `json:"intent"`
}

type AppPrimitiveAvailability string

const (
	AppPrimitiveActive       AppPrimitiveAvailability = "active"
	AppPrimitiveUnavailable  AppPrimitiveAvailability = "unavailable"
	AppPrimitiveReactivating AppPrimitiveAvailability = "reactivating"
)

type AppPrimitiveVerify struct {
	Strategy  string `json:"strategy"`
	Primitive string `json:"primitive,omitempty"`
	Command   string `json:"command,omitempty"`
}

type AppPrimitiveRollback struct {
	Strategy  string `json:"strategy"`
	Primitive string `json:"primitive,omitempty"`
}

type AppPrimitiveRegistry interface {
	Register(ctx context.Context, manifest AppPrimitiveManifest) error
	Unregister(ctx context.Context, appID, name string) error
	Get(ctx context.Context, name string) (*AppPrimitiveManifest, error)
	List(ctx context.Context) ([]AppPrimitiveManifest, error)
	MarkUnavailable(ctx context.Context, appID string) error
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

	normalized, err := NormalizeAppPrimitiveManifest(manifest)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	reactivating := false
	for _, existing := range r.manifests {
		if existing.AppID == normalized.AppID && existing.Availability == AppPrimitiveUnavailable {
			reactivating = true
			break
		}
	}
	if existing, exists := r.manifests[normalized.Name]; exists && existing.AppID != normalized.AppID {
		return fmt.Errorf(
			"app_primitive_conflict: %q is already registered by app %q",
			normalized.Name,
			existing.AppID,
		)
	}

	for name, existing := range r.manifests {
		if existing.AppID != normalized.AppID {
			continue
		}
		existing.Availability = AppPrimitiveActive
		r.manifests[name] = existing
	}
	if reactivating {
		normalized.Availability = AppPrimitiveActive
	}
	r.manifests[normalized.Name] = cloneAppPrimitiveManifest(normalized)
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

func (r *inMemoryAppRegistry) MarkUnavailable(ctx context.Context, appID string) error {
	_ = ctx

	r.mu.Lock()
	defer r.mu.Unlock()

	for name, manifest := range r.manifests {
		if manifest.AppID != appID {
			continue
		}
		manifest.Availability = AppPrimitiveUnavailable
		r.manifests[name] = manifest
	}
	return nil
}

func cloneAppPrimitiveManifest(manifest AppPrimitiveManifest) AppPrimitiveManifest {
	manifest.InputSchema = cloneJSON(manifest.InputSchema)
	manifest.OutputSchema = cloneJSON(manifest.OutputSchema)
	manifest.Intent.AffectedScopes = append([]string(nil), manifest.Intent.AffectedScopes...)
	if manifest.Verify != nil {
		verifyCopy := *manifest.Verify
		manifest.Verify = &verifyCopy
	}
	if manifest.Rollback != nil {
		rollbackCopy := *manifest.Rollback
		manifest.Rollback = &rollbackCopy
	}
	return manifest
}

var reservedSystemPrimitiveNamespaces = [...]string{
	"fs.",
	"state.",
	"shell.",
	"verify.",
	"macro.",
	"code.",
	"test.",
}

func NormalizeAppPrimitiveManifest(manifest AppPrimitiveManifest) (AppPrimitiveManifest, error) {
	normalized := cloneAppPrimitiveManifest(manifest)
	normalized.Availability = normalizeAppPrimitiveAvailability(normalized.Availability)

	normalized.VerifyEndpoint = strings.TrimSpace(normalized.VerifyEndpoint)
	normalized.RollbackEndpoint = strings.TrimSpace(normalized.RollbackEndpoint)

	if err := validateAppPrimitiveName(normalized.Name); err != nil {
		return AppPrimitiveManifest{}, err
	}

	inputSchema, err := NormalizeAppManifestSchema(normalized.InputSchema)
	if err != nil {
		return AppPrimitiveManifest{}, fmt.Errorf("invalid input_schema: %w", err)
	}
	outputSchema, err := NormalizeAppManifestSchema(normalized.OutputSchema)
	if err != nil {
		return AppPrimitiveManifest{}, fmt.Errorf("invalid output_schema: %w", err)
	}

	normalized.InputSchema = inputSchema
	normalized.OutputSchema = outputSchema
	verify, err := normalizeAppPrimitiveVerify(normalized.VerifyEndpoint, normalized.Verify)
	if err != nil {
		return AppPrimitiveManifest{}, fmt.Errorf("invalid verify declaration: %w", err)
	}
	normalized.Verify = verify
	if normalized.Verify != nil && normalized.Verify.Strategy == "primitive" {
		normalized.VerifyEndpoint = normalized.Verify.Primitive
	}
	rollback, err := normalizeAppPrimitiveRollback(normalized.RollbackEndpoint, normalized.Rollback)
	if err != nil {
		return AppPrimitiveManifest{}, fmt.Errorf("invalid rollback declaration: %w", err)
	}
	normalized.Rollback = rollback
	if normalized.Rollback != nil && normalized.Rollback.Strategy == "primitive" {
		normalized.RollbackEndpoint = normalized.Rollback.Primitive
	}
	return normalized, nil
}

func normalizeAppPrimitiveAvailability(availability AppPrimitiveAvailability) AppPrimitiveAvailability {
	switch availability {
	case "", AppPrimitiveActive, AppPrimitiveUnavailable, AppPrimitiveReactivating:
		if availability == "" {
			return AppPrimitiveActive
		}
		return availability
	default:
		return AppPrimitiveActive
	}
}

func normalizeAppPrimitiveVerify(legacyEndpoint string, verify *AppPrimitiveVerify) (*AppPrimitiveVerify, error) {
	legacyEndpoint = strings.TrimSpace(legacyEndpoint)
	if verify == nil {
		if legacyEndpoint == "" {
			return nil, nil
		}
		return &AppPrimitiveVerify{
			Strategy:  "primitive",
			Primitive: legacyEndpoint,
		}, nil
	}

	normalized := &AppPrimitiveVerify{
		Strategy:  strings.TrimSpace(verify.Strategy),
		Primitive: strings.TrimSpace(verify.Primitive),
		Command:   strings.TrimSpace(verify.Command),
	}

	if legacyEndpoint != "" {
		switch normalized.Strategy {
		case "", "primitive":
			if normalized.Primitive == "" {
				normalized.Primitive = legacyEndpoint
			} else if normalized.Primitive != legacyEndpoint {
				return nil, fmt.Errorf("verify_endpoint %q conflicts with verify.primitive %q", legacyEndpoint, normalized.Primitive)
			}
			if normalized.Strategy == "" {
				normalized.Strategy = "primitive"
			}
		default:
			return nil, fmt.Errorf("verify_endpoint cannot be combined with verify.strategy=%q", normalized.Strategy)
		}
	}

	switch normalized.Strategy {
	case "":
		return nil, fmt.Errorf("verify.strategy is required when verify is provided")
	case "primitive":
		if normalized.Primitive == "" {
			return nil, fmt.Errorf(`verify.strategy "primitive" requires verify.primitive`)
		}
		if normalized.Command != "" {
			return nil, fmt.Errorf(`verify.strategy "primitive" cannot include verify.command`)
		}
	case "command":
		if normalized.Command == "" {
			return nil, fmt.Errorf(`verify.strategy "command" requires verify.command`)
		}
		if normalized.Primitive != "" {
			return nil, fmt.Errorf(`verify.strategy "command" cannot include verify.primitive`)
		}
	case "none":
		if normalized.Primitive != "" || normalized.Command != "" {
			return nil, fmt.Errorf(`verify.strategy "none" cannot include verify.primitive or verify.command`)
		}
	default:
		return nil, fmt.Errorf("unsupported verify.strategy %q", normalized.Strategy)
	}

	return normalized, nil
}

func normalizeAppPrimitiveRollback(legacyEndpoint string, rollback *AppPrimitiveRollback) (*AppPrimitiveRollback, error) {
	legacyEndpoint = strings.TrimSpace(legacyEndpoint)
	if rollback == nil {
		if legacyEndpoint == "" {
			return nil, nil
		}
		return &AppPrimitiveRollback{
			Strategy:  "primitive",
			Primitive: legacyEndpoint,
		}, nil
	}

	normalized := &AppPrimitiveRollback{
		Strategy:  strings.TrimSpace(rollback.Strategy),
		Primitive: strings.TrimSpace(rollback.Primitive),
	}

	if legacyEndpoint != "" {
		switch normalized.Strategy {
		case "", "primitive":
			if normalized.Primitive == "" {
				normalized.Primitive = legacyEndpoint
			} else if normalized.Primitive != legacyEndpoint {
				return nil, fmt.Errorf("rollback_endpoint %q conflicts with rollback.primitive %q", legacyEndpoint, normalized.Primitive)
			}
			if normalized.Strategy == "" {
				normalized.Strategy = "primitive"
			}
		default:
			return nil, fmt.Errorf("rollback_endpoint cannot be combined with rollback.strategy=%q", normalized.Strategy)
		}
	}

	switch normalized.Strategy {
	case "":
		return nil, fmt.Errorf("rollback.strategy is required when rollback is provided")
	case "primitive":
		if normalized.Primitive == "" {
			return nil, fmt.Errorf(`rollback.strategy "primitive" requires rollback.primitive`)
		}
	case "none":
		if normalized.Primitive != "" {
			return nil, fmt.Errorf(`rollback.strategy "none" cannot include rollback.primitive`)
		}
	default:
		return nil, fmt.Errorf("unsupported rollback.strategy %q", normalized.Strategy)
	}

	return normalized, nil
}

func NormalizeAppManifestSchema(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := cloneJSON(raw)
	trimmed = json.RawMessage(strings.TrimSpace(string(trimmed)))
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return json.RawMessage(`{}`), nil
	}
	if trimmed[0] == '"' {
		var encoded string
		if err := json.Unmarshal(trimmed, &encoded); err != nil {
			return nil, err
		}
		trimmed = json.RawMessage(strings.TrimSpace(encoded))
	}
	if len(trimmed) == 0 {
		return json.RawMessage(`{}`), nil
	}

	var doc any
	if err := json.Unmarshal(trimmed, &doc); err != nil {
		return nil, fmt.Errorf("must be valid JSON")
	}

	obj, ok := doc.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("must be a JSON object")
	}
	if err := validateManifestSchemaObject(obj); err != nil {
		return nil, err
	}

	normalized, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(normalized), nil
}

func validateAppPrimitiveName(name string) error {
	for _, prefix := range reservedSystemPrimitiveNamespaces {
		if strings.HasPrefix(name, prefix) {
			return fmt.Errorf("app_primitive_reserved_namespace: %q uses reserved system namespace %q", name, prefix)
		}
	}
	return nil
}

func validateManifestSchemaObject(schema map[string]any) error {
	if rawType, ok := schema["type"]; ok {
		typeName, ok := rawType.(string)
		if !ok || typeName == "" {
			return fmt.Errorf(`"type" must be a non-empty string when present`)
		}
		if typeName != "object" {
			return fmt.Errorf(`top-level "type" must be "object"`)
		}
	}

	if rawProperties, ok := schema["properties"]; ok {
		if _, ok := rawProperties.(map[string]any); !ok {
			return fmt.Errorf(`"properties" must be an object`)
		}
	}

	if rawRequired, ok := schema["required"]; ok {
		items, ok := rawRequired.([]any)
		if !ok {
			return fmt.Errorf(`"required" must be an array of strings`)
		}
		for _, item := range items {
			value, ok := item.(string)
			if !ok || value == "" {
				return fmt.Errorf(`"required" must be an array of strings`)
			}
		}
	}

	return nil
}
