package eventing

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// Event captures a structured control-plane or primitive execution event.
type Event struct {
	ID        int64           `json:"id,omitempty"`
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Source    string          `json:"source,omitempty"`
	SandboxID string          `json:"sandbox_id,omitempty"`
	Method    string          `json:"method,omitempty"`
	Stream    string          `json:"stream,omitempty"`
	Message   string          `json:"message,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
	Sequence  int64           `json:"sequence,omitempty"`
}

// ListFilter narrows an event query for inspector and replay APIs.
type ListFilter struct {
	SandboxID string
	Method    string
	Type      string
	Limit     int
}

// Store persists events for later inspection.
type Store interface {
	Append(ctx context.Context, evt Event) (Event, error)
	ListEvents(ctx context.Context, filter ListFilter) ([]Event, error)
}

// Sink receives events emitted during primitive execution.
type Sink interface {
	Emit(ctx context.Context, evt Event)
}

// SinkFunc adapts a function into a Sink.
type SinkFunc func(context.Context, Event)

func (fn SinkFunc) Emit(ctx context.Context, evt Event) {
	fn(ctx, evt)
}

// MultiSink forwards the same event to multiple sinks.
type MultiSink struct {
	sinks []Sink
}

// NewMultiSink builds a sink that fans out to all non-nil sinks.
func NewMultiSink(sinks ...Sink) Sink {
	filtered := make([]Sink, 0, len(sinks))
	for _, sink := range sinks {
		if sink != nil {
			filtered = append(filtered, sink)
		}
	}
	switch len(filtered) {
	case 0:
		return nil
	case 1:
		return filtered[0]
	default:
		return MultiSink{sinks: filtered}
	}
}

func (m MultiSink) Emit(ctx context.Context, evt Event) {
	for _, sink := range m.sinks {
		sink.Emit(ctx, evt)
	}
}

// Bus persists and fan-outs events to subscribers.
type Bus struct {
	store Store

	mu          sync.RWMutex
	subscribers map[int]chan Event
	nextID      int
}

// NewBus creates an event bus backed by the given store.
func NewBus(store Store) *Bus {
	return &Bus{
		store:       store,
		subscribers: make(map[int]chan Event),
	}
}

// Publish records an event and broadcasts it to subscribers.
func (b *Bus) Publish(ctx context.Context, evt Event) {
	if b == nil {
		return
	}

	if evt.Timestamp == "" {
		evt.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}

	if b.store != nil {
		if persisted, err := b.store.Append(ctx, evt); err == nil {
			evt = persisted
		}
	}

	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.subscribers {
		select {
		case ch <- evt:
		default:
		}
	}
}

// Subscribe registers a buffered event stream.
func (b *Bus) Subscribe(buffer int) (<-chan Event, func()) {
	if b == nil {
		ch := make(chan Event)
		close(ch)
		return ch, func() {}
	}
	if buffer <= 0 {
		buffer = 32
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	id := b.nextID
	b.nextID++
	ch := make(chan Event, buffer)
	b.subscribers[id] = ch

	cancel := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if existing, ok := b.subscribers[id]; ok {
			delete(b.subscribers, id)
			close(existing)
		}
	}

	return ch, cancel
}

type sinkContextKey struct{}

// WithSink attaches an event sink to the context.
func WithSink(ctx context.Context, sink Sink) context.Context {
	if sink == nil {
		return ctx
	}
	return context.WithValue(ctx, sinkContextKey{}, sink)
}

// SinkFromContext retrieves the current event sink, if any.
func SinkFromContext(ctx context.Context) Sink {
	if ctx == nil {
		return nil
	}
	sink, _ := ctx.Value(sinkContextKey{}).(Sink)
	return sink
}

// Emit publishes an event through the current context sink.
func Emit(ctx context.Context, evt Event) {
	if sink := SinkFromContext(ctx); sink != nil {
		sink.Emit(ctx, evt)
	}
}

// MustJSON marshals arbitrary data for event payloads.
func MustJSON(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return data
}
