import type { UIEvent, UIEventType } from '@/types/workspace'

type Listener = (event: UIEvent) => void

class UIEventBus {
  private listeners: Map<string, Set<Listener>> = new Map()
  private seq = 0

  emit(type: UIEventType, payload: Record<string, unknown>): UIEvent {
    const event: UIEvent = {
      id: `uiev-${++this.seq}`,
      timestamp: new Date().toISOString(),
      type,
      payload,
    }

    // Notify type-specific listeners
    this.listeners.get(type)?.forEach((fn) => fn(event))
    // Notify wildcard listeners
    this.listeners.get('*')?.forEach((fn) => fn(event))

    return event
  }

  on(type: UIEventType | '*', listener: Listener): () => void {
    if (!this.listeners.has(type)) {
      this.listeners.set(type, new Set())
    }
    this.listeners.get(type)!.add(listener)
    return () => this.listeners.get(type)?.delete(listener)
  }
}

// Singleton — in-process only, not wired to backend SSE
export const uiEventBus = new UIEventBus()
