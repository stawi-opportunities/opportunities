/**
 * Offset-based list pagination with First / Prev / numbered pages / Next / Last.
 * Designed for catalog browse (All jobs) where total is known.
 */

import clsx from 'clsx';

export interface ListPaginationProps {
  /** 1-based page index. */
  page: number;
  pageSize: number;
  total: number;
  onPageChange: (page: number) => void;
  className?: string;
  /** Max numbered buttons to show (default 5). */
  maxButtons?: number;
}

export function ListPagination({
  page,
  pageSize,
  total,
  onPageChange,
  className,
  maxButtons = 5,
}: ListPaginationProps) {
  const totalPages = Math.max(1, Math.ceil(total / pageSize));
  const safePage = Math.min(Math.max(1, page), totalPages);
  const from = total === 0 ? 0 : (safePage - 1) * pageSize + 1;
  const to = Math.min(safePage * pageSize, total);

  const pages = pageWindow(safePage, totalPages, maxButtons);

  if (total === 0) return null;

  return (
    <nav
      className={clsx(
        'flex flex-col gap-3 border-t border-muted pt-4 sm:flex-row sm:items-center sm:justify-between',
        className
      )}
      aria-label="Pagination"
    >
      <p className="text-sm text-secondary tabular-nums">
        Showing <span className="font-medium text-main">{from}</span>–
        <span className="font-medium text-main">{to}</span> of{' '}
        <span className="font-medium text-main">{total.toLocaleString()}</span>
      </p>

      <div className="flex flex-wrap items-center gap-1.5">
        <PageBtn
          label="First"
          disabled={safePage <= 1}
          onClick={() => {
            if (safePage > 1) onPageChange(1);
          }}
          ariaLabel="First page"
        />
        <PageBtn
          label="Previous"
          disabled={safePage <= 1}
          onClick={() => {
            if (safePage > 1) onPageChange(safePage - 1);
          }}
          ariaLabel="Previous page"
        />

        {(pages[0] ?? 1) > 1 && <Ellipsis />}
        {pages.map((p) => (
          <PageBtn
            key={p}
            label={String(p)}
            active={p === safePage}
            onClick={() => {
              if (p !== safePage) onPageChange(p);
            }}
            ariaLabel={`Page ${p}`}
            ariaCurrent={p === safePage ? 'page' : undefined}
          />
        ))}
        {(pages[pages.length - 1] ?? 0) < totalPages && <Ellipsis />}

        <PageBtn
          label="Next"
          disabled={safePage >= totalPages}
          onClick={() => {
            if (safePage < totalPages) onPageChange(safePage + 1);
          }}
          ariaLabel="Next page"
        />
        <PageBtn
          label="Last"
          disabled={safePage >= totalPages}
          onClick={() => {
            if (safePage < totalPages) onPageChange(totalPages);
          }}
          ariaLabel="Last page"
        />
      </div>
    </nav>
  );
}

function pageWindow(page: number, totalPages: number, maxButtons: number): number[] {
  if (totalPages <= maxButtons) {
    return Array.from({ length: totalPages }, (_, i) => i + 1);
  }
  const half = Math.floor(maxButtons / 2);
  let start = Math.max(1, page - half);
  let end = start + maxButtons - 1;
  if (end > totalPages) {
    end = totalPages;
    start = end - maxButtons + 1;
  }
  return Array.from({ length: end - start + 1 }, (_, i) => start + i);
}

function Ellipsis() {
  return (
    <span className="px-1 text-sm text-secondary" aria-hidden="true">
      …
    </span>
  );
}

function PageBtn({
  label,
  onClick,
  disabled,
  active,
  ariaLabel,
  ariaCurrent,
}: {
  label: string;
  onClick: () => void;
  disabled?: boolean;
  active?: boolean;
  ariaLabel: string;
  ariaCurrent?: 'page';
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      aria-label={ariaLabel}
      aria-current={ariaCurrent}
      className={clsx(
        'inline-flex min-h-[40px] min-w-[40px] items-center justify-center rounded-lg px-2.5 text-sm font-medium transition-colors',
        active
          ? 'bg-navy-900 text-white dark:bg-accent-600'
          : 'border border-muted bg-surface text-main hover:bg-surface-hover',
        disabled && 'cursor-not-allowed opacity-40 hover:bg-surface'
      )}
    >
      {label}
    </button>
  );
}
