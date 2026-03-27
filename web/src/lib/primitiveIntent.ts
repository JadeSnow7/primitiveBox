import { usePrimitiveStore } from '@/store/primitiveStore'
import type { PrimitiveIntent } from '@/types/workspace'

export class PrimitiveCatalogUnavailableError extends Error {
  constructor(message: string) {
    super(message)
    this.name = 'PrimitiveCatalogUnavailableError'
  }
}

export function resolvePrimitiveIntent(method: string): PrimitiveIntent {
  const { status, error, getPrimitive } = usePrimitiveStore.getState()

  if (status !== 'ready') {
    throw new PrimitiveCatalogUnavailableError(
      error
        ? `Primitive catalog unavailable: ${error}`
        : 'Primitive catalog unavailable: manifest has not been hydrated yet.',
    )
  }

  const primitive = getPrimitive(method)
  if (!primitive) {
    throw new PrimitiveCatalogUnavailableError(
      `Primitive catalog unavailable: missing manifest entry for "${method}".`,
    )
  }

  return primitive.intent
}

export function requiresHumanReview(method: string): boolean {
  const intent = resolvePrimitiveIntent(method)
  return intent.risk_level === 'high' || intent.reversible === false
}
