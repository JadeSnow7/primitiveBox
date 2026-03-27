import { describe, expect, it } from 'vitest'
import { resolveExecutionEntities } from '@/lib/entityTracker'

describe('entityTracker', () => {
  it('resolves fs.read into a file entity with normalized path metadata', () => {
    const entities = resolveExecutionEntities(
      'fs.read',
      { path: 'calc.go' },
      { content: 'package main' },
      'call-1',
    )

    expect(entities).toHaveLength(1)
    expect(entities[0].type).toBe('file')
    expect(entities[0].uri).toBe('/calc.go')
    expect(entities[0].metadata['path']).toBe('/calc.go')
    expect(entities[0].lastSourceExecutionId).toBe('call-1')
  })

  it('resolves fs.list entries into directory/file entities', () => {
    const entities = resolveExecutionEntities(
      'fs.list',
      { path: '.' },
      {
        entries: [
          { path: 'src', is_dir: true, size: 0 },
          { path: 'src/main.go', is_dir: false, size: 12 },
        ],
      },
      'call-2',
    )

    expect(entities.map((e) => e.id)).toContain('directory:/')
    expect(entities.map((e) => e.id)).toContain('directory:/src')
    expect(entities.map((e) => e.id)).toContain('file:/src/main.go')
  })

  it('resolves code.search matches into deduplicated file entities', () => {
    const entities = resolveExecutionEntities(
      'code.search',
      { query: 'TODO' },
      {
        matches: [
          { file: 'a.go', line: 1, content: 'TODO: x' },
          { file: 'a.go', line: 9, content: 'TODO: y' },
          { file: 'b.go', line: 2, content: 'TODO: z' },
        ],
      },
      'call-3',
    )

    expect(entities).toHaveLength(2)
    expect(entities.map((e) => e.id).sort()).toEqual(['file:/a.go', 'file:/b.go'])
  })

  it('resolves db.query into a database_table entity with query metadata', () => {
    const entities = resolveExecutionEntities(
      'db.query',
      {
        connection: { dialect: 'sqlite', path: 'app.db' },
        query: 'SELECT id, name FROM widgets',
      },
      [{ id: 1, name: 'alpha' }],
      'call-4',
    )

    expect(entities).toHaveLength(1)
    expect(entities[0].type).toBe('database_table')
    expect(entities[0].uri).toBe('db:app.db')
    expect(entities[0].id).toBe('database_table:db:app.db')
    expect(entities[0].metadata['query']).toBe('SELECT id, name FROM widgets')
    expect(entities[0].lastSourceExecutionId).toBe('call-4')
  })

  it('resolves browser.goto into a web_page entity with url metadata', () => {
    const entities = resolveExecutionEntities(
      'browser.goto',
      { url: 'https://example.com', timeout_s: 30 },
      { title: 'Example Domain', url: 'https://example.com', markdown: '# Example' },
      'call-5',
    )

    expect(entities).toHaveLength(1)
    expect(entities[0].type).toBe('web_page')
    expect(entities[0].uri).toBe('https://example.com')
    expect(entities[0].id).toBe('web_page:https://example.com')
    expect(entities[0].metadata['url']).toBe('https://example.com')
    expect(entities[0].metadata['title']).toBe('Example Domain')
    expect(entities[0].lastSourceExecutionId).toBe('call-5')
  })

  it('resolves db.query_readonly the same as db.query', () => {
    const entities = resolveExecutionEntities(
      'db.query_readonly',
      { connection: { dialect: 'postgres', dsn: 'postgres://localhost/mydb' }, query: 'SELECT 1' },
      [],
    )

    expect(entities).toHaveLength(1)
    expect(entities[0].type).toBe('database_table')
    expect(entities[0].uri).toBe('db:postgres://localhost/mydb')
  })

  it('returns empty for browser.goto with missing url', () => {
    const entities = resolveExecutionEntities('browser.goto', {}, null)
    expect(entities).toHaveLength(0)
  })
})
