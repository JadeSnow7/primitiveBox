export type SandboxStatus = 'running' | 'stopped' | 'error' | 'creating'

export interface Sandbox {
  id: string
  status: SandboxStatus
  driver: 'docker' | 'kubernetes'
  workspace_root: string
  created_at: string
  ttl_seconds: number
}
