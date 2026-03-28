import { useEffect, useRef, useState } from 'react'
import { useGoalStore } from '@/store/goalStore'
import type { Goal, GoalBinding, GoalBindingStatus, GoalReview, GoalStatus, GoalStepStatus, GoalVerificationStatus } from '@/types/goal'
import type { WorkspacePanel } from '@/types/workspace'

function StatusBadge({ status }: { status: GoalStatus }) {
  const colors: Record<GoalStatus, string> = {
    created:   'bg-[var(--text-muted)] text-[var(--text-inverse)]',
    executing: 'bg-amber-500 text-white',
    verifying: 'bg-blue-500 text-white',
    completed: 'bg-[var(--teal)] text-white',
    failed:    'bg-red-500 text-white',
    paused:    'bg-orange-500 text-white',
  }
  return (
    <span className={`rounded px-1.5 py-0.5 font-mono text-[10px] uppercase tracking-wider ${colors[status] ?? 'bg-gray-400 text-white'}`}>
      {status}
    </span>
  )
}

function StepStatusIcon({ status }: { status: GoalStepStatus }) {
  const map: Record<GoalStepStatus, { char: string; color: string }> = {
    pending:         { char: '○', color: 'text-[var(--text-muted)]' },
    running:         { char: '◑', color: 'text-amber-500' },
    passed:          { char: '✓', color: 'text-[var(--teal)]' },
    failed:          { char: '✗', color: 'text-red-500' },
    skipped:         { char: '—', color: 'text-[var(--text-muted)]' },
    awaiting_review: { char: '⏸', color: 'text-orange-500' },
    rolled_back:     { char: '↩', color: 'text-purple-500' },
  }
  const { char, color } = map[status] ?? map.pending
  return <span className={`font-mono text-[12px] ${color}`}>{char}</span>
}

function VerificationBadge({ status }: { status: GoalVerificationStatus }) {
  const colors: Record<GoalVerificationStatus, string> = {
    pending: 'bg-amber-500/20 text-amber-600',
    running: 'bg-blue-500/20 text-blue-600',
    passed:  'bg-[var(--teal)]/20 text-[var(--teal)]',
    failed:  'bg-red-500/20 text-red-500',
  }
  return (
    <span className={`rounded px-1.5 py-0.5 text-[10px] ${colors[status]}`}>{status}</span>
  )
}

function stringifyEvidence(evidence: Record<string, unknown> | undefined): string | null {
  if (!evidence) return null
  if (typeof evidence['body'] === 'string') return String(evidence['body']).slice(0, 120)
  if (typeof evidence['error'] === 'string') return String(evidence['error'])
  if (typeof evidence['result'] === 'object' && evidence['result'] !== null) {
    return JSON.stringify(evidence['result']).slice(0, 120)
  }
  if (typeof evidence['assertion'] === 'object' && evidence['assertion'] !== null) {
    return JSON.stringify(evidence['assertion']).slice(0, 120)
  }
  return JSON.stringify(evidence).slice(0, 120)
}

function BindingTypeBadge({ type }: { type: GoalBinding['binding_type'] }) {
  const colors: Record<GoalBinding['binding_type'], string> = {
    service_endpoint: 'bg-blue-500/20 text-blue-600',
    network_exposure: 'bg-purple-500/20 text-purple-600',
    credential:       'bg-[var(--text-muted)]/20 text-[var(--text-muted)]',
  }
  const labels: Record<GoalBinding['binding_type'], string> = {
    service_endpoint: 'svc',
    network_exposure: 'net',
    credential:       'cred',
  }
  return (
    <span className={`rounded px-1.5 py-0.5 font-mono text-[10px] ${colors[type]}`}>
      {labels[type]}
    </span>
  )
}

function BindingStatusBadge({ status }: { status: GoalBindingStatus }) {
  const colors: Record<GoalBindingStatus, string> = {
    pending:  'text-amber-500',
    resolved: 'text-[var(--teal)]',
    failed:   'text-red-500',
  }
  return <span className={`text-[10px] ${colors[status]}`}>{status}</span>
}

