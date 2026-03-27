import type { WorkspaceEntity, WorkspaceEntityType } from '@/types/workspace'

export interface ResolvedEntityCandidate {
  type: WorkspaceEntityType
  uri: string
  metadata?: Record<string, unknown>
}

export type ExecutionEntityResolver = (
  params: Record<string, unknown>,
  result: unknown,
) => ResolvedEntityCandidate[]

function nowISO(): string {
  return new Date().toISOString()
}

function normalizeURI(input: string): string {
  if (!input) return '/'
  if (input === '.') return '/'
  // Preserve scheme-based URIs (e.g. https://, db:, file://) as-is.
  if (/^[a-zA-Z][a-zA-Z0-9+\-.]*:/.test(input)) return input
  const withSlashes = input.replace(/\\/g, '/')
  const collapsed = withSlashes.replace(/\/{2,}/g, '/')
  const rooted = collapsed.startsWith('/') ? collapsed : `/${collapsed}`
  return rooted.replace(/\/\.\//g, '/').replace(/\/\.$/, '/')
}

function toEntity(
  candidate: ResolvedEntityCandidate,
  sourceExecutionId?: string,
): WorkspaceEntity {
  const uri = normalizeURI(candidate.uri)
  return {
    id: `${candidate.type}:${uri}`,
    type: candidate.type,
    uri,
    metadata: {
      path: uri,
      ...(candidate.metadata ?? {}),
    },
    version: 1,
    lastTouchedAt: nowISO(),
    ...(sourceExecutionId ? { lastSourceExecutionId: sourceExecutionId } : {}),
  }
}

function asRecord(value: unknown): Record<string, unknown> | null {
  return value && typeof value === 'object' ? (value as Record<string, unknown>) : null
}

const resolveFsRead: ExecutionEntityResolver = (params) => {
  const path = typeof params['path'] === 'string' ? params['path'] : null
  if (!path) return []
  return [{ type: 'file', uri: path, metadata: { source: 'fs.read' } }]
}

const resolveFsList: ExecutionEntityResolver = (params, result) => {
  const out: ResolvedEntityCandidate[] = []
  const basePath = typeof params['path'] === 'string' ? params['path'] : '/'
  out.push({ type: 'directory', uri: basePath, metadata: { source: 'fs.list' } })

  const root = asRecord(result)
  const entries = root?.['entries']
  if (!Array.isArray(entries)) return out

  for (const raw of entries) {
    const entry = asRecord(raw)
    if (!entry) continue
    const entryPath = typeof entry['path'] === 'string'
      ? entry['path']
      : typeof entry['name'] === 'string'
        ? `${basePath}/${entry['name']}`
        : null
    if (!entryPath) continue
    const isDir = Boolean(entry['is_dir'])
    out.push({
      type: isDir ? 'directory' : 'file',
      uri: entryPath,
      metadata: {
        source: 'fs.list',
        ...(typeof entry['size'] === 'number' ? { size: entry['size'] } : {}),
      },
    })
  }

  return out
}

const resolveCodeSearch: ExecutionEntityResolver = (_params, result) => {
  const root = asRecord(result)
  const matches = root?.['matches']
  if (!Array.isArray(matches)) return []
  const out: ResolvedEntityCandidate[] = []
  for (const raw of matches) {
    const match = asRecord(raw)
    if (!match) continue
    const file = typeof match['file'] === 'string' ? match['file'] : null
    if (!file) continue
    out.push({
      type: 'file',
      uri: file,
      metadata: {
        source: 'code.search',
        ...(typeof match['line'] === 'number' ? { line: match['line'] } : {}),
      },
    })
  }
  return out
}

const resolveDbQuery: ExecutionEntityResolver = (params) => {
  const conn = asRecord(params['connection'])
  const query = typeof params['query'] === 'string' ? params['query'] : ''
  const dsn = conn
    ? (typeof conn['dsn'] === 'string' ? conn['dsn'] : typeof conn['path'] === 'string' ? conn['path'] : '')
    : ''
  const uri = dsn ? `db:${dsn}` : 'db:unknown'
  return [{ type: 'database_table', uri, metadata: { query, connection: conn ?? {} } }]
}

const resolveBrowserGoto: ExecutionEntityResolver = (params, result) => {
  const url = typeof params['url'] === 'string' ? params['url'] : ''
  if (!url) return []
  const root = asRecord(result)
  const title = typeof root?.['title'] === 'string' ? root['title'] : undefined
  return [{
    type: 'web_page',
    uri: url,
    metadata: { url, ...(title ? { title } : {}) },
  }]
}

export const executionEntityRegistry: Record<string, ExecutionEntityResolver> = {
  'fs.read': resolveFsRead,
  'fs.list': resolveFsList,
  'code.search': resolveCodeSearch,
  'db.query': resolveDbQuery,
  'db.query_readonly': resolveDbQuery,
  'browser.goto': resolveBrowserGoto,
}

export function registerExecutionEntityResolver(
  method: string,
  resolver: ExecutionEntityResolver,
): void {
  executionEntityRegistry[method] = resolver
}

export function resolveExecutionEntities(
  method: string,
  params: Record<string, unknown>,
  result: unknown,
  sourceExecutionId?: string,
): WorkspaceEntity[] {
  const resolver = executionEntityRegistry[method]
  if (!resolver) return []
  const candidates = resolver(params, result)
  const dedup = new Map<string, WorkspaceEntity>()
  for (const candidate of candidates) {
    const entity = toEntity(candidate, sourceExecutionId)
    dedup.set(entity.id, entity)
  }
  return Array.from(dedup.values())
}
