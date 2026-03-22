/**
 * executionMapper.ts
 *
 * Maps a completed execution primitive result → a list of UI primitives to
 * automatically open/update panels in the workspace.
 *
 * Architecture: registry pattern (not a hard-coded switch) so that future
 * primitives — including app-registered ones — can plug in their own mappers
 * without touching this file.
 *
 * Dedup contract:
 *   Every generated UIPrimitive carries `props.sourceExecutionId = entry.id`
 *   so the caller (orchestratorDispatcher) can detect when a panel for that
 *   particular execution result is already open and skip re-dispatch.
 */

import type { UIPrimitive, PanelType } from '@/types/workspace'

// ─── Internal helper type ─────────────────────────────────────────────────────

/** Shorthand for the ui.panel.open variant — used internally to avoid
 *  spread gymnastics on the discriminated union. */
type PanelOpenPrimitive = Extract<UIPrimitive, { method: 'ui.panel.open' }>

function openPanel(
  type: PanelType,
  props: Record<string, unknown>,
): PanelOpenPrimitive {
  return { method: 'ui.panel.open', params: { type, props } }
}

// ─── Mapper type ──────────────────────────────────────────────────────────────

/**
 * A mapper function for one execution method.
 * Receives the params sent to the primitive and the result it returned.
 * Must return [] if it cannot produce a meaningful UI (don't open empty panels).
 * `sourceExecutionId` will be stamped on all returned primitives by the caller.
 */
export type ExecutionUIMapper = (
  params: Record<string, unknown>,
  result: unknown,
) => PanelOpenPrimitive[]

// ─── Individual mappers ───────────────────────────────────────────────────────

const mapFsRead: ExecutionUIMapper = (params, result) => {
  if (!result) return []
  const path = typeof params['path'] === 'string' ? params['path'] : 'file'
  return [openPanel('primitive', { title: path, content: result })]
}

const mapFsList: ExecutionUIMapper = (params, result) => {
  if (!Array.isArray(result) && !result) return []
  const path = typeof params['path'] === 'string' ? params['path'] : '.'
  return [openPanel('primitive', { title: `ls ${path}`, items: result })]
}

const mapFsDiff: ExecutionUIMapper = (params, result) => {
  if (!result) return []
  const path = typeof params['path'] === 'string' ? params['path'] : ''
  return [openPanel('diff', { path, diff: result })]
}

const mapShellExec: ExecutionUIMapper = (_params, result) => {
  const r = result as Record<string, unknown> | null | undefined
  return [
    openPanel('event_stream', {
      stdout:    r?.['stdout']    ?? '',
      stderr:    r?.['stderr']    ?? '',
      exit_code: r?.['exit_code'] ?? null,
    }),
  ]
}

const mapStateCheckpoint: ExecutionUIMapper = (_params, result) => {
  if (!result) return []
  return [openPanel('checkpoint', { checkpoint: result })]
}

// ─── Registry ─────────────────────────────────────────────────────────────────

/**
 * Registry of execution method → UI mapper.
 * Use `registerExecutionUIMapper` to add app-level mappings at runtime.
 */
export const executionUIRegistry: Record<string, ExecutionUIMapper> = {
  'fs.read':          mapFsRead,
  'fs.list':          mapFsList,
  'fs.diff':          mapFsDiff,
  'shell.exec':       mapShellExec,
  'state.checkpoint': mapStateCheckpoint,
}

/**
 * Register a custom mapper for an execution method.
 * Useful for app primitives that want to drive the workspace UI.
 */
export function registerExecutionUIMapper(method: string, mapper: ExecutionUIMapper): void {
  executionUIRegistry[method] = mapper
}

// ─── Public API ───────────────────────────────────────────────────────────────

/**
 * Map an execution primitive result to a list of UI primitives.
 *
 * @param method             The primitive method name (e.g. "fs.read")
 * @param params             The params that were sent
 * @param result             The raw result returned by the primitive
 * @param sourceExecutionId  The timeline entry ID — stamped into props for dedup
 * @returns                  Array of UIPrimitives to dispatch (may be empty)
 */
export function mapExecutionResultToUI(
  method: string,
  params: Record<string, unknown>,
  result: unknown,
  sourceExecutionId: string,
): UIPrimitive[] {
  const mapper = executionUIRegistry[method]
  if (!mapper) return []

  const raw = mapper(params, result)

  // Stamp sourceExecutionId into each panel's props for dedup.
  // All mappers return PanelOpenPrimitive[], so props is always present.
  return raw.map(
    (p): PanelOpenPrimitive => ({
      method: 'ui.panel.open',
      params: {
        type: p.params.type,
        props: { ...p.params.props, sourceExecutionId },
      },
    }),
  )
}
