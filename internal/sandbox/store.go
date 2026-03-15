package sandbox

import (
	"context"
	"sort"
	"sync"
	"time"
)

// MemoryStore is an in-memory sandbox metadata store used by tests and fallback setups.
type MemoryStore struct {
	mu        sync.RWMutex
	sandboxes map[string]*Sandbox
}

// NewMemoryStore creates an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		sandboxes: make(map[string]*Sandbox),
	}
}

func (s *MemoryStore) Upsert(ctx context.Context, sb *Sandbox) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sandboxes[sb.ID] = cloneSandbox(sb)
	return nil
}

func (s *MemoryStore) Get(ctx context.Context, sandboxID string) (*Sandbox, bool, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()
	sb, ok := s.sandboxes[sandboxID]
	if !ok {
		return nil, false, nil
	}
	return cloneSandbox(sb), true, nil
}

func (s *MemoryStore) List(ctx context.Context) ([]*Sandbox, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]*Sandbox, 0, len(s.sandboxes))
	for _, sb := range s.sandboxes {
		out = append(out, cloneSandbox(sb))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (s *MemoryStore) Delete(ctx context.Context, sandboxID string) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sandboxes, sandboxID)
	return nil
}

func (s *MemoryStore) ListExpired(ctx context.Context, before time.Time, limit int) ([]*Sandbox, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()

	var out []*Sandbox
	for _, sb := range s.sandboxes {
		if sb.ExpiresAt == 0 || time.Unix(sb.ExpiresAt, 0).After(before) {
			continue
		}
		if sb.Status == StatusDestroyed {
			continue
		}
		out = append(out, cloneSandbox(sb))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ExpiresAt < out[j].ExpiresAt
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
