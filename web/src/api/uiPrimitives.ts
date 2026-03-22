import { ORCHESTRATOR_SYSTEM_PROMPT } from '@/lib/orchestratorSystemPrompt'
import { validateOrchestratorOutput } from '@/lib/uiPrimitiveValidator'
import type { UIPrimitive, OrchestratorOutput, ExecutionCall, PlanStep } from '@/types/workspace'
import type { WorkspaceState } from '@/store/workspaceStore'
import type { TimelineEntry } from '@/types/timeline'

// ─── Context ──────────────────────────────────────────────────────────────────

/**
 * Open entity description, injected into LLM context so the model can avoid
 * re-reading files already open in the workspace.
 */
export interface OpenEntity {
  panelType: string
  props: Record<string, unknown>
}

export interface OrchestratorContext {
  uiState: { panelCount: number; openTypes: string[] }
  /** Currently open panels with their props — used for entity-aware dedup */
  openEntities: OpenEntity[]
  sandboxId?: string
  lastExecution?: { method: string; result?: unknown }
  /** Last 5 timeline entry kinds for lightweight context injection */
  timelineSummary: string[]
}

export function buildOrchestratorContext(
  state: WorkspaceState,
  opts: { sandboxId?: string; timelineEntries: TimelineEntry[] },
): OrchestratorContext {
  const panels = Object.values(state.panels)
  return {
    uiState: {
      panelCount: panels.length,
      openTypes: panels.map((p) => p.type),
    },
    openEntities: panels.map((p) => ({
      panelType: p.type,
      props: p.props,
    })),
    sandboxId: opts.sandboxId,
    lastExecution: opts.timelineEntries
      .filter((e) => e.kind === 'execution.call')
      .slice(-1)
      .map((e) => ({ method: e.method }))[0],
    timelineSummary: opts.timelineEntries.slice(-5).map((e) => e.kind),
  }
}

// ─── Legacy helper (backward compat) ─────────────────────────────────────────

/** @deprecated Use buildOrchestratorContext instead */
export interface UIState {
  panelCount: number
  openTypes: string[]
}

/** @deprecated Use buildOrchestratorContext instead */
export function buildUIStateContext(state: WorkspaceState): UIState {
  return {
    panelCount: Object.keys(state.panels).length,
    openTypes: Object.values(state.panels).map((p) => p.type),
  }
}

// ─── LLM path ─────────────────────────────────────────────────────────────────

/**
 * Build the user message content that describes current workspace state.
 * The LLM sees this alongside the system prompt to produce entity-aware output.
 */
function buildUserMessage(userInput: string, context: OrchestratorContext): string {
  const lines: string[] = []
  lines.push(`User request: ${userInput}`)
  lines.push('')
  lines.push('## Current Workspace State')
  lines.push(`- Open panels: ${context.uiState.panelCount}`)
  if (context.uiState.openTypes.length > 0) {
    lines.push(`- Panel types: ${context.uiState.openTypes.join(', ')}`)
  }
  if (context.openEntities.length > 0) {
    lines.push('- Open entities:')
    for (const entity of context.openEntities) {
      const path = entity.props['path'] ?? entity.props['title'] ?? ''
      const id = entity.props['sourceExecutionId'] ?? ''
      lines.push(`  - ${entity.panelType}${path ? `: ${path}` : ''}${id ? ` [id:${id}]` : ''}`)
    }
  }
  if (context.sandboxId) {
    lines.push(`- Active sandbox: ${context.sandboxId}`)
  } else {
    lines.push('- No active sandbox (execution calls will be skipped)')
  }
  if (context.timelineSummary.length > 0) {
    lines.push(`- Recent timeline: ${context.timelineSummary.join(' → ')}`)
  }
  return lines.join('\n')
}

/**
 * Call the OpenAI-compatible LLM endpoint.
 * Reads VITE_ORCHESTRATOR_URL and VITE_ORCHESTRATOR_KEY from env.
 * Returns the parsed OrchestratorOutput, or null on any failure.
 */
