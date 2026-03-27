import { beforeEach, describe, expect, it } from 'vitest'
import { callOrchestratorAI, type OrchestratorContext } from '@/api/uiPrimitives'
import { usePrimitiveStore } from '@/store/primitiveStore'
import type { PrimitiveSchema } from '@/types/primitive'

const BASE_CONTEXT: OrchestratorContext = {
  uiState: { panelCount: 0, openTypes: [] },
  openEntities: [],
  activeWorkspaceEntities: [],
  timelineSummary: [],
}

describe('callOrchestratorAI local fallback', () => {
  beforeEach(() => {
    usePrimitiveStore.getState().reset()
  })

  it('falls back to a draft panel when email.send is unavailable', async () => {
    const output = await callOrchestratorAI('send an email to alice@example.com about the release', BASE_CONTEXT, {
      forceLocal: true,
    })

    expect(output.execution).toBeUndefined()
    expect(output.ui).toHaveLength(1)
    expect(output.ui?.[0]).toMatchObject({
      method: 'ui.panel.open',
      params: {
        type: 'primitive',
        props: {
          method: 'email.draft',
          uiLayoutHint: 'markdown',
        },
      },
    })
  })

  it('keeps emitting email.send when the primitive is available', async () => {
    const emailPrimitive: PrimitiveSchema = {
      name: 'email.send',
      description: 'Send email',
      kind: 'app',
      input_schema: {},
      output_schema: {},
      intent: {
        category: 'mutation',
        side_effect: 'external',
        reversible: false,
        risk_level: 'high',
      },
    }
    usePrimitiveStore.setState({
      status: 'ready',
      error: null,
      primitives: [emailPrimitive],
      primitivesByName: { 'email.send': emailPrimitive },
    })

    const output = await callOrchestratorAI('send an email to alice@example.com about the release', BASE_CONTEXT, {
      forceLocal: true,
    })

    expect(output.execution).toHaveLength(1)
    expect(output.execution?.[0]).toMatchObject({
      method: 'email.send',
      params: {
        to: 'alice@example.com',
        subject: 'Draft from PrimitiveBox',
      },
    })
  })
})
