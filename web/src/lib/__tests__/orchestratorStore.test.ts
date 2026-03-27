import { beforeEach, describe, expect, it } from 'vitest'
import { useOrchestratorStore } from '@/store/orchestratorStore'

describe('orchestratorStore', () => {
  beforeEach(() => {
    useOrchestratorStore.getState().reset()
  })

  it('rejects an abandoned pending review on reset', async () => {
    const promise = useOrchestratorStore.getState().requestReview({
      groupId: 'g1',
      correlationId: 'c1',
      method: 'shell.exec',
      params: { command: 'echo hi' },
      intent: { category: 'mutation', reversible: false, risk_level: 'high', side_effect: 'exec' },
    })

    useOrchestratorStore.getState().reset()

    await expect(promise).resolves.toBe('rejected')
    expect(useOrchestratorStore.getState().phase).toBe('IDLE')
  })

  it('rejects a superseded pending review before replacing it', async () => {
    const first = useOrchestratorStore.getState().requestReview({
      groupId: 'g1',
      correlationId: 'c1',
      method: 'shell.exec',
      params: { command: 'echo hi' },
      intent: { category: 'mutation', reversible: false, risk_level: 'high', side_effect: 'exec' },
    })

    const second = useOrchestratorStore.getState().requestReview({
      groupId: 'g2',
      correlationId: 'c2',
      method: 'db.execute',
      params: { sql: 'delete from users' },
      intent: { category: 'mutation', reversible: false, risk_level: 'high', side_effect: 'write' },
    })

    await expect(first).resolves.toBe('rejected')
    useOrchestratorStore.getState().approvePendingReview()
    await expect(second).resolves.toBe('approved')
  })
})
