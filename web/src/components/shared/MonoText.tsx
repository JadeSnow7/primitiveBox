import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

export function MonoText({ children, className }: { children: ReactNode; className?: string }) {
  return <span className={cn('font-mono text-[12px] text-[var(--text-mono)]', className)}>{children}</span>
}
