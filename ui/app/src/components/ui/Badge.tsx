import type { HTMLAttributes } from 'react';
import clsx from 'clsx';

type BadgeVariant = 'success' | 'warning' | 'info' | 'neutral';

const variantStyles: Record<BadgeVariant, string> = {
  success: 'bg-[#219c3f]/15 text-[#219c3f]',
  warning: 'bg-amber-100 text-amber-800',
  info: 'bg-blue-100 text-blue-700',
  neutral: 'bg-gray-100 text-gray-700 dark:bg-navy-800 dark:text-gray-300',
};

interface BadgeProps extends HTMLAttributes<HTMLSpanElement> {
  variant?: BadgeVariant;
}

export function Badge({ variant = 'neutral', className, children, ...rest }: BadgeProps) {
  return (
    <span
      className={clsx(
        'inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-semibold',
        variantStyles[variant],
        className,
      )}
      {...rest}
    >
      {children}
    </span>
  );
}
