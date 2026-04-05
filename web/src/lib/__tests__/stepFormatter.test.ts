import { describe, it, expect } from 'vitest'
import { formatStepLabel } from '@/lib/stepFormatter'
import type { GoalStep } from '@/types/goal'

function makeStep(overrides: Partial<GoalStep>): GoalStep {
  return {
    id: 's1',
    goal_id: 'g1',
    primitive: 'fs.read',
    input: {},
    status: 'pending',
    seq: 0,
    created_at: 1,
    updated_at: 1,
    ...overrides,
  }
}

describe('formatStepLabel', () => {
  it('maps fs.read with path param to Chinese label', () => {
    const step = makeStep({ primitive: 'fs.read', input: { path: '/data/sales.csv' } })
    expect(formatStepLabel(step)).toBe('读取文件 /data/sales.csv')
  })

  it('maps fs.write with path param', () => {
    const step = makeStep({ primitive: 'fs.write', input: { path: '/out/report.pdf' } })
    expect(formatStepLabel(step)).toBe('写入文件 /out/report.pdf')
  })

  it('maps shell.exec with command param', () => {
    const step = makeStep({ primitive: 'shell.exec', input: { command: 'npm test' } })
    expect(formatStepLabel(step)).toBe('执行命令 npm test')
  })

  it('maps http.fetch with url param', () => {
    const step = makeStep({ primitive: 'http.fetch', input: { url: 'https://api.example.com' } })
    expect(formatStepLabel(step)).toBe('请求网络 https://api.example.com')
  })

  it('uses primitive name as fallback for unknown primitives', () => {
    const step = makeStep({ primitive: 'custom.op', input: {} })
    expect(formatStepLabel(step)).toBe('custom.op')
  })

  it('omits param when key param is missing from input', () => {
    const step = makeStep({ primitive: 'fs.read', input: {} })
    expect(formatStepLabel(step)).toBe('读取文件')
  })

  it('maps fs.list with path param', () => {
    const step = makeStep({ primitive: 'fs.list', input: { path: '/src' } })
    expect(formatStepLabel(step)).toBe('列出目录 /src')
  })

  it('maps fs.delete with path param', () => {
    const step = makeStep({ primitive: 'fs.delete', input: { path: '/tmp/old.txt' } })
    expect(formatStepLabel(step)).toBe('删除文件 /tmp/old.txt')
  })
})
