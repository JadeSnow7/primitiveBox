export type GoalStatus = 'created' | 'executing' | 'verifying' | 'completed' | 'failed' | 'paused'

export type GoalStepStatus = 'pending' | 'running' | 'passed' | 'failed' | 'skipped' | 'awaiting_review' | 'rolled_back'

export type GoalVerificationStatus = 'pending' | 'running' | 'passed' | 'failed'

export type GoalBindingType = 'service_endpoint' | 'network_exposure' | 'credential'

export type GoalBindingStatus = 'pending' | 'resolved' | 'failed'

export type GoalReviewStatus = 'pending' | 'approved' | 'rejected'

export interface GoalReview {
  id: string
  goal_id: string
  step_id: string
  status: GoalReviewStatus
  primitive: string
  risk_level: string
  reversible: boolean
  side_effect?: string
  decision_reason?: string
  created_at: number
  updated_at: number
}

export interface GoalStep {
  id: string
  goal_id: string
  primitive: string
  input: Record<string, unknown>
  output?: Record<string, unknown>
  status: GoalStepStatus
  checkpoint_id?: string
  seq: number
  risk_level?: string
  reversible?: boolean
  created_at: number
  updated_at: number
}

export interface GoalVerification {
  id: string
  goal_id: string
  step_id?: string
  status: GoalVerificationStatus
  verdict?: string
  evidence?: Record<string, unknown>
  check_type?: string
  check_params?: Record<string, unknown>
  created_at: number
  updated_at: number
}

export interface GoalBinding {
  id: string
  goal_id: string
  binding_type: GoalBindingType
  source_ref: string
  target_ref: string
  status: GoalBindingStatus
  resolved_value?: string
  failure_reason?: string
  metadata?: Record<string, unknown>
  created_at: number
  updated_at: number
}

export interface Goal {
  id: string
  description: string
  status: GoalStatus
  packages: string[]
  sandbox_ids: string[]
  steps: GoalStep[]
  verifications: GoalVerification[]
  bindings?: GoalBinding[]
  reviews?: GoalReview[]
  created_at: number
  updated_at: number
}

export interface GoalReplayEntry {
  seq: number
  step_id: string
  primitive: string
  input: Record<string, unknown>
  output?: Record<string, unknown>
  status: GoalStepStatus
  checkpoint_id?: string
  skipped: boolean
}

export interface GoalReplayResult {
  goal_id: string
  mode: 'full' | 'skip_passed' | 'step_by_step'
  entries: GoalReplayEntry[]
}
