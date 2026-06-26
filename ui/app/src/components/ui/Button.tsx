import type { ButtonHTMLAttributes, AnchorHTMLAttributes } from 'react';
import clsx from 'clsx';

type Variant = 'primary' | 'secondary' | 'ghost' | 'danger';
type Size = 'sm' | 'md' | 'lg';

const variantStyles: Record<Variant, string> = {
  primary:
    'bg-[#219c3f] text-white shadow-sm hover:bg-[#45b739] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#219c3f]/50',
  secondary:
    'border border-gray-300 bg-white text-[#0c1226] hover:border-gray-400 hover:bg-gray-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-gray-300',
  ghost:
    'text-gray-600 hover:bg-gray-100 hover:text-navy-900 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-gray-300 dark:text-gray-300 dark:hover:bg-navy-800 dark:hover:text-white',
  danger:
    'bg-red-600 text-white shadow-sm hover:bg-red-700 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-red-500/50',
};

const sizeStyles: Record<Size, string> = {
  sm: 'px-3 py-1.5 text-xs font-semibold rounded-md',
  md: 'px-4 py-2 text-sm font-semibold rounded-lg',
  lg: 'px-6 py-3 text-base font-bold rounded-xl',
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

export function Button({ variant = 'primary', size = 'md', className, as: tag, ...rest }: ButtonProps) {
  const classes = clsx(
    'inline-flex items-center justify-center gap-2 transition-all duration-150',
    variantStyles[variant],
    sizeStyles[size],
    className,
  );

  if (tag === 'a') {
    return <a className={classes} {...rest} />;
  }

  return <button className={classes} {...rest} />;
}