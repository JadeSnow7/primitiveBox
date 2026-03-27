import { useMemo } from 'react'
import { useOrchestratorStore } from '@/store/orchestratorStore'

export function ReviewerPanel() {
  const pendingReview = useOrchestratorStore((s) => s.pendingReview)
  const approvePendingReview = useOrchestratorStore((s) => s.approvePendingReview)
  const rejectPendingReview = useOrchestratorStore((s) => s.rejectPendingReview)

  const formattedArgs = useMemo(
    () => JSON.stringify(pendingReview?.params ?? {}, null, 2),
    [pendingReview],
  )

  if (!pendingReview) return null

  return (
    <div className="pointer-events-auto w-full max-w-2xl rounded-2xl border border-[var(--red)]/40 bg-[var(--bg-surface)] shadow-[0_24px_80px_rgba(0,0,0,0.45)]">
      <div className="border-b border-[var(--border)] px-5 py-4">
        <div className="flex items-center justify-between gap-3">
          <div>
            <div className="text-[10px] uppercase tracking-[0.22em] text-[var(--red)]">
              Human Review Required
            </div>
            <div className="mt-1 text-[18px] font-semibold text-[var(--text-primary)]">
              {pendingReview.method}
            </div>
          </div>
          <div className="rounded-full border border-[var(--red)]/30 bg-[var(--red-bg)] px-3 py-1 font-mono text-[11px] text-[var(--red)]">
            {pendingReview.intent.risk_level.toUpperCase()} RISK
          </div>
        </div>
      </div>

      <div className="space-y-4 px-5 py-4">
        <div className="grid gap-3 md:grid-cols-3">
          <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] p-3">
            <div className="text-[10px] uppercase tracking-[0.18em] text-[var(--text-muted)]">Category</div>
            <div className="mt-1 text-[13px] text-[var(--text-primary)]">{pendingReview.intent.category}</div>
          </div>
          <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] p-3">
            <div className="text-[10px] uppercase tracking-[0.18em] text-[var(--text-muted)]">Reversible</div>
            <div className="mt-1 text-[13px] text-[var(--text-primary)]">
              {pendingReview.intent.reversible ? 'Yes' : 'No'}
            </div>
          </div>
          <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] p-3">
            <div className="text-[10px] uppercase tracking-[0.18em] text-[var(--text-muted)]">Side Effect</div>
            <div className="mt-1 text-[13px] text-[var(--text-primary)]">{pendingReview.intent.side_effect}</div>
          </div>
        </div>

        <div>
          <div className="mb-2 text-[10px] uppercase tracking-[0.18em] text-[var(--text-muted)]">
            Requested Payload
          </div>
          <pre className="max-h-80 overflow-auto rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] p-4 font-mono text-[12px] leading-5 text-[var(--text-mono)]">
            {formattedArgs}
          </pre>
        </div>

        <div className="flex items-center justify-end gap-2">
          <button
            onClick={rejectPendingReview}
            className="rounded-lg border border-[var(--red)]/35 bg-[var(--red-bg)] px-4 py-2 text-[12px] font-medium text-[var(--red)] transition-opacity hover:opacity-90"
          >
            Reject
          </button>
          <button
            onClick={approvePendingReview}
            className="rounded-lg bg-[var(--blue)] px-4 py-2 text-[12px] font-medium text-white transition-opacity hover:opacity-90"
          >
            Approve
          </button>
        </div>
      </div>
    </div>
  )
}
