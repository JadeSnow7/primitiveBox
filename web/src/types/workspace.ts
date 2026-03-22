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
}

// ─── Panel types ────────────────────────────────────────────────────────────

export type PanelType =
  | 'trace'
  | 'event_stream'
  | 'sandbox'
  | 'checkpoint'
  | 'diff'
  | 'primitive'

export interface WorkspacePanel {
  id: string
  type: PanelType
  props: Record<string, unknown>
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
  | { method: 'ui.panel.open';   params: { type: PanelType; props?: Record<string, unknown>; target?: SemanticRef } }
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
