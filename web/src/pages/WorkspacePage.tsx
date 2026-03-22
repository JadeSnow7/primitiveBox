import { useState, useCallback } from 'react'
import { AICommandBar } from '@/components/workspace/AICommandBar'
import { LayoutEngine } from '@/components/workspace/LayoutEngine'
import { useWorkspaceStore } from '@/store/workspaceStore'
import { useTimelineStore } from '@/store/timelineStore'
import { replayTimelineGroup } from '@/lib/replayEngine'
import type { TimelineEntry } from '@/types/timeline'
import type { UIPrimitive } from '@/types/workspace'

// ─── Kind badge ───────────────────────────────────────────────────────────────

const KIND_DOT: Record<TimelineEntry['kind'], string> = {
  'plan':                 '#c084fc',  // purple-400
  'execution.call':       '#60a5fa',  // blue-400
  'execution.result':     '#4ade80',  // green-400
  'execution.skipped':    '#facc15',  // yellow-400
  'execution.simulated':  '#fb923c',  // orange-400 — replay stub
  'ui':                   '#a1a1aa',  // zinc-400
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

/** Extract unique groupIds in insertion order */
function uniqueGroups(entries: TimelineEntry[]): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const e of entries) {
    if (!seen.has(e.groupId)) {
      seen.add(e.groupId)
      out.push(e.groupId)
    }
  }
  return out
}

// ─── WorkspacePage ────────────────────────────────────────────────────────────

export function WorkspacePage() {
  const layout     = useWorkspaceStore((s) => s.layout)
  const panelCount = Object.keys(useWorkspaceStore((s) => s.panels)).length
  const dispatch   = useWorkspaceStore((s) => s.dispatch)
  const resetWs    = useWorkspaceStore((s) => s.resetWorkspace)

  const entries       = useTimelineStore((s) => s.entries)
  const entriesByGroup = useTimelineStore((s) => s.entriesByGroup)
  const appendTimeline = useTimelineStore((s) => s.append)

  const [timelineOpen, setTimelineOpen] = useState(true)
  const [replayingGroup, setReplayingGroup] = useState<string | null>(null)

  const recent = [...entries].reverse().slice(0, 40)
  const groups = uniqueGroups(entries)

  // ── Workspace dispatch shim (converts UIPrimitive[] → store.dispatch) ──────
  const workspaceDispatch = useCallback(
    (primitives: UIPrimitive[]) => dispatch(primitives),
    [dispatch],
  )

  // ── Replay handler ────────────────────────────────────────────────────────
  const handleReplay = useCallback(
    (groupId: string) => {
      setReplayingGroup(groupId)
      try {
        replayTimelineGroup({
          groupId,
          mode: 'simulate',
          entries: entriesByGroup(groupId),
          resetWorkspace: resetWs,
          workspaceDispatch,
          appendTimeline,
        })
      } finally {
        setReplayingGroup(null)
      }
    },
    [entriesByGroup, resetWs, workspaceDispatch, appendTimeline],
  )

  return (
    <div className="flex h-full flex-col gap-3 p-3">
      {/* AI Command Bar */}
      <div className="shrink-0">
        <AICommandBar />
      </div>

      {/* Status bar */}
      <div className="flex shrink-0 items-center gap-3">
        <span className="text-[10px] uppercase tracking-[0.2em] text-[var(--text-muted)]">
          Workspace
        </span>
        <span className="rounded-full border border-[var(--border)] px-2 py-0.5 font-mono text-[10px] text-[var(--text-muted)]">
          {panelCount} / 6 panels
        </span>
        <span className="rounded-full border border-[var(--border)] px-2 py-0.5 font-mono text-[10px] text-[var(--text-muted)]">
          {entries.length} timeline entries
        </span>
        {groups.length > 0 && (
          <span className="rounded-full border border-[var(--border)] px-2 py-0.5 font-mono text-[10px] text-[var(--text-muted)]">
            {groups.length} groups
          </span>
        )}
      </div>

      {/* Layout */}
      <div className="min-h-0 flex-1">
        <LayoutEngine node={layout} />
      </div>

      {/* Timeline bar */}
      {entries.length > 0 && (
        <div className="shrink-0 rounded-xl border border-[var(--border)] bg-[var(--bg-surface)]">
          <button
            onClick={() => setTimelineOpen((o) => !o)}
            className="flex w-full items-center justify-between px-3 py-2 text-left"
          >
            <span className="text-[10px] uppercase tracking-[0.2em] text-[var(--text-muted)]">
              Timeline · {entries.length} entries · {groups.length} groups
            </span>
            <span className="text-[10px] text-[var(--text-muted)]">
              {timelineOpen ? '▾' : '▸'}
            </span>
          </button>

          {timelineOpen && (
            <div className="border-t border-[var(--border)] px-2 pb-2 pt-1">
              <div className="space-y-1 max-h-52 overflow-y-auto">
                {/* Render entries grouped — show a group header row before the
                    first entry of each group, with a ▶ Replay button */}
                {(() => {
                  const rows: React.ReactNode[] = []
                  const seenGroups = new Set<string>()

                  for (const entry of recent) {
                    // Group header (first time we see this groupId in the view)
                    if (!seenGroups.has(entry.groupId)) {
                      seenGroups.add(entry.groupId)
                      const gid = entry.groupId
                      rows.push(
                        <div
                          key={`grp-${gid}`}
                          className="flex items-center gap-2 rounded px-1.5 py-0.5 bg-[var(--bg-subtle)]"
                        >
                          <span className="font-mono text-[10px] text-[var(--text-muted)] flex-1 truncate">
                            ⬡ group {gid.slice(0, 16)}
                          </span>
                          <button
                            id={`replay-btn-${gid}`}
                            disabled={replayingGroup !== null}
                            onClick={() => handleReplay(gid)}
                            className={[
                              'shrink-0 rounded border border-[var(--border)]',
                              'px-1.5 py-0 font-mono text-[9px] leading-5',
                              'transition-colors',
                              replayingGroup === gid
                                ? 'text-[var(--text-muted)] cursor-wait'
                                : 'text-[var(--text-primary)] hover:bg-[var(--bg-subtle)] cursor-pointer',
                            ].join(' ')}
                          >
                            {replayingGroup === gid ? '⏳' : '▶ Replay'}
                          </button>
                        </div>,
                      )
                    }

                    // Entry row
                    rows.push(
                      <div
                        key={entry.id}
                        className="flex items-center gap-2 rounded px-1.5 py-0.5 pl-4 hover:bg-[var(--bg-subtle)]"
                      >
                        {/* Kind dot */}
                        <span
                          className="shrink-0 h-1.5 w-1.5 rounded-full"
                          style={{ background: KIND_DOT[entry.kind] }}
                        />
                        {/* Kind label */}
                        <span
                          className="w-32 shrink-0 font-mono text-[10px]"
                          style={{ color: KIND_DOT[entry.kind] }}
                        >
                          {entry.kind}
                        </span>
                        {/* Method */}
                        <span className="font-mono text-[10px] text-[var(--text-primary)] truncate">
                          {'method' in entry ? entry.method : ''}
                        </span>
                        {/* Timestamp */}
                        <span className="ml-auto shrink-0 font-mono text-[10px] text-[var(--text-muted)]">
                          {entry.ts.slice(11, 19)}
                        </span>
                      </div>,
                    )
                  }
                  return rows
                })()}
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
