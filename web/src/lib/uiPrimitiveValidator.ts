import { PANEL_TYPES } from './uiPrimitiveSchema'
import type { UIPrimitive, OrchestratorOutput, ExecutionCall, PlanStep } from '@/types/workspace'
import { EXECUTION_METHODS } from '@/types/workspace'

// ─── Manual validator — replaces Zod to avoid v3/v4 bundle conflicts ─────────

export type ValidatedUIPrimitive = UIPrimitive

export type ValidationResult<T = ValidatedUIPrimitive[]> =
  | { success: true; data: T }
  | { success: false; error: string }

function isValidPanelType(type: unknown): type is typeof PANEL_TYPES[number] {
  return PANEL_TYPES.includes(type as typeof PANEL_TYPES[number])
}

function validatePrimitive(raw: unknown): ValidatedUIPrimitive | null {
  if (!raw || typeof raw !== 'object') return null
  const p = raw as Record<string, unknown>
  if (typeof p['method'] !== 'string') return null
  const params = p['params']
  if (!params || typeof params !== 'object') return null
  const ps = params as Record<string, unknown>

  switch (p['method']) {
    case 'ui.panel.open': {
      if (!isValidPanelType(ps['type'])) return null
      const entityId = typeof ps['entityId'] === 'string' ? ps['entityId'] : undefined
      const entityIds = Array.isArray(ps['entityIds'])
        ? ps['entityIds'].filter((id): id is string => typeof id === 'string')
        : undefined
      return {
        method: 'ui.panel.open',
        params: {
          type: ps['type'],
          props: (typeof ps['props'] === 'object' && ps['props'] !== null ? ps['props'] : {}) as Record<string, unknown>,
          target: ps['target'] ? validateSemanticRef(ps['target']) ?? undefined : undefined,
          ...(entityId ? { entityId } : {}),
          ...(entityIds && entityIds.length > 0 ? { entityIds } : {}),
        },
      }
    }
    case 'ui.panel.close': {
      const target = validateSemanticRef(ps['target'])
      if (!target) return null
      return { method: 'ui.panel.close', params: { target } }
    }
    case 'ui.layout.split': {
      const target = validateSemanticRef(ps['target'])
      if (!target) return null
      if (ps['direction'] !== 'horizontal' && ps['direction'] !== 'vertical') return null
      return { method: 'ui.layout.split', params: { target, direction: ps['direction'] } }
    }
    case 'ui.focus.panel': {
      const target = validateSemanticRef(ps['target'])
      if (!target) return null
      return { method: 'ui.focus.panel', params: { target } }
    }
    default:
      return null
  }
}

function validateSemanticRef(raw: unknown) {
  if (!raw || typeof raw !== 'object') return null
  const r = raw as Record<string, unknown>
  if (!isValidPanelType(r['type'])) return null
  return {
    type: r['type'] as typeof PANEL_TYPES[number],
    index: typeof r['index'] === 'number' ? r['index'] : undefined,
  }
}

export function validateUIPrimitives(rawInput: unknown): ValidationResult {
  if (!Array.isArray(rawInput)) {
    return { success: false, error: 'Expected an array of primitives' }
  }
  const results: ValidatedUIPrimitive[] = []
  for (let i = 0; i < rawInput.length; i++) {
    const validated = validatePrimitive(rawInput[i])
    if (!validated) {
      return { success: false, error: `Invalid primitive at index ${i}: ${JSON.stringify(rawInput[i])}` }
    }
    results.push(validated)
  }
  return { success: true, data: results }
}

// ─── Plan validator ───────────────────────────────────────────────────────────

function validatePlan(raw: unknown): PlanStep[] | string {
  if (!Array.isArray(raw)) return 'plan must be an array'
  const steps: PlanStep[] = []
  for (let i = 0; i < raw.length; i++) {
    const item = raw[i]
    if (!item || typeof item !== 'object') return `plan[${i}]: not an object`
    const s = item as Record<string, unknown>
    if (typeof s['step'] !== 'string') return `plan[${i}]: missing step string`
    if (typeof s['reason'] !== 'string') return `plan[${i}]: missing reason string`
    steps.push({ step: s['step'], reason: s['reason'] })
  }
  return steps
}

// ─── ExecutionCall validator ──────────────────────────────────────────────────