function PendingReviewCard({ goalId, review }: { goalId: string; review: GoalReview }) {
  const { approve, reject } = useGoalStore()
  const [submitting, setSubmitting] = useState(false)

  const handleApprove = async () => {
    setSubmitting(true)
    try { await approve(goalId, review.id) } finally { setSubmitting(false) }
  }

  const handleReject = async () => {
    setSubmitting(true)
    try { await reject(goalId, review.id) } finally { setSubmitting(false) }
  }

  return (
    <div className="rounded-lg border border-orange-400/40 bg-orange-500/5 px-3 py-2.5">
      <div className="mb-1.5 flex items-center gap-2">
        <span className="text-[11px] font-semibold uppercase tracking-wider text-orange-500">
          Review Required
        </span>
        <span className="rounded bg-orange-500/20 px-1.5 py-0.5 font-mono text-[10px] text-orange-600">
          {review.risk_level}
        </span>
        {!review.reversible && (
          <span className="rounded bg-red-500/20 px-1.5 py-0.5 font-mono text-[10px] text-red-600">
            irreversible
          </span>
        )}
      </div>
      <div className="mb-2 font-mono text-[11px] text-[var(--text-mono)]">{review.primitive}</div>
      {review.side_effect && (
        <div className="mb-2 text-[11px] text-[var(--text-secondary)]">{review.side_effect}</div>
      )}
      <div className="flex gap-2">
        <button
          disabled={submitting}
          onClick={handleApprove}
          className="rounded border border-[var(--teal)] bg-[var(--teal)]/10 px-2.5 py-1 text-[11px] text-[var(--teal)] hover:bg-[var(--teal)]/20 disabled:opacity-50"
        >
          Approve
        </button>
        <button
          disabled={submitting}
          onClick={handleReject}
          className="rounded border border-red-400 bg-red-500/10 px-2.5 py-1 text-[11px] text-red-500 hover:bg-red-500/20 disabled:opacity-50"
        >
          Reject
        </button>
      </div>
    </div>
  )
}

