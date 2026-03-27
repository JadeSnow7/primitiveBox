import { create } from 'zustand'
import type { PrimitiveIntent } from '@/types/workspace'

export type OrchestratorPhase = 'IDLE' | 'RUNNING' | 'AWAITING_REVIEW'
export type ReviewDecision = 'approved' | 'rejected'

export interface PendingReview {
  groupId: string
  correlationId: string
  method: string
  params: Record<string, unknown>
  intent: PrimitiveIntent
}

interface OrchestratorState {
  phase: OrchestratorPhase
  pendingReview: PendingReview | null
  setPhase: (phase: OrchestratorPhase) => void
  requestReview: (review: PendingReview) => Promise<ReviewDecision>
  approvePendingReview: () => void
  rejectPendingReview: () => void
  reset: () => void
}

let pendingResolver: ((decision: ReviewDecision) => void) | null = null
let phaseBeforeReview: OrchestratorPhase = 'IDLE'

function resolveReview(decision: ReviewDecision) {
  const resolver = pendingResolver
  pendingResolver = null
  if (resolver) {
    resolver(decision)
  }
}

function cancelPendingReview() {
  resolveReview('rejected')
}

export const useOrchestratorStore = create<OrchestratorState>((set, get) => ({
  phase: 'IDLE',
  pendingReview: null,

  setPhase(phase) {
    set({ phase })
  },

  requestReview(review) {
    cancelPendingReview()
    const currentPhase = get().phase
    phaseBeforeReview = currentPhase === 'AWAITING_REVIEW' ? 'RUNNING' : currentPhase
    set({
      phase: 'AWAITING_REVIEW',
      pendingReview: review,
    })

    return new Promise<ReviewDecision>((resolve) => {
      pendingResolver = resolve
    })
  },

  approvePendingReview() {
    set({
      phase: phaseBeforeReview,
      pendingReview: null,
    })
    resolveReview('approved')
  },

  rejectPendingReview() {
    set({
      phase: phaseBeforeReview,
      pendingReview: null,
    })
    resolveReview('rejected')
  },

  reset() {
    cancelPendingReview()
    phaseBeforeReview = 'IDLE'
    set({
      phase: 'IDLE',
      pendingReview: null,
    })
  },
}))
