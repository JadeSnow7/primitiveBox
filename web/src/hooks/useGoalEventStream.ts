import { useEffect, useRef } from 'react'
import { createGoalEventStream } from '@/api/goalEvents'
import { useGoalStore } from '@/store/goalStore'

/**
 * Subscribes to goal.* events from the global SSE stream and keeps the
 * goal store up to date.
 *
 * Mount once at the app level (e.g., in WorkspacePage).
 */
export function useGoalEventStream() {
  // Stable reference to store actions — these don't change between renders.
  const loadRef = useRef(useGoalStore.getState().load)
  const refreshRef = useRef(useGoalStore.getState().refresh)
  const loadBindingsRef = useRef(useGoalStore.getState().loadBindings)

  useEffect(() => {
    const cleanup = createGoalEventStream((event) => {
      const { load, refresh, loadBindings } = useGoalStore.getState()

      switch (event.type) {
        case 'goal.created':
          void load()
          break

        case 'goal.status_changed': {
          // message is the goal ID for goal-level events
          const goalId = event.message
          if (goalId) void refresh(goalId)
          break
        }

        case 'goal.step_appended':
        case 'goal.verification_appended':
        case 'goal.verification_started':
        case 'goal.verification_updated':
        case 'goal.verification_passed':
        case 'goal.verification_failed': {
          // data contains goal_id for these events (full struct payload)
          const goalId = event.data?.goal_id as string | undefined
          if (goalId) void refresh(goalId)
          break
        }

        case 'goal.binding_appended':
        case 'goal.binding_resolved':
        case 'goal.binding_failed': {
          const goalId = event.data?.goal_id as string | undefined
          if (goalId) {
            void refresh(goalId)
            void loadBindings(goalId)
          }
          break
        }

        case 'goal.replay_started':
        case 'goal.replay_completed': {
          const goalId = event.message
          if (goalId) void refresh(goalId)
          break
        }

        case 'goal.review_requested':
        case 'goal.review_approved':
        case 'goal.review_rejected':
        case 'goal.resumed': {
          const goalId = event.message
          if (goalId) void refresh(goalId)
          break
        }
      }
    })

    // Keep refs current (not strictly needed since we use getState(), but safe).
    loadRef.current = useGoalStore.getState().load
    refreshRef.current = useGoalStore.getState().refresh
    loadBindingsRef.current = useGoalStore.getState().loadBindings

    return cleanup
  }, [])
}
