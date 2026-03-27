import { describe, expect, it } from 'vitest'
import { retainExecutionPayload } from '@/lib/resultRetention'

describe('resultRetention', () => {
  it('truncates oversized strings and strips script/style blocks', () => {
    const payload = retainExecutionPayload({
      html: `<script>alert("x")</script><style>body{display:none}</style>${'a'.repeat(5000)}`,
    }) as Record<string, unknown>

    const html = String(payload.html)
    expect(html).not.toContain('<script')
    expect(html).not.toContain('<style')
    expect(html.length).toBeLessThan(4200)
    expect(html).toContain('[truncated')
  })
})
