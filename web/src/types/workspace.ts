// ─── Execution primitives ───────────────────────────────────────────────────

/** Allowlisted execution methods the orchestrator may emit */
export const EXECUTION_METHODS = [
  'fs.read',
  'fs.write',
  'fs.list',
  'fs.diff',
  'shell.exec',
  'state.checkpoint',
  'state.restore',
  'verify.test',
  'code.search',
  'db.query',
  'db.execute',
  'browser.goto',
  'browser.read',
  'email.send',
  'demo.irrevocable_action',
  'demo.tabular_data',
] as const
export type ExecutionMethod = typeof EXECUTION_METHODS[number]

export interface ExecutionCall {
  /** Correlation id — links timeline call ↔ result entries */
  id: string
  method: ExecutionMethod
  params: Record<string, unknown>
}

/** One reasoning step in the orchestrator plan */
export interface PlanStep {
  step: string    // short action label, e.g. "read file"
  reason: string  // why this step is needed
}

/** Structured output from the AI orchestrator. */
export interface OrchestratorOutput {
  /** One groupId per orchestrator invocation — all timeline entries share it */
  groupId: string
  /** Reasoning trace: what the orchestrator intends to do and why */
  plan?: PlanStep[]
  execution?: ExecutionCall[]
  ui?: UIPrimitive[]
  /**
   * Agent loop signal.
   * - 'continue' → the agent loop should fire another iteration
   * - 'done'     → goal is achieved, stop the loop
   * - undefined  → one-shot mode (no loop)
   */
  status?: 'continue' | 'done'
  /**
   * Confidence score (0.0–1.0).
   * - 1.0 = fully verified result
   * - 0.7 = likely correct
   * - <0.5 = uncertain → agent loop should continue even if status is 'done'
   */
  confidence?: number
}

/**
 * Output of the Verification Agent.
 * Produced after the Executor reports 'done' to independently validate the goal.
 */
export interface VerificationResult {
  /** Whether the goal is confirmed achieved with concrete evidence. */
  verified: boolean
  /** Confidence score 0.0–1.0 in the verification assessment. */
  confidence: number
  /** Human-readable explanation of the verdict. */
  reason: string
  /** Missing steps if not verified; empty array when verified. */
  missing: string[]
  /** Concrete next actions the planner/executor should take. */
  recommendedNext: string[]
}

export type PrimitiveRiskLevel = 'low' | 'medium' | 'high'

export interface PrimitiveIntent {
  category: string
  reversible: boolean
  risk_level: PrimitiveRiskLevel
  side_effect: string
}

// ─── Panel types ────────────────────────────────────────────────────────────

export type PanelType =
  | 'trace'
  | 'event_stream'
  | 'sandbox'
  | 'checkpoint'
  | 'diff'
  | 'primitive'

export type WorkspaceEntityType =
  | 'file'
  | 'directory'
  | 'process'
  | 'database_table'
  | 'web_page'
  | 'unknown'

export interface WorkspaceEntity {
  id: string
  type: WorkspaceEntityType
  uri: string
  metadata: Record<string, unknown>
  /**
   * Monotonic version used for staleness checks across bound panels.
   * If one panel updates an entity, other panels with an older snapshot can
   * detect stale state.
   */
  version: number
  lastTouchedAt: string
  lastSourceExecutionId?: string
}

export interface WorkspacePanel {
  id: string
  type: PanelType
  props: Record<string, unknown>
  entityId?: string
  entityIds?: string[]
  entityVersionSnapshot?: number
}

// ─── Layout tree ─────────────────────────────────────────────────────────────

export type LayoutNode =
  | { type: 'panel'; panelId: string }
  | { type: 'split'; direction: 'horizontal' | 'vertical'; children: [LayoutNode, LayoutNode] }
  | { type: 'tabs'; panels: string[]; active: string }
  | { type: 'empty' }  // root before any panel is opened

// ─── UI Primitives (raw shape before Zod) ───────────────────────────────────

export interface SemanticRef {
  type: PanelType
  index?: number
}

export type UIPrimitive =
  | { method: 'ui.panel.open';   params: { type: PanelType; props?: Record<string, unknown>; target?: SemanticRef; entityId?: string; entityIds?: string[] } }
  | { method: 'ui.panel.close';  params: { target: SemanticRef } }
  | { method: 'ui.layout.split'; params: { target: SemanticRef; direction: 'horizontal' | 'vertical' } }
  | { method: 'ui.focus.panel';  params: { target: SemanticRef } }

// ─── UI Events ───────────────────────────────────────────────────────────────

export type UIEventType =
  | 'ui.panel.opened'
  | 'ui.panel.closed'
  | 'ui.layout.changed'
  | 'ui.focus.changed'
  | 'ui.primitive.rejected'

export interface UIEvent {
  id: string
  timestamp: string
  type: UIEventType
  payload: Record<string, unknown>
}
