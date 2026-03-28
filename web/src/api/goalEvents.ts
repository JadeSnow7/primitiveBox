/**
 * SSE connection to /api/v1/events/stream filtered for goal.* events.
 *
 * The global event stream publishes all eventing.Bus events. Each SSE frame
 * uses a named event matching the event type (e.g. "goal.status_changed") and
 * a JSON-encoded eventing.Event as data.
 */

export interface GoalSSEEvent {
  type: string
  /** sourceID passed to publishGoalEvent — goal ID for goal-level events */
  message: string
  data: Record<string, unknown> | null
}

/**
 * Opens an EventSource on /api/v1/events/stream, filters for goal.* events,
 * and calls onEvent for each one.
 *
 * Returns a cleanup function that closes the connection.
 */
export function createGoalEventStream(
  onEvent: (event: GoalSSEEvent) => void,
): () => void {
  const es = new EventSource('/api/v1/events/stream')

  const GOAL_EVENTS = [
    'goal.created',
    'goal.status_changed',
    'goal.step_appended',
    'goal.step_updated',
    'goal.verification_appended',
    'goal.verification_started',
    'goal.verification_updated',
    'goal.verification_passed',
    'goal.verification_failed',
    'goal.binding_appended',
    'goal.binding_resolved',
    'goal.binding_failed',
    'goal.replay_started',
    'goal.replay_completed',
    'goal.review_requested',
    'goal.review_approved',
    'goal.review_rejected',
    'goal.resumed',
  ] as const

  const handlers: Array<[string, EventListener]> = []

  for (const eventType of GOAL_EVENTS) {
    const handler = (e: MessageEvent) => {
      try {
        const payload = JSON.parse(e.data)
        onEvent({
          type: eventType,
          message: payload.message ?? '',
          data: payload.data ?? null,
        })
      } catch {
        // ignore parse errors
      }
    }
    es.addEventListener(eventType, handler as EventListener)
    handlers.push([eventType, handler as EventListener])
  }

  return () => {
    for (const [type, handler] of handlers) {
      es.removeEventListener(type, handler)
    }
    es.close()
  }
}
