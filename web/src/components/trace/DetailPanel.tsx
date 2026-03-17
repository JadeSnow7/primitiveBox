import { Badge } from '@/components/shared/Badge'
import { JsonView } from '@/components/shared/JsonView'
import { useTraceStore } from '@/store/traceStore'

export function DetailPanel() {
  const events = useTraceStore((s) => s.events)
  const selectedId = useTraceStore((s) => s.selectedId)
  const event = events.find((e) => e.id === selectedId)

  if (!event) {
    return <div className="flex h-full items-center justify-center px-6 text-center text-sm text-[var(--text-muted)]">选择一条 trace 查看详情</div>
  }

  const intentVariant =
    event.intent_snapshot?.category === 'rollback'
      ? 'rollback'
      : event.intent_snapshot?.category === 'verification'
        ? 'checkpoint'
        : event.intent_snapshot?.category === 'mutation'
          ? 'failed'
          : 'neutral'

  const timestamp = new Date(event.timestamp)
  const timeValue = Number.isNaN(timestamp.getTime())
    ? event.timestamp
    : `${timestamp.toLocaleTimeString('zh-CN', { hour12: false })}.${String(timestamp.getMilliseconds()).padStart(3, '0')}`

  return (
    <div className="h-full overflow-y-auto p-4 text-[13px]">
      <div className="space-y-4">
        <Section title="EVENT">
          <KV k="primitive" v={event.primitive_id} mono />
          <KV
            k="time"
            v={timeValue}
            mono
          />
          <KV k="duration" v={`${event.duration_ms}ms`} mono />
          <KV k="attempt" v={String(event.attempt)} mono />
          <KV k="trace" v={event.trace_id} mono />
        </Section>

        {event.intent_snapshot ? (
          <Section title="INTENT">
            <div className="flex items-center justify-between py-1">
              <span className="text-[var(--text-muted)]">category</span>
              <Badge variant={intentVariant}>{event.intent_snapshot.category}</Badge>
            </div>
            <KV k="reversible" v={String(event.intent_snapshot.reversible)} color={event.intent_snapshot.reversible ? 'green' : 'red'} />
            <KV
              k="risk"
              v={event.intent_snapshot.risk_level}
              color={event.intent_snapshot.risk_level === 'high' ? 'red' : event.intent_snapshot.risk_level === 'medium' ? 'amber' : undefined}
            />
          </Section>
        ) : null}

        <Section title="CVR OUTCOME">
          <KV
            k="layer_a"
            v={event.layer_a_outcome}
            color={event.layer_a_outcome === 'checkpoint_created' ? 'green' : event.layer_a_outcome === 'failed' ? 'red' : undefined}
          />
          {event.checkpoint_id ? <KV k="checkpoint" v={event.checkpoint_id} mono color="blue" /> : null}
          {event.strategy_name ? <KV k="strategy" v={event.strategy_name} /> : null}
          <KV k="verify" v={event.strategy_outcome} color={event.strategy_outcome === 'passed' ? 'green' : 'red'} />
          {event.recovery_path ? (
            <KV k="recovery" v={event.recovery_path} color={event.recovery_path === 'rollback' ? 'red' : event.recovery_path === 'retry' ? 'amber' : undefined} />
          ) : null}
          {event.cvr_depth_exceeded ? <KV k="depth_exceeded" v="true" color="red" /> : null}
        </Section>

        {event.affected_scopes?.length > 0 ? (
          <Section title="AFFECTED SCOPES">
            <div className="mt-1 flex flex-wrap gap-1">
              {event.affected_scopes.map((scope) => (
                <span key={scope} className="rounded bg-[var(--teal-bg)] px-1.5 py-0.5 font-mono text-[11px] text-[var(--teal)]">
                  {scope}
                </span>
              ))}
            </div>
          </Section>
        ) : null}

        <Section title="RAW SNAPSHOT">
          <JsonView data={event} />
        </Section>
      </div>
    </div>
  )
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="mb-2 text-[11px] font-medium tracking-[0.22em] text-[var(--text-muted)]">{title}</div>
      <div className="divide-y divide-[var(--border)]">{children}</div>
    </div>
  )
}

function KV({
  k,
  v,
  mono,
  color
}: {
  k: string
  v: string
  mono?: boolean
  color?: 'green' | 'red' | 'blue' | 'amber'
}) {
  const colorClassMap = {
    green: 'text-[var(--green)]',
    red: 'text-[var(--red)]',
    blue: 'text-[var(--blue)]',
    amber: 'text-[var(--amber)]'
  }
  const colorClass = color ? colorClassMap[color] : 'text-[var(--text-primary)]'

  return (
    <div className="flex items-center justify-between gap-4 py-1.5">
      <span className="flex-shrink-0 text-[var(--text-muted)]">{k}</span>
      <span className={`${mono ? 'font-mono' : ''} ${colorClass} truncate text-right text-[12px]`}>{v}</span>
    </div>
  )
}
