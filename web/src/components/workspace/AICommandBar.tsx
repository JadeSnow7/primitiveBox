import { useCallback, useEffect, useRef, useState } from 'react'
import { callOrchestratorAI, buildOrchestratorContext } from '@/api/uiPrimitives'
import { dispatchOrchestratorOutput } from '@/lib/orchestratorDispatcher'
import { runAgentLoop } from '@/lib/agentLoop'
import { useOrchestratorStore } from '@/store/orchestratorStore'
import { usePrimitiveStore } from '@/store/primitiveStore'
import { useWorkspaceStore } from '@/store/workspaceStore'
import { useTimelineStore } from '@/store/timelineStore'
import { useSandboxStore } from '@/store/sandboxStore'
import type { OrchestratorOutput } from '@/types/workspace'
import type { VerificationResult } from '@/types/workspace'
import type { TimelineEntry } from '@/types/timeline'

// ─── Timeline entry kind styling ─────────────────────────────────────────────

const KIND_COLORS: Record<TimelineEntry['kind'], string> = {
  'plan':                 'var(--purple, #c084fc)',
  'execution.call':       'var(--blue)',
  'execution.pending_review': '#f97316',
  'execution.result':     'var(--green, #4ade80)',
  'execution.rejected':   'var(--red, #f87171)',
  'execution.skipped':    'var(--yellow, #facc15)',
  'execution.simulated':  '#fb923c',  // orange-400 — replay stub
  'ui':                   'var(--text-muted)',
}

const KIND_LABELS: Record<TimelineEntry['kind'], string> = {
  'plan':                 'plan',
  'execution.call':       'exec.call',
  'execution.pending_review': 'exec.review',
  'execution.result':     'exec.result',
  'execution.rejected':   'exec.rejected',
  'execution.skipped':    'exec.skipped',
  'execution.simulated':  'exec.simulated',
  'ui':                   'ui',
}

// ─── Component ────────────────────────────────────────────────────────────────

type Mode = 'one-shot' | 'agent'
type Status = 'idle' | 'loading' | 'preview' | 'running' | 'error'

