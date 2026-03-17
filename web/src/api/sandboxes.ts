import { apiFetch } from '@/api/client'
import type { AppPrimitiveManifest } from '@/types/primitive'
import type { Sandbox } from '@/types/sandbox'

interface RawSandbox {
  id: string
  status: 'running' | 'stopped' | 'error' | 'creating' | 'destroyed'
  driver?: 'docker' | 'kubernetes'
  created_at?: number
  expires_at?: number
  config?: {
    mount_source?: string
    lifecycle?: {
      ttl_seconds?: number
    }
  }
}

interface SandboxesResponse {
  sandboxes: RawSandbox[]
}

function normalizeSandbox(raw: RawSandbox): Sandbox {
  const createdAt = raw.created_at ? new Date(raw.created_at * 1000).toISOString() : new Date().toISOString()
  const ttlSeconds = raw.config?.lifecycle?.ttl_seconds ?? Math.max((raw.expires_at ?? 0) - Math.floor(Date.now() / 1000), 0)

  return {
    id: raw.id,
    status: raw.status === 'destroyed' ? 'stopped' : raw.status,
    driver: raw.driver ?? 'docker',
    workspace_root: raw.config?.mount_source ?? '.',
    created_at: createdAt,
    ttl_seconds: ttlSeconds
  }
}

export async function listSandboxes(): Promise<Sandbox[]> {
  const data = await apiFetch<SandboxesResponse>('/api/v1/sandboxes')
  return data.sandboxes.map(normalizeSandbox)
}

export async function getSandbox(id: string): Promise<Sandbox> {
  const data = await apiFetch<RawSandbox>(`/api/v1/sandboxes/${id}`)
  return normalizeSandbox(data)
}

export async function createSandbox(params: {
  driver: 'docker'
  workspace: string
  ttl: number
}): Promise<Sandbox> {
  const data = await apiFetch<RawSandbox>('/sandboxes', {
    method: 'POST',
    body: JSON.stringify({
      driver: params.driver,
      mount_source: params.workspace,
      lifecycle: { ttl_seconds: params.ttl }
    })
  })
  return normalizeSandbox(data)
}

export async function destroySandbox(id: string): Promise<void> {
  await apiFetch<void>(`/sandboxes/${id}`, { method: 'DELETE' })
}

export async function listAppPrimitives(id: string): Promise<AppPrimitiveManifest[]> {
  const data = await apiFetch<{ app_primitives?: AppPrimitiveManifest[]; primitives?: AppPrimitiveManifest[] }>(
    `/api/v1/sandboxes/${id}/app-primitives`
  )
  return data.app_primitives ?? data.primitives ?? []
}
