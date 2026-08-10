import type { ButtonHTMLAttributes, AnchorHTMLAttributes } from 'react';
import clsx from 'clsx';

type Variant = 'primary' | 'secondary' | 'ghost' | 'danger';
type Size = 'sm' | 'md' | 'lg';

const variantStyles: Record<Variant, string> = {
  primary:
    'bg-accent-600 text-white shadow-sm hover:bg-accent-500 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40 focus-visible:ring-offset-2 focus-visible:ring-offset-page disabled:bg-accent-600/50',
  secondary:
    'border border-muted-strong bg-surface text-main hover:border-accent-500/40 hover:bg-surface-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40 focus-visible:ring-offset-2 focus-visible:ring-offset-page',
  ghost:
    'text-secondary hover:bg-surface-hover hover:text-main focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40 focus-visible:ring-offset-2 focus-visible:ring-offset-page',
  danger:
    'bg-red-600 text-white shadow-sm hover:bg-red-700 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-red-500/40 focus-visible:ring-offset-2 focus-visible:ring-offset-page',
};

const sizeStyles: Record<Size, string> = {
  sm: 'min-h-[36px] px-3 py-1.5 text-xs font-semibold rounded-md',
  md: 'min-h-[44px] px-4 py-2 text-sm font-semibold rounded-lg',
  lg: 'min-h-[48px] px-6 py-3 text-sm font-semibold rounded-lg',
};

interface ButtonBaseProps {
  variant?: Variant;
  size?: Size;
  className?: string;
  as?: 'button' | 'a';
}

type ButtonProps = ButtonBaseProps &
  ButtonHTMLAttributes<HTMLButtonElement> &
  AnchorHTMLAttributes<HTMLAnchorElement>;

export function Button({
  variant = 'primary',
  size = 'md',
  className,
  as: tag,
  ...rest
}: ButtonProps) {
  const classes = clsx(
    'inline-flex items-center justify-center gap-2 transition-colors duration-150 disabled:cursor-not-allowed disabled:opacity-60',
    variantStyles[variant],
    sizeStyles[size],
    className
  );

  if (tag === 'a') {
    return <a className={classes} {...rest} />;
  }

  return <button className={classes} {...rest} />;
}