/**
 * @param appMethods - Optional set of app-registered primitive names from the
 * live catalog (e.g., 'data.insert', 'data.query'). These are accepted in
 * addition to the static EXECUTION_METHODS allowlist so the orchestrator can
 * dispatch app primitives installed via Boxfile packages.
 */
function validateExecutionCall(
  raw: unknown,
  index: number,
  appMethods?: ReadonlySet<string>,
): ExecutionCall | string {
  if (!raw || typeof raw !== 'object') return `execution[${index}]: not an object`
  const e = raw as Record<string, unknown>
  if (typeof e['id'] !== 'string' || !e['id']) return `execution[${index}]: missing id`
  if (typeof e['method'] !== 'string') return `execution[${index}]: missing method`
  const isBuiltin = (EXECUTION_METHODS as readonly string[]).includes(e['method'])
  const isAppMethod = appMethods?.has(e['method']) ?? false
  if (!isBuiltin && !isAppMethod) {
    return `execution[${index}]: unknown method "${e['method']}". Allowed: ${EXECUTION_METHODS.join(', ')}`
  }
  if (!e['params'] || typeof e['params'] !== 'object') return `execution[${index}]: params must be object`
  return {
    id: e['id'],
    method: e['method'] as ExecutionCall['method'],
    params: e['params'] as Record<string, unknown>,
  }
}

// ─── OrchestratorOutput validator ────────────────────────────────────────────

/**
 * Accepts both:
 *   - `{ groupId, plan?, execution?, ui? }` — the canonical orchestrator output
 *   - `UIPrimitive[]`                       — backward compat flat array
 *
 * @param appMethods - Optional set of app-registered primitive names to allow
 * through the execution method gate (built from the live primitive catalog).
 */
export function validateOrchestratorOutput(
  raw: unknown,
  appMethods?: ReadonlySet<string>,
): ValidationResult<OrchestratorOutput> {
  // Backward compat: flat array → ui-only output
  if (Array.isArray(raw)) {
    const uiResult = validateUIPrimitives(raw)
    if (!uiResult.success) return uiResult
    return {
      success: true,
      data: { groupId: `compat-${Date.now()}`, ui: uiResult.data },
    }
  }

  if (!raw || typeof raw !== 'object') {
    return { success: false, error: 'Orchestrator output must be an object or array' }
  }

  const o = raw as Record<string, unknown>
  if (typeof o['groupId'] !== 'string' || !o['groupId']) {
    return { success: false, error: 'Orchestrator output missing groupId' }
  }

  // Validate plan[] (informational — soft validation)
  let plan: PlanStep[] | undefined
  if (o['plan'] !== undefined) {
    const planResult = validatePlan(o['plan'])
    if (typeof planResult === 'string') return { success: false, error: `plan: ${planResult}` }
    plan = planResult
  }

  // Validate execution[]
  let execution: ExecutionCall[] | undefined
  if (o['execution'] !== undefined) {
    if (!Array.isArray(o['execution'])) {
      return { success: false, error: 'execution must be an array' }
    }
    execution = []
    for (let i = 0; i < o['execution'].length; i++) {
      const result = validateExecutionCall(o['execution'][i], i, appMethods)
      if (typeof result === 'string') return { success: false, error: result }
      execution.push(result)
    }
  }

  // Validate ui[]
  let ui: ValidatedUIPrimitive[] | undefined
  if (o['ui'] !== undefined) {
    const uiResult = validateUIPrimitives(o['ui'])
    if (!uiResult.success) return { success: false, error: `ui: ${uiResult.error}` }
    ui = uiResult.data
  }

  // Validate status (optional agent loop signal)
  let status: 'continue' | 'done' | undefined
  if (o['status'] !== undefined) {
    if (o['status'] !== 'continue' && o['status'] !== 'done') {
      return { success: false, error: `status must be 'continue' or 'done', got: ${JSON.stringify(o['status'])}` }
    }
    status = o['status']
  }

  // Validate confidence (optional 0.0–1.0)
  let confidence: number | undefined
  if (o['confidence'] !== undefined) {
    if (typeof o['confidence'] !== 'number' || o['confidence'] < 0 || o['confidence'] > 1) {
      return { success: false, error: `confidence must be a number 0.0–1.0, got: ${JSON.stringify(o['confidence'])}` }
    }
    confidence = o['confidence']
  }

  return { success: true, data: { groupId: o['groupId'], plan, execution, ui, status, confidence } }
}