async function callLLMOrchestrator(
  userInput: string,
  context: OrchestratorContext,
): Promise<OrchestratorOutput | null> {
  const url = import.meta.env.VITE_ORCHESTRATOR_URL as string | undefined
  const apiKey = import.meta.env.VITE_ORCHESTRATOR_KEY as string | undefined
  const model = (import.meta.env.VITE_ORCHESTRATOR_MODEL as string | undefined) ?? 'gpt-4o-mini'

  if (!url) return null

  try {
    const headers: Record<string, string> = { 'Content-Type': 'application/json' }
    if (apiKey) headers['Authorization'] = `Bearer ${apiKey}`

    const body = JSON.stringify({
      model,
      messages: [
        { role: 'system', content: ORCHESTRATOR_SYSTEM_PROMPT },
        { role: 'user', content: buildUserMessage(userInput, context) },
      ],
      temperature: 0.2,
      response_format: { type: 'json_object' },
    })

    const res = await fetch(url, { method: 'POST', headers, body })
    if (!res.ok) {
      console.warn('[orchestrator] LLM HTTP error:', res.status, await res.text())
      return null
    }

    const data = await res.json() as {
      choices?: Array<{ message?: { content?: string } }>
    }
    const content = data.choices?.[0]?.message?.content
    if (!content) {
      console.warn('[orchestrator] LLM returned empty content')
      return null
    }

    // Strip accidental markdown fences
    const stripped = content.replace(/^```(?:json)?\n?/, '').replace(/\n?```$/, '').trim()
    const parsed: unknown = JSON.parse(stripped)

    const validated = validateOrchestratorOutput(parsed)
    if (!validated.success) {
      console.warn('[orchestrator] LLM output failed validation:', validated.error)
      return null
    }
    return validated.data
  } catch (err) {
    console.warn('[orchestrator] LLM call failed, falling back to local:', err)
    return null
  }
}

// ─── Local orchestrator (enhanced fallback) ───────────────────────────────────

type Intent =
  | 'modify'         // modify/write/fix/edit file → checkpoint-first
  | 'restore'        // undo/revert/restore → state.restore
  | 'debug'          // analyze trace, failure
  | 'read-file'      // read file
  | 'logs'           // logs, event stream
  | 'shell'          // run command
  | 'checkpoint'     // checkpoint, snapshot
  | 'sandbox'        // sandbox panel
  | 'diff'           // diff, compare
  | 'test'           // run tests, verify
  | 'search'         // search code
  | 'default'

function classifyIntent(input: string): Intent {
  const s = input.toLowerCase()
  if (s.includes('修改') || s.includes('modify') || s.includes('write') || s.includes('fix') || s.includes('edit') || s.includes('update') || s.includes('更新') || s.includes('编辑')) return 'modify'
  if (s.includes('restore') || s.includes('undo') || s.includes('revert') || s.includes('回滚') || s.includes('撤销') || s.includes('恢复')) return 'restore'
  if (s.includes('trace') || s.includes('分析') || s.includes('失败') || s.includes('analyze') || s.includes('debug') || s.includes('调试')) return 'debug'
  if (s.includes('读取') || s.includes('read') || s.includes('文件') || s.includes('file') || s.includes('open') || s.includes('打开')) return 'read-file'
  if (s.includes('日志') || s.includes('log') || s.includes('event') || s.includes('stream') || s.includes('流')) return 'logs'
  if (s.includes('shell') || s.includes('命令') || s.includes('exec') || s.includes('执行') || s.includes('run')) return 'shell'
  if (s.includes('checkpoint') || s.includes('检查点') || s.includes('快照') || s.includes('snapshot')) return 'checkpoint'
  if (s.includes('sandbox') || s.includes('沙箱') || s.includes('容器')) return 'sandbox'
  if (s.includes('diff') || s.includes('对比') || s.includes('compare')) return 'diff'
  if (s.includes('test') || s.includes('测试') || s.includes('verify') || s.includes('验证')) return 'test'
  if (s.includes('search') || s.includes('搜索') || s.includes('find') || s.includes('查找')) return 'search'
  return 'default'
}

