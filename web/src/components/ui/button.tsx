import * as React from 'react'
import { cva, type VariantProps } from 'class-variance-authority'
import { cn } from '@/lib/utils'

const buttonVariants = cva(
  'inline-flex items-center justify-center rounded-md border text-sm font-medium transition-colors duration-[120ms] disabled:pointer-events-none disabled:opacity-50',
  {
    variants: {
      variant: {
        default: 'border-[var(--border-strong)] bg-[var(--bg-surface)] text-[var(--text-primary)] hover:bg-[var(--bg-subtle)]',
        ghost: 'border-transparent bg-transparent text-[var(--text-secondary)] hover:bg-[var(--bg-subtle)]',
        subtle: 'border-[var(--border)] bg-[var(--bg-subtle)] text-[var(--text-primary)] hover:bg-[color-mix(in_srgb,var(--bg-subtle)_85%,white)]'
      },
      size: {
        default: 'h-9 px-3 py-2',
        sm: 'h-8 px-2.5',
        lg: 'h-10 px-4'
      }
    },
    defaultVariants: {
      variant: 'default',
      size: 'default'
    }
  }
)

export interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement>, VariantProps<typeof buttonVariants> {}

export const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(({ className, variant, size, ...props }, ref) => {
  return <button ref={ref} className={cn(buttonVariants({ variant, size }), className)} {...props} />
})

Button.displayName = 'Button'
