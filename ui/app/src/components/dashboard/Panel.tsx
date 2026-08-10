import type { ReactNode } from 'react';
import clsx from 'clsx';

/**
 * Standard content surface for dashboard sections.
 * Quiet border, soft shadow, clear title + optional description/actions.
 */
export function Panel({
  title,
  description,
  actions,
  children,
  id,
  className,
  flush = false,
}: {
  title?: string;
  description?: string;
  actions?: ReactNode;
  children: ReactNode;
  id?: string;
  className?: string;
  /** Drop default body top margin when composing custom headers. */
  flush?: boolean;
}) {
  const hasHeader = Boolean(title || description || actions);

  return (
    <section
      id={id}
      className={clsx(
        'rounded-xl border border-muted bg-surface p-4 sm:p-5',
        'shadow-[var(--shadow-soft)]',
        className
      )}
    >
      {hasHeader && (
        <header
          className={clsx(
            'flex flex-wrap items-start justify-between gap-3',
            !flush && children ? 'mb-4 border-b border-muted pb-4' : ''
          )}
        >
          <div className="min-w-0 flex-1">
            {title && <h2 className="text-base font-semibold tracking-tight text-main">{title}</h2>}
            {description && (
              <p className="mt-1 max-w-2xl text-sm leading-relaxed text-secondary">{description}</p>
            )}
          </div>
          {actions && <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div>}
        </header>
      )}
      <div className={clsx(hasHeader && !flush ? '' : '')}>{children}</div>
    </section>
  );
}