function extractPath(input: string): string {
  const quoted = input.match(/[""']([^""']+\.[a-zA-Z0-9]+)[""']/)
  if (quoted) return quoted[1]
  const word = input.match(/\b([\w./-]+\.[a-zA-Z0-9]{1,10})\b/)
  if (word) return word[1]
  return 'README.md'
}

function extractCommand(input: string): string {
  const after = input.match(/(?:run|exec|执行|命令)\s+(.+)/i)
  if (after) return after[1].trim()
  return 'echo hello'
}

function extractQuery(input: string): string {
  const after = input.match(/(?:search|find|搜索|查找)\s+(.+)/i)
  if (after) return after[1].trim()
  return input.trim()
}

function makeCallId(): string {
  return `call-${Math.random().toString(36).slice(2, 9)}`
}

function buildLocalOutput(intent: Intent, input: string, context: OrchestratorContext): OrchestratorOutput {
  const groupId = `grp-${Math.random().toString(36).slice(2, 9)}-${Date.now()}`
  const openTypes = new Set(context.uiState.openTypes)

  const plan: PlanStep[] = []
  const execution: ExecutionCall[] = []
  const ui: UIPrimitive[] = []

  switch (intent) {
    case 'modify': {
      const path = extractPath(input)
      plan.push(
        { step: 'create checkpoint', reason: 'safe rollback point before modification' },
        { step: `read ${path}`, reason: 'inspect current content before editing' },
        { step: 'apply modification', reason: 'write updated content' },
        { step: 'show diff', reason: 'verify the change' },
      )
      execution.push({ id: makeCallId(), method: 'state.checkpoint', params: {} })
      execution.push({ id: makeCallId(), method: 'fs.read', params: { path } })
      // fs.write content is TBD — placeholder for human/LLM to fill
      execution.push({ id: makeCallId(), method: 'fs.diff', params: { path } })
      if (!openTypes.has('diff')) {
        ui.push({ method: 'ui.panel.open', params: { type: 'diff', props: { path } } })
      }
      break
    }

    case 'restore': {
      plan.push(
        { step: 'restore checkpoint', reason: 'revert to previous safe state' },
        { step: 'show checkpoint panel', reason: 'confirm rollback state' },
      )
      execution.push({ id: makeCallId(), method: 'state.restore', params: {} })
      if (!openTypes.has('checkpoint')) {
        ui.push({ method: 'ui.panel.open', params: { type: 'checkpoint', props: {} } })
      }
      break
    }

    case 'debug': {
      plan.push(
        { step: 'open trace panel', reason: 'inspect execution flow' },
        { step: 'split layout', reason: 'side-by-side comparison' },
        { step: 'open diff panel', reason: 'identify differences' },
        { step: 'focus trace', reason: 'direct attention to failure point' },
      )
      if (!openTypes.has('trace')) {
        ui.push({ method: 'ui.panel.open', params: { type: 'trace', props: {} } })
      }
      if (!openTypes.has('diff')) {
        ui.push({ method: 'ui.layout.split', params: { target: { type: 'trace', index: 0 }, direction: 'horizontal' } })
        ui.push({ method: 'ui.panel.open', params: { type: 'diff', props: {} } })
      }
      ui.push({ method: 'ui.focus.panel', params: { target: { type: 'trace', index: 0 } } })
      break
    }

    case 'read-file': {
      const path = extractPath(input)
      // Entity-aware: if this path is already open, just focus it
      const alreadyOpen = context.openEntities.some(
        (e) => e.panelType === 'primitive' && (e.props['path'] === path || e.props['title'] === path),
      )
      if (alreadyOpen) {
        plan.push({ step: `focus existing ${path} panel`, reason: 'file already open — avoid redundant read' })
        ui.push({ method: 'ui.focus.panel', params: { target: { type: 'primitive', index: 0 } } })
      } else {
        plan.push(
          { step: `read file: ${path}`, reason: 'get file content for inspection' },
        )
        execution.push({ id: makeCallId(), method: 'fs.read', params: { path } })
      }
      break
    }

    case 'logs': {
      plan.push({ step: 'open event stream panel', reason: 'surface runtime logs' })
      if (!openTypes.has('event_stream')) {
        ui.push({ method: 'ui.panel.open', params: { type: 'event_stream', props: {} } })
      }
      break
    }

    case 'shell': {
      const command = extractCommand(input)
      plan.push(
        { step: `execute: ${command}`, reason: 'run requested shell operation' },
      )
      execution.push({ id: makeCallId(), method: 'shell.exec', params: { command } })
      break
    }

    case 'checkpoint': {
      plan.push(
        { step: 'create checkpoint', reason: 'save current workspace state' },
        { step: 'open checkpoint panel', reason: 'confirm and inspect saved state' },
      )
      execution.push({ id: makeCallId(), method: 'state.checkpoint', params: {} })
      if (!openTypes.has('checkpoint')) {
        ui.push({ method: 'ui.panel.open', params: { type: 'checkpoint', props: {} } })
      }
      break
    }

    case 'sandbox': {
      plan.push({ step: 'open sandbox panel', reason: 'inspect sandbox state' })
      if (!openTypes.has('sandbox')) {
        ui.push({ method: 'ui.panel.open', params: { type: 'sandbox', props: {} } })
      }
      break
    }

    case 'diff': {
      const path = extractPath(input)
      plan.push({ step: 'show diff', reason: 'compare current state with reference' })
      if (!openTypes.has('diff')) {
        execution.push({ id: makeCallId(), method: 'fs.diff', params: { path } })
        ui.push({ method: 'ui.panel.open', params: { type: 'diff', props: { path } } })
      } else {
        ui.push({ method: 'ui.focus.panel', params: { target: { type: 'diff', index: 0 } } })
      }
      break
    }

    case 'test': {
      plan.push(
        { step: 'create checkpoint', reason: 'safe state before running tests' },
        { step: 'run tests', reason: 'verify correctness' },
        { step: 'show event stream', reason: 'surface test output' },
      )
      execution.push({ id: makeCallId(), method: 'state.checkpoint', params: {} })
      execution.push({ id: makeCallId(), method: 'verify.test', params: {} })
      if (!openTypes.has('event_stream')) {
        ui.push({ method: 'ui.panel.open', params: { type: 'event_stream', props: {} } })
      }
      break
    }

    case 'search': {
      const query = extractQuery(input)
      plan.push({ step: `search code: "${query}"`, reason: 'find relevant code locations' })
      execution.push({ id: makeCallId(), method: 'code.search', params: { query } })
      if (!openTypes.has('primitive')) {
        ui.push({ method: 'ui.panel.open', params: { type: 'primitive', props: { title: `Search: ${query}` } } })
      }
      break
    }

    default: {
      plan.push({ step: 'open trace panel', reason: 'default execution visibility' })
      if (!openTypes.has('trace')) {
        ui.push({ method: 'ui.panel.open', params: { type: 'trace', props: {} } })
      }
      break
    }
  }

  return {
    groupId,
    ...(plan.length > 0 ? { plan } : {}),
    ...(execution.length > 0 ? { execution } : {}),
    ...(ui.length > 0 ? { ui } : {}),
  }
}

// ─── Public API ───────────────────────────────────────────────────────────────

/**
 * Dual-path orchestrator:
 *
 *   1. LLM path  — if VITE_ORCHESTRATOR_URL is set, calls the OpenAI-compatible
 *                  endpoint with the canonical system prompt. On any failure,
 *                  falls through to the local path.
 *
 *   2. Local path — enhanced intent-classifier with entity-aware dedup,
 *                   checkpoint-first modification flow, and restore support.
 */
export async function callOrchestratorAI(
  userInput: string,
  context: OrchestratorContext,
): Promise<OrchestratorOutput> {
  // ── Try LLM path ──────────────────────────────────────────────────────────
  const llmResult = await callLLMOrchestrator(userInput, context)
  if (llmResult) return llmResult

  // ── Local fallback ────────────────────────────────────────────────────────
  // Simulate async latency for UX consistency
  await new Promise((r) => setTimeout(r, 200))
  const intent = classifyIntent(userInput)
  return buildLocalOutput(intent, userInput, context)
}

/**
 * @deprecated Backward compat shim — extracts ui[] from orchestrator output.
 */
export async function callUIPrimitiveAI(
  userInput: string,
  currentState: WorkspaceState,
): Promise<UIPrimitive[]> {
  const context = buildOrchestratorContext(currentState, { timelineEntries: [] })
  const output = await callOrchestratorAI(userInput, context)
  return output.ui ?? []
}
