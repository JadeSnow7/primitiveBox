import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

type BadgeVariant =
  | 'passed'
  | 'failed'
  | 'rollback'
  | 'retry'
  | 'escalate'
  | 'checkpoint'
  | 'skipped'
  | 'running'
  | 'stopped'
  | 'neutral'
  | 'warning'

const variantStyles: Record<BadgeVariant, string> = {
  passed: 'bg-[var(--green-bg)] text-[var(--green)]',
  failed: 'bg-[var(--red-bg)] text-[var(--red)]',
  rollback: 'bg-[var(--red-bg)] text-[var(--red)]',
  retry: 'bg-[var(--amber-bg)] text-[var(--amber)]',
  escalate: 'bg-[var(--amber-bg)] text-[var(--amber)]',
  checkpoint: 'bg-[var(--blue-bg)] text-[var(--blue)]',
  skipped: 'bg-[var(--bg-subtle)] text-[var(--text-muted)]',
  running: 'bg-[var(--green-bg)] text-[var(--green)]',
  stopped: 'bg-[var(--bg-subtle)] text-[var(--text-muted)]',
  neutral: 'bg-[var(--bg-subtle)] text-[var(--text-secondary)]',
  warning: 'bg-[var(--amber-bg)] text-[var(--amber)]'
}

export function Badge({ variant, children, className }: { variant: BadgeVariant; children: ReactNode; className?: string }) {
  return (
    <span
      className={cn(
        'inline-flex items-center rounded px-2 py-0.5 font-mono text-[11px] font-medium',
        variantStyles[variant],
        className
      )}
    >
      {children}
    </span>
  )
}
