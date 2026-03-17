export type LayerAOutcome = 'checkpoint_created' | 'skipped' | 'failed'
export type VerifyOutcome = 'passed' | 'failed' | 'skipped' | 'timeout' | 'error'
export type RecoveryAction = 'retry' | 'rollback' | 'rewrite' | 'skip' | 'escalate' | 'abort'
export type IntentCategory = 'mutation' | 'query' | 'verification' | 'rollback'
export type RiskLevel = 'low' | 'medium' | 'high'

export interface IntentSnapshot {
  category: IntentCategory
  reversible: boolean
  risk_level: RiskLevel
  affected_scopes: string[]
}

export interface TraceEvent {
  id: string
  sandbox_id: string
  trace_id: string
  primitive_id: string
  timestamp: string
  duration_ms: number
  attempt: number
  checkpoint_id: string
  layer_a_outcome: LayerAOutcome
  strategy_name: string
  strategy_outcome: VerifyOutcome
  recovery_path: RecoveryAction | ''
  cvr_depth_exceeded: boolean
  intent_snapshot: IntentSnapshot | null
  affected_scopes: string[]
}

export interface TraceMetrics {
  total: number
  checkpoints: number
  rollbacks: number
  failures: number
}