function GoalContent({ goal }: { goal: Goal }) {
  const { replay, execute, resume, loadBindings, bindings, refresh } = useGoalStore()
  const goalBindings = bindings[goal.id] ?? goal.bindings ?? []
  const canReplay = goal.status === 'completed' || goal.status === 'failed' || goal.status === 'paused'
  const canExecute = goal.status === 'created'
  const isExecuting = goal.status === 'executing'
  const isVerifying = goal.status === 'verifying'
  const isPaused = goal.status === 'paused'
  const pendingReviews = (goal.reviews ?? []).filter((r) => r.status === 'pending')
  const canResume = isPaused && pendingReviews.length === 0
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null)

  useEffect(() => {
    loadBindings(goal.id)
  }, [goal.id, loadBindings])

  // Polling fallback: refresh every 2 s while executing, verifying, or paused.
  useEffect(() => {
    if (!isExecuting && !isPaused && !isVerifying) {
      if (intervalRef.current !== null) {
        clearInterval(intervalRef.current)
        intervalRef.current = null
      }
      return
    }
    intervalRef.current = setInterval(() => {
      void refresh(goal.id)
    }, 2000)
    return () => {
      if (intervalRef.current !== null) {
        clearInterval(intervalRef.current)
        intervalRef.current = null
      }
    }
  }, [isExecuting, isPaused, isVerifying, goal.id, refresh])

  return (
    <div className="flex h-full flex-col gap-3 overflow-y-auto p-3">
      {/* Header */}
      <div className="flex items-start justify-between gap-2">
        <div className="flex-1">
          <div className="flex items-center gap-2">
            <span className="font-mono text-[10px] text-[var(--text-muted)]">{goal.id}</span>
            <StatusBadge status={goal.status} />
            {(isExecuting || isVerifying) && (
              <span className="inline-block h-1.5 w-1.5 animate-pulse rounded-full bg-amber-400" />
            )}
          </div>
          <div className="mt-1 text-[13px] text-[var(--text-primary)]">{goal.description}</div>
        </div>
        <div className="flex shrink-0 gap-1">
          {canExecute && (
            <button
              onClick={() => execute(goal.id)}
              className="rounded-lg border border-[var(--blue)] bg-[var(--blue)]/10 px-2.5 py-1 text-[11px] text-[var(--blue)] hover:bg-[var(--blue)]/20"
            >
              Execute
            </button>
          )}
          {canResume && (
            <button
              onClick={() => resume(goal.id)}
              className="rounded-lg border border-orange-400 bg-orange-500/10 px-2.5 py-1 text-[11px] text-orange-500 hover:bg-orange-500/20"
            >
              Resume
            </button>
          )}
          {canReplay && (
            <button
              onClick={() => replay(goal.id)}
              className="rounded-lg border border-[var(--border)] bg-[var(--bg-subtle)] px-2.5 py-1 text-[11px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
            >
              Replay
            </button>
          )}
        </div>
      </div>

      {/* Pending Reviews */}
      {pendingReviews.length > 0 && (
        <div className="space-y-2">
          {pendingReviews.map((review) => (
            <PendingReviewCard key={review.id} goalId={goal.id} review={review} />
          ))}
        </div>
      )}

      {/* Packages */}
      {goal.packages.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {goal.packages.map((pkg) => (
            <span
              key={pkg}
              className="rounded border border-[var(--border)] bg-[var(--bg-subtle)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--text-secondary)]"
            >
              {pkg}
            </span>
          ))}
        </div>
      )}

      {/* Steps */}
      {goal.steps.length > 0 && (
        <div>
          <div className="mb-1 text-[11px] uppercase tracking-[0.15em] text-[var(--text-muted)]">
            Steps
          </div>
          <div className="space-y-1">
            {goal.steps.map((step) => (
              <div
                key={step.id}
                className="flex items-center gap-2 rounded-lg border border-[var(--border)] bg-[var(--bg-subtle)] px-2.5 py-1.5"
              >
                <span className="w-5 shrink-0 text-center text-[10px] font-mono text-[var(--text-muted)]">
                  {step.seq}
                </span>
                <StepStatusIcon status={step.status} />
                <span className="flex-1 font-mono text-[11px] text-[var(--text-mono)]">
                  {step.primitive}
                </span>
                {step.checkpoint_id && (
                  <span className="rounded bg-[var(--teal)]/10 px-1 py-0.5 font-mono text-[9px] text-[var(--teal)]">
                    ckpt
                  </span>
                )}
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Verifications */}
      {goal.verifications.length > 0 && (
        <div>
          <div className="mb-1 text-[11px] uppercase tracking-[0.15em] text-[var(--text-muted)]">
            Verifications
          </div>
          <div className="space-y-1">
            {goal.verifications.map((v) => {
              const evidenceSummary = stringifyEvidence(v.evidence)
              const failed = v.status === 'failed'
              return (
              <div
                key={v.id}
                className={`rounded-lg border px-2.5 py-1.5 ${
                  failed
                    ? 'border-red-400/50 bg-red-500/5'
                    : 'border-[var(--border)] bg-[var(--bg-subtle)]'
                }`}
              >
                <div className="flex items-start gap-2">
                  <VerificationBadge status={v.status} />
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <span className="font-mono text-[11px] text-[var(--text-mono)]">
                        {v.check_type ?? 'unknown'}
                      </span>
                      {v.verdict && (
                        <span className={`text-[11px] ${failed ? 'text-red-500' : 'text-[var(--text-secondary)]'}`}>
                          {v.verdict}
                        </span>
                      )}
                    </div>
                    {evidenceSummary && (
                      <div className="mt-1 break-all font-mono text-[10px] text-[var(--text-muted)]">
                        {evidenceSummary}
                      </div>
                    )}
                  </div>
                </div>
              </div>
              )
            })}
          </div>
        </div>
      )}

      {/* Bindings */}
      {goalBindings.length > 0 && (
        <div>
          <div className="mb-1 text-[11px] uppercase tracking-[0.15em] text-[var(--text-muted)]">
            Bindings
          </div>
          <div className="space-y-1">
            {goalBindings.map((b) => (
              <div
                key={b.id}
                className="flex flex-col gap-0.5 rounded-lg border border-[var(--border)] bg-[var(--bg-subtle)] px-2.5 py-1.5"
              >
                <div className="flex items-center gap-2">
                  <BindingTypeBadge type={b.binding_type} />
                  <span className="font-mono text-[11px] text-[var(--text-mono)]">
                    {b.source_ref} → {b.target_ref}
                  </span>
                  <BindingStatusBadge status={b.status} />
                </div>
                {b.resolved_value && (
                  <span className="font-mono text-[10px] text-[var(--text-muted)]">
                    = {b.resolved_value}
                  </span>
                )}
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Empty state */}
      {goal.steps.length === 0 && goal.verifications.length === 0 && goalBindings.length === 0 && (
        <div className="flex flex-1 items-center justify-center text-[12px] text-[var(--text-muted)]">
          No steps yet
        </div>
      )}
    </div>
  )
}

export function GoalPanel({ panel }: { panel: WorkspacePanel }) {
  const goalId = panel.props['goalId'] as string | undefined
  const { goals } = useGoalStore()
  const goal = goalId ? goals.find((g) => g.id === goalId) : undefined

  if (!goalId || !goal) {
    return (
      <div className="flex h-full items-center justify-center text-[12px] text-[var(--text-muted)]">
        No goal selected
      </div>
    )
  }

  return <GoalContent goal={goal} />
}