export function AICommandBar() {
  const [input, setInput]   = useState('')
  const [mode, setMode]     = useState<Mode>('one-shot')
  const [status, setStatus] = useState<Status>('idle')
  const [preview, setPreview] = useState<OrchestratorOutput | null>(null)
  const [errorMsg, setErrorMsg] = useState<string | null>(null)
  const [agentIter, setAgentIter] = useState(0)
  const [agentConfidence, setAgentConfidence] = useState<number | null>(null)
  const [agentVerification, setAgentVerification] = useState<VerificationResult | null>(null)
  const [agentDoneReason, setAgentDoneReason] = useState<string | null>(null)

  const abortRef = useRef<AbortController | null>(null)
  const runTokenRef = useRef(0)
  const mountedRef = useRef(true)
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  const workspaceState  = useWorkspaceStore()
  const dispatch        = useWorkspaceStore((s) => s.dispatch)
  const reset           = useWorkspaceStore((s) => s.reset)
  const timelineEntries = useTimelineStore((s) => s.entries)
  const appendTimeline  = useTimelineStore((s) => s.append)
  const clearTimeline   = useTimelineStore((s) => s.clear)

  // Wire to the real selected sandbox — undefined when none is selected
  const activeSandboxId = useSandboxStore((s) => s.selectedId ?? undefined)
  const orchestratorPhase = useOrchestratorStore((s) => s.phase)
  const resetOrchestrator = useOrchestratorStore((s) => s.reset)
  const primitiveCatalogStatus = usePrimitiveStore((s) => s.status)
  const primitiveCatalogError = usePrimitiveStore((s) => s.error)
  const loadPrimitiveCatalog = usePrimitiveStore((s) => s.load)

  const panelCount = Object.keys(workspaceState.panels).length

  useEffect(() => {
    return () => {
      mountedRef.current = false
      abortRef.current?.abort()
    }
  }, [])

  // ── One-shot: Send ───────────────────────────────────────────────────────────

  async function handleSend() {
    if (!input.trim()) return
    if (primitiveCatalogStatus !== 'ready') {
      setErrorMsg(primitiveCatalogError ?? 'Primitive catalog is still loading. Execution is fail-closed.')
      setStatus('error')
      return
    }
    setStatus('loading')
    setPreview(null)
    setErrorMsg(null)
    try {
      const context = buildOrchestratorContext(workspaceState, {
        sandboxId: activeSandboxId,
        timelineEntries,
      })
      const output = await callOrchestratorAI(input, context)
      setPreview(output)
      setStatus('preview')
    } catch (err) {
      setErrorMsg(err instanceof Error ? err.message : 'Unknown error')
      setStatus('error')
    }
  }

  // ── One-shot: Apply ──────────────────────────────────────────────────────────

  async function handleApply() {
    if (!preview) return
    if (primitiveCatalogStatus !== 'ready') {
      setErrorMsg(primitiveCatalogError ?? 'Primitive catalog is still loading. Execution is fail-closed.')
      setStatus('error')
      return
    }
    await dispatchOrchestratorOutput(preview, {
      workspaceDispatch: dispatch,
      appendTimeline,
      sandboxId: activeSandboxId,
    })
    setPreview(null)
    setInput('')
    setStatus('idle')
  }

  function handleDiscard() {
    setPreview(null)
    setStatus('idle')
  }

  // ── Agent: Run ───────────────────────────────────────────────────────────────

  const handleRunAgent = useCallback(async () => {
    if (!input.trim()) return
    if (primitiveCatalogStatus !== 'ready') {
      setErrorMsg(primitiveCatalogError ?? 'Primitive catalog is still loading. Execution is fail-closed.')
      setStatus('error')
      return
    }
    abortRef.current?.abort()
    setStatus('running')
    setAgentIter(0)
    setAgentConfidence(null)
    setAgentVerification(null)
    setAgentDoneReason(null)
    setErrorMsg(null)

    const abort = new AbortController()
    const runToken = runTokenRef.current + 1
    runTokenRef.current = runToken
    abortRef.current = abort
    const isCurrentRun = () => mountedRef.current && runTokenRef.current === runToken && !abort.signal.aborted

    try {
      // Note: no timelineEntries arg — agentLoop reads live store each iteration
      await runAgentLoop(input, {
        workspaceDispatch: dispatch,
        appendTimeline,
        sandboxId: activeSandboxId,
        signal: abort.signal,
        maxIterations: 10,
        confidenceThreshold: 0.5,
        verify: true,
        onIterationStart: (i) => { if (isCurrentRun()) setAgentIter(i + 1) },
        onConfidence: (_i, c) => { if (isCurrentRun()) setAgentConfidence(c) },
        onVerification: (v) => { if (isCurrentRun()) setAgentVerification(v) },
        onDone: (_n, reason) => { if (isCurrentRun()) setAgentDoneReason(reason) },
      })
    } catch (err) {
      if (isCurrentRun()) {
        setErrorMsg(err instanceof Error ? err.message : 'Agent loop error')
      }
    } finally {
      if (runTokenRef.current === runToken) {
        abortRef.current = null
        if (mountedRef.current) {
          setStatus('idle')
        }
      }
    }
  }, [input, dispatch, appendTimeline, activeSandboxId, primitiveCatalogStatus, primitiveCatalogError])

  // ── Agent: Cancel ────────────────────────────────────────────────────────────

  function handleCancel() {
    abortRef.current?.abort()
  }

  // ── Shared keyboard shortcut ─────────────────────────────────────────────────

  function handleKeyDown(e: React.KeyboardEvent) {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
      if (mode === 'agent') void handleRunAgent()
      else void handleSend()
    }
  }

  // Auto-resize textarea
  useEffect(() => {
    const el = textareaRef.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = `${el.scrollHeight}px`
  }, [input])

  // ── Recent timeline ──────────────────────────────────────────────────────────

  const recentEntries = [...timelineEntries].reverse().slice(0, 8)

  // ── Render ───────────────────────────────────────────────────────────────────

  return (
    <div className="flex flex-col gap-2">

      {/* Mode toggle + input row */}
      <div className="flex gap-2">
        {/* Mode toggle */}
        <div className="flex shrink-0 items-start gap-0.5 rounded-lg border border-[var(--border)] p-0.5">
          {(['one-shot', 'agent'] as const).map((m) => (
            <button
              key={m}
              onClick={() => { setMode(m); setPreview(null); setStatus('idle') }}
              disabled={status === 'loading' || status === 'running'}
              className={[
                'rounded-md px-2 py-1 text-[10px] font-medium transition-colors',
                mode === m
                  ? 'bg-[var(--blue)] text-white'
                  : 'text-[var(--text-muted)] hover:bg-[var(--bg-subtle)]',
              ].join(' ')}
            >
              {m === 'one-shot' ? '⚡ One-shot' : '🤖 Agent'}
            </button>
          ))}
        </div>

        <textarea
          ref={textareaRef}
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={handleKeyDown}
          placeholder={
            mode === 'agent'
              ? '告诉 Agent 要实现什么目标，例如：修复 README.md 中的拼写错误'
              : '告诉 AI 要做什么，例如：读取 README.md 并展示'
          }
          rows={2}
          className="field-input flex-1 resize-none leading-5"
          disabled={status === 'loading' || status === 'running'}
        />

        <div className="flex flex-col gap-1.5">
          {mode === 'agent' ? (
            status === 'running' ? (
              <button
                onClick={handleCancel}
                className="rounded-lg border border-[var(--red)]/40 bg-[var(--red-bg)] px-3 py-1.5 text-[12px] font-medium text-[var(--red)] transition-opacity hover:opacity-90"
              >
                Stop
              </button>
            ) : (
              <button
                onClick={() => void handleRunAgent()}
                disabled={!input.trim() || primitiveCatalogStatus !== 'ready'}
                className="rounded-lg bg-[var(--blue)] px-3 py-1.5 text-[12px] font-medium text-white transition-opacity disabled:opacity-40 hover:opacity-90"
              >
                Run
              </button>
            )
          ) : (
            <button
              onClick={() => void handleSend()}
              disabled={status === 'loading' || !input.trim() || primitiveCatalogStatus !== 'ready'}
              className="rounded-lg bg-[var(--blue)] px-3 py-1.5 text-[12px] font-medium text-white transition-opacity disabled:opacity-40 hover:opacity-90"
            >
              {status === 'loading' ? '…' : 'Send'}
            </button>
          )}

          <button
            onClick={() => { reset(); clearTimeline(); resetOrchestrator() }}
            disabled={panelCount === 0 && timelineEntries.length === 0}
            className="rounded-lg border border-[var(--border)] px-3 py-1.5 text-[11px] text-[var(--text-muted)] transition-colors disabled:opacity-30 hover:bg-[var(--bg-subtle)]"
          >
            Reset
          </button>
        </div>
      </div>

      {primitiveCatalogStatus !== 'ready' && (
        <div className="flex items-center justify-between rounded-lg border border-[var(--red)]/20 bg-[var(--red-bg)] px-3 py-2">
          <span className="text-[11px] text-[var(--red)]">
            {primitiveCatalogStatus === 'loading'
              ? 'Loading primitive catalog. Agent execution is paused until intent metadata is available.'
              : `Primitive catalog failed to load. Execution is fail-closed. ${primitiveCatalogError ?? ''}`}
          </span>
          <button
            onClick={() => void loadPrimitiveCatalog()}
            className="rounded-md border border-[var(--red)]/30 px-2.5 py-1 text-[10px] text-[var(--red)] transition-colors hover:bg-[var(--bg-surface)]"
          >
            Retry
          </button>
        </div>
      )}

      {/* Agent running indicator + confidence bar */}
      {status === 'running' && (
        <div className="flex flex-col gap-1 rounded-lg border border-[var(--blue)]/30 bg-[var(--blue-bg)] px-3 py-2">
          <div className="flex items-center gap-2">
            <span className="inline-block h-2 w-2 animate-pulse rounded-full bg-[var(--blue)]" />
            <span className="text-[12px] text-[var(--blue)]">
              {orchestratorPhase === 'AWAITING_REVIEW'
                ? `Awaiting human review · iteration ${agentIter}`
                : `Agent running · iteration ${agentIter}`}
            </span>
          </div>
          {agentConfidence !== null && (
            <div className="flex items-center gap-2">
              <div className="h-1 flex-1 overflow-hidden rounded-full bg-[var(--bg-surface)]">
                <div
                  className="h-full rounded-full transition-all duration-300"
                  style={{
                    width: `${agentConfidence * 100}%`,
                    background: agentConfidence >= 0.7
                      ? 'var(--green, #4ade80)'
                      : agentConfidence >= 0.5
                        ? 'var(--yellow, #facc15)'
                        : 'var(--red, #f87171)',
                  }}
                />
              </div>
              <span className="shrink-0 font-mono text-[10px] text-[var(--text-muted)]">
                {(agentConfidence * 100).toFixed(0)}% confidence
              </span>
            </div>
          )}
        </div>
      )}

      {/* Verification result */}
      {agentVerification && (
        <div
          className={[
            'flex flex-col gap-1.5 rounded-xl border px-3 py-2',
            agentVerification.verified
              ? 'border-[var(--green,#4ade80)]/30 bg-[var(--green-bg,#052e16)]'
              : 'border-[var(--red,#f87171)]/30 bg-[var(--red-bg)]',
          ].join(' ')}
        >
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2">
              <span className="text-[14px]">{agentVerification.verified ? '✅' : '❌'}</span>
              <span
                className="text-[12px] font-medium"
                style={{ color: agentVerification.verified ? 'var(--green, #4ade80)' : 'var(--red, #f87171)' }}
              >
                {agentVerification.verified ? 'Verified' : 'Not verified'}
              </span>
              <span className="font-mono text-[10px] text-[var(--text-muted)]">
                {(agentVerification.confidence * 100).toFixed(0)}%
              </span>
            </div>
            <button
              onClick={() => setAgentVerification(null)}
              className="text-[10px] text-[var(--text-muted)] hover:text-[var(--text-primary)]"
            >
              ✕
            </button>
          </div>
          <p className="text-[11px] text-[var(--text-muted)]">{agentVerification.reason}</p>
          {agentVerification.missing.length > 0 && (
            <div className="flex flex-col gap-0.5">
              <span className="text-[10px] uppercase tracking-[0.15em] text-[var(--red,#f87171)]">Missing steps</span>
              <ul className="ml-3 list-disc space-y-0.5">
                {agentVerification.missing.map((m, i) => (
                  <li key={i} className="text-[11px] text-[var(--text-muted)]">{m}</li>
                ))}
              </ul>
            </div>
          )}
        </div>
      )}

      {/* Agent done badge */}
      {agentDoneReason && status === 'idle' && (
        <div className="flex items-center justify-between rounded-lg border border-[var(--green,#4ade80)]/20 bg-[var(--bg-subtle)] px-3 py-1.5">
          <span className="text-[11px] text-[var(--text-muted)]">
            Agent finished in {agentIter} iteration{agentIter !== 1 ? 's' : ''}&nbsp;·&nbsp;
            <span className={agentDoneReason === 'max-iterations' ? 'text-[var(--yellow,#facc15)]' : 'text-[var(--green,#4ade80)]'}>
              {agentDoneReason === 'done' ? '✓ goal satisfied' : agentDoneReason === 'max-iterations' ? '⚠ max iterations reached' : '⏹ cancelled'}
            </span>
          </span>
          <button
            onClick={() => setAgentDoneReason(null)}
            className="text-[10px] text-[var(--text-muted)] hover:text-[var(--text-primary)]"
          >
            ✕
          </button>
        </div>
      )}

      {/* Error */}
      {status === 'error' && errorMsg && (
        <div className="rounded-lg border border-[var(--red)]/20 bg-[var(--red-bg)] px-3 py-2 text-[12px] text-[var(--red)]">
          {errorMsg}
        </div>
      )}

      {/* One-shot preview */}
      {status === 'preview' && preview && (
        <div className="flex flex-col gap-2 rounded-xl border border-[var(--blue)]/30 bg-[var(--blue-bg)] p-3">
          {/* Header */}
          <div className="flex items-center justify-between">
            <span className="text-[11px] uppercase tracking-[0.2em] text-[var(--blue)]">
              AI Output · group <code className="font-mono text-[10px]">{preview.groupId}</code>
            </span>
            <div className="flex gap-1.5">
              <button
                data-testid="workspace-discard"
                onClick={handleDiscard}
                className="rounded-md px-2.5 py-1 text-[11px] text-[var(--text-muted)] hover:bg-[var(--bg-subtle)]"
              >
                Discard
              </button>
              <button
                data-testid="workspace-apply"
                onClick={() => void handleApply()}
                disabled={primitiveCatalogStatus !== 'ready'}
                className="rounded-md bg-[var(--blue)] px-2.5 py-1 text-[11px] font-medium text-white hover:opacity-90 disabled:opacity-40"
              >
                Apply
              </button>
            </div>
          </div>

          {/* Plan section */}
          {preview.plan && preview.plan.length > 0 && (
            <div className="flex flex-col gap-1">
              <div className="text-[10px] uppercase tracking-[0.15em] text-[var(--text-muted)]">
                Plan · {preview.plan.length} step{preview.plan.length !== 1 ? 's' : ''}
              </div>
              <div className="flex flex-col gap-0.5 rounded-lg bg-[var(--bg-surface)] p-2">
                {preview.plan.map((s, i) => (
                  <div key={i} className="flex items-start gap-2">
                    <span className="shrink-0 w-4 font-mono text-[10px] text-[var(--text-muted)] mt-0.5">
                      {i + 1}.
                    </span>
                    <div className="flex flex-col min-w-0">
                      <span className="text-[11px] font-medium text-[var(--text-primary)] leading-4">
                        {s.step}
                      </span>
                      <span className="font-mono text-[10px] text-[var(--text-muted)]">
                        ↳ {s.reason}
                      </span>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}

          {/* Execution section */}
          {preview.execution && preview.execution.length > 0 && (
            <div className="flex flex-col gap-1">
              <div className="text-[10px] uppercase tracking-[0.15em] text-[var(--blue)]">
                Execution · {preview.execution.length} call{preview.execution.length !== 1 ? 's' : ''}
                {!activeSandboxId && (
                  <span className="ml-2 text-[var(--yellow,#facc15)]">⚠ no sandbox → will skip</span>
                )}
              </div>
              {preview.execution.map((call) => (
                <div key={call.id} className="flex items-start gap-2 rounded-lg bg-[var(--bg-surface)] px-2 py-1.5">
                  <span className="shrink-0 font-mono text-[11px] text-[var(--blue)] w-28">{call.method}</span>
                  <span className="font-mono text-[10px] text-[var(--text-muted)] truncate">
                    {JSON.stringify(call.params)}
                  </span>
                </div>
              ))}
            </div>
          )}

          {/* UI section */}
          {preview.ui && preview.ui.length > 0 && (
            <div className="flex flex-col gap-1">
              <div className="text-[10px] uppercase tracking-[0.15em] text-[var(--text-muted)]">
                UI Primitives · {preview.ui.length}
              </div>
              <pre className="overflow-x-auto rounded-lg bg-[var(--bg-surface)] p-2 font-mono text-[11px] text-[var(--text-mono)] leading-5">
                {JSON.stringify(preview.ui, null, 2)}
              </pre>
            </div>
          )}
        </div>
      )}

      {/* Timeline */}
      {recentEntries.length > 0 && (
        <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-subtle)] p-2">
          <div className="mb-1 flex items-center justify-between">
            <span className="text-[10px] uppercase tracking-[0.2em] text-[var(--text-muted)]">
              Timeline
            </span>
            <button
              onClick={clearTimeline}
              className="text-[10px] text-[var(--text-muted)] hover:text-[var(--text-primary)] transition-colors"
            >
              clear
            </button>
          </div>
          <div className="max-h-32 space-y-0.5 overflow-y-auto">
            {recentEntries.map((entry) => (
              <div key={entry.id} className="flex items-center gap-2 rounded px-1.5 py-0.5 hover:bg-[var(--bg-surface)]">
                <span
                  className="w-24 shrink-0 font-mono text-[10px]"
                  style={{ color: KIND_COLORS[entry.kind] }}
                >
                  {KIND_LABELS[entry.kind]}
                </span>
                <span className="font-mono text-[10px] text-[var(--text-muted)] truncate">
                  {'method' in entry ? entry.method : ''}
                </span>
                <span className="ml-auto shrink-0 font-mono text-[10px] text-[var(--border)]">
                  {entry.groupId.slice(0, 10)}
                </span>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}
