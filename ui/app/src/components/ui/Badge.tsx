import type { HTMLAttributes } from 'react';
import clsx from 'clsx';

type BadgeVariant = 'success' | 'warning' | 'info' | 'neutral' | 'accent';

const variantStyles: Record<BadgeVariant, string> = {
  success:
    'bg-emerald-50 text-emerald-800 ring-emerald-600/15 dark:bg-emerald-950/40 dark:text-emerald-200 dark:ring-emerald-500/25',
  warning:
    'bg-amber-50 text-amber-900 ring-amber-600/15 dark:bg-amber-950/40 dark:text-amber-100 dark:ring-amber-500/25',
  info: 'bg-sky-50 text-sky-900 ring-sky-600/15 dark:bg-sky-950/40 dark:text-sky-100 dark:ring-sky-500/25',
  neutral: 'bg-surface-muted text-secondary ring-muted dark:bg-surface-hover dark:text-secondary',
  accent:
    'bg-accent-500/10 text-accent-700 ring-accent-500/20 dark:text-accent-300 dark:ring-accent-400/25',
};

interface BadgeProps extends HTMLAttributes<HTMLSpanElement> {
  variant?: BadgeVariant;
}

export function Badge({ variant = 'neutral', className, children, ...rest }: BadgeProps) {
  return (
    <span
      className={clsx(
        'inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset',
        variantStyles[variant],
        className
      )}
      {...rest}
    >
      {children}
    </span>
  );
}
