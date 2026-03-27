// Package primitive provides the primitive registry for dynamic registration.
package primitive

import (
	"fmt"
	"sync"
)

// --------------------------------------------------------------------------
// Registry: Pluggable Primitive Registration
// --------------------------------------------------------------------------

// Registry manages the registration and lookup of primitives.
// It supports dynamic registration for future plugin/marketplace use.
type Registry struct {
	mu         sync.RWMutex
	primitives map[string]Primitive
}

// NewRegistry creates an empty primitive registry.
func NewRegistry() *Registry {
	return &Registry{
		primitives: make(map[string]Primitive),
	}
}

// Register adds a primitive to the registry.
// Returns an error if a primitive with the same name already exists.
func (r *Registry) Register(p Primitive) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := p.Name()
	if _, exists := r.primitives[name]; exists {
		return fmt.Errorf("primitive already registered: %s", name)
	}

	r.primitives[name] = p
	return nil
}

// MustRegister registers a primitive and panics on failure.
func (r *Registry) MustRegister(p Primitive) {
	if err := r.Register(p); err != nil {
		panic(err)
	}
}

// Get retrieves a primitive by its fully qualified name.
func (r *Registry) Get(name string) (Primitive, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.primitives[name]
	return p, ok
}

// List returns all registered primitive names.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.primitives))
	for name := range r.primitives {
		names = append(names, name)
	}
	return names
}

// Registered returns all registered primitive implementations.
func (r *Registry) Registered() []Primitive {
	r.mu.RLock()
	defer r.mu.RUnlock()

	items := make([]Primitive, 0, len(r.primitives))
	for _, p := range r.primitives {
		items = append(items, p)
	}
	return items
}

// Schemas returns all registered primitive schemas.
func (r *Registry) Schemas() []Schema {
	r.mu.RLock()
	defer r.mu.RUnlock()

	schemas := make([]Schema, 0, len(r.primitives))
	for _, p := range r.primitives {
		schemas = append(schemas, EnrichSchema(p.Schema()))
	}
	return schemas
}

// Schema returns the enriched schema for one registered primitive.
func (r *Registry) Schema(name string) (Schema, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.primitives[name]
	if !ok {
		return Schema{}, false
	}
	return EnrichSchema(p.Schema()), true
}

// RegisterDefaults registers all built-in primitives.
func (r *Registry) RegisterDefaults(workspaceDir string, options Options) {
	// File system primitives
	r.MustRegister(NewFSRead(workspaceDir))
	r.MustRegister(NewFSWrite(workspaceDir))
	r.MustRegister(NewFSList(workspaceDir))

	// Shell primitive
	r.MustRegister(NewShellExec(workspaceDir, options))

	// State primitives
	r.MustRegister(NewStateCheckpoint(workspaceDir))
	r.MustRegister(NewStateRestore(workspaceDir))
	r.MustRegister(NewStateList(workspaceDir))

	// Code primitives
	r.MustRegister(NewCodeSearch(workspaceDir))
	r.MustRegister(NewCodeSymbols(workspaceDir))

	// Verify primitives
	r.MustRegister(NewVerifyTest(workspaceDir, options))
	r.MustRegister(NewTestRun(workspaceDir, options))
	r.MustRegister(NewVerifyCommand(workspaceDir, options))

	// Macro / compound primitives
	r.MustRegister(NewMacroSafeEdit(workspaceDir, options))

	// FS extended primitives
	r.MustRegister(NewFSDiff(workspaceDir))
}

// RegisterSandboxExtras registers sandbox-only primitives that must not be
// exposed in host workspace mode.
func (r *Registry) RegisterSandboxExtras(workspaceDir string, options Options) {
	r.MustRegister(NewDBSchema(workspaceDir))
	r.MustRegister(NewDBQuery(workspaceDir))
	r.MustRegister(NewDBExecute(workspaceDir))
	r.MustRegister(NewDBQueryReadonly(workspaceDir))

	manager := NewBrowserSessionManager(options)
	r.MustRegister(NewBrowserGoto(workspaceDir, manager, options))
	r.MustRegister(NewBrowserRead(workspaceDir, manager, options))
	r.MustRegister(NewBrowserExtract(workspaceDir, manager, options))
	r.MustRegister(NewBrowserClick(workspaceDir, manager, options))
	r.MustRegister(NewBrowserScreenshot(workspaceDir, manager, options))
}
