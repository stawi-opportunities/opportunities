import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { searchJobs } from '@/api/search';
import type { SearchParams } from '@/types/search';
import { QUERY_KEYS } from '@/constants/queryKeys';
import { JobRow } from '@/components/JobRow';
import { ListPagination } from '@/components/ui/ListPagination';
import { SortPicker } from '@/components/ui/SortPicker';
import { Spinner } from '@/components/ui/Spinner';

const PAGE_SIZE = 25;

type FilterChip = {
  label: string;
  key: 'remote_type' | 'employment_type' | 'seniority';
  value: string;
};

const CHIPS: FilterChip[] = [
  { label: 'Remote', key: 'remote_type', value: 'remote' },
  { label: 'Full-time', key: 'employment_type', value: 'full_time' },
  { label: 'Part-time', key: 'employment_type', value: 'part_time' },
  { label: 'Contract', key: 'employment_type', value: 'contract' },
  { label: 'Entry', key: 'seniority', value: 'entry' },
  { label: 'Senior', key: 'seniority', value: 'senior' },
];

function readPageFromURL(): number {
  if (typeof window === 'undefined') return 1;
  const raw = new URL(window.location.href).searchParams.get('page');
  const n = raw ? Number.parseInt(raw, 10) : 1;
  return Number.isFinite(n) && n >= 1 ? n : 1;
}

function writePageToURL(page: number, mode: 'push' | 'replace' = 'push'): void {
  if (typeof window === 'undefined') return;
  const url = new URL(window.location.href);
  if (page <= 1) url.searchParams.delete('page');
  else url.searchParams.set('page', String(page));
  const next = url.pathname + url.search + url.hash;
  if (mode === 'replace') {
    window.history.replaceState({}, '', next);
  } else {
    // push so browser Back/Forward moves between pages
    window.history.pushState({}, '', next);
  }
}

export default function JobList() {
  const [active, setActive] = useState<Partial<Record<FilterChip['key'], string>>>({});
  const [sort, setSort] = useState<SearchParams['sort']>('recent');
  const [page, setPage] = useState(readPageFromURL);
  const listTopRef = useRef<HTMLDivElement>(null);
  const headingRef = useRef<HTMLHeadingElement>(null);

  // Keep page state in sync with browser Back/Forward.
  useEffect(() => {
    function onPopState() {
      setPage(readPageFromURL());
    }
    window.addEventListener('popstate', onPopState);
    return () => window.removeEventListener('popstate', onPopState);
  }, []);

  const goToPage = useCallback((next: number, opts?: { replace?: boolean; scroll?: boolean }) => {
    const p = Math.max(1, next);
    setPage(p);
    writePageToURL(p, opts?.replace ? 'replace' : 'push');
    if (opts?.scroll !== false) {
      // Prefer the list top so pagination doesn't leave the user mid-page.
      const el = listTopRef.current ?? headingRef.current;
      if (el) {
        el.scrollIntoView({ behavior: 'smooth', block: 'start' });
      } else {
        window.scrollTo({ top: 0, behavior: 'smooth' });
      }
      // Move focus to the heading for keyboard/screen-reader continuity.
      requestAnimationFrame(() => {
        headingRef.current?.focus({ preventScroll: true });
      });
    }
  }, []);

  /** Filters/sort change the result set — always land on page 1 without stacking history. */
  function resetToFirstPage() {
    if (page === 1) return;
    goToPage(1, { replace: true, scroll: true });
  }

  function toggle(chip: FilterChip) {
    setActive((prev) => {
      const current = prev[chip.key];
      if (current === chip.value) {
        const next = { ...prev };
        delete next[chip.key];
        return next;
      }
      return { ...prev, [chip.key]: chip.value };
    });
    resetToFirstPage();
  }

  function clearFilters() {
    setActive({});
    resetToFirstPage();
  }

  function onSortChange(v: SearchParams['sort']) {
    setSort(v);
    resetToFirstPage();
  }

  const searchParams = useMemo<SearchParams>(() => {
    const offset = (page - 1) * PAGE_SIZE;
    return {
      sort,
      limit: PAGE_SIZE,
      offset,
      ...active,
    };
  }, [sort, active, page]);

  const q = useQuery({
    queryKey: QUERY_KEYS.SEARCH(searchParams as Record<string, unknown>),
    queryFn: () => searchJobs(searchParams),
    staleTime: 30_000,
    placeholderData: (prev) => prev,
  });

  const total = q.data?.total ?? 0;
  const results = q.data?.results ?? [];
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));
  const hasFilters = Object.keys(active).length > 0;

  // Clamp if the URL asked for a page past the end (e.g. after filters shrink the set).
  useEffect(() => {
    if (!q.data || q.isFetching) return;
    if (total === 0 && page !== 1) {
      goToPage(1, { replace: true, scroll: false });
      return;
    }
    if (total > 0 && page > totalPages) {
      goToPage(totalPages, { replace: true, scroll: false });
    }
  }, [q.data, q.isFetching, total, totalPages, page, goToPage]);

  const isInitialLoad = q.isLoading && !q.data;
  const isPageTransition = q.isFetching && !!q.data;

  return (
    <div className="mx-auto max-w-7xl px-4 py-8 sm:px-6 lg:px-8">
      <div className="flex flex-col gap-6 lg:flex-row lg:items-start lg:gap-10">
        <div className="min-w-0 flex-1">
          <div className="flex items-center justify-between gap-4">
            <h1
              ref={headingRef}
              tabIndex={-1}
              className="text-3xl font-bold text-main outline-none"
            >
              All jobs
            </h1>
            <a href="/onboarding/" className="btn-primary hidden sm:inline-flex">
              Create profile
            </a>
          </div>

          <div className="mt-5 flex flex-wrap items-center gap-2">
            {CHIPS.map((chip) => {
              const on = active[chip.key] === chip.value;
              return (
                <button
                  key={`${chip.key}-${chip.value}`}
                  type="button"
                  onClick={() => toggle(chip)}
                  aria-pressed={on}
                  className={[
                    'inline-flex items-center rounded-full border px-4 py-1.5 text-sm font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500',
                    on
                      ? 'border-accent-500 bg-accent-500/10 text-accent-500'
                      : 'border-muted-strong bg-surface text-secondary hover:border-accent-500/50 hover:text-main',
                  ].join(' ')}
                >
                  {chip.label}
                </button>
              );
            })}
            {hasFilters && (
              <button
                type="button"
                onClick={clearFilters}
                className="ml-1 text-sm text-secondary underline hover:text-main"
              >
                Clear
              </button>
            )}
          </div>

          <div ref={listTopRef} className="mt-6">
            {/* Status line: total + page, plus subtle fetching indicator */}
            <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
              <p className="text-sm text-secondary" aria-live="polite">
                {isInitialLoad ? (
                  'Loading jobs…'
                ) : q.isError ? null : total === 0 ? (
                  'No jobs match these filters'
                ) : (
                  <>
                    <span className="font-medium text-main">{total.toLocaleString()}</span>
                    {' jobs'}
                    {totalPages > 1 && (
                      <>
                        {' · Page '}
                        <span className="font-medium text-main">{Math.min(page, totalPages)}</span>
                        {' of '}
                        <span className="font-medium text-main">{totalPages}</span>
                      </>
                    )}
                  </>
                )}
              </p>
              {isPageTransition && (
                <span className="inline-flex items-center gap-1.5 text-xs text-secondary">
                  <Spinner size={14} />
                  Updating…
                </span>
              )}
            </div>

            {totalPages > 1 && !isInitialLoad && !q.isError && (
              <ListPagination
                page={page}
                pageSize={PAGE_SIZE}
                total={total}
                onPageChange={(p) => goToPage(p)}
                className="mb-4 border-t-0 border-b border-muted pb-4 pt-0"
              />
            )}

            {isInitialLoad && <SkeletonList />}

            {q.isError && (
              <div
                className="mt-4 rounded-md bg-accent-500/10 p-4 text-sm text-red-500"
                role="alert"
              >
                Could not load jobs.{' '}
                <button
                  type="button"
                  onClick={() => void q.refetch()}
                  className="font-medium underline hover:text-red-400"
                >
                  Retry
                </button>
              </div>
            )}

            {q.data && !isInitialLoad && (
              <>
                {results.length === 0 ? (
                  <div className="rounded-lg border border-muted bg-surface px-4 py-12 text-center">
                    <p className="text-sm font-medium text-main">No jobs found</p>
                    <p className="mt-1 text-sm text-secondary">
                      Try clearing filters or using advanced search.
                    </p>
                    {hasFilters && (
                      <button
                        type="button"
                        onClick={clearFilters}
                        className="btn-primary mt-4 inline-flex"
                      >
                        Clear filters
                      </button>
                    )}
                  </div>
                ) : (
                  <ul
                    className={[
                      'overflow-hidden rounded-lg border border-muted bg-surface',
                      isPageTransition ? 'opacity-70 transition-opacity' : '',
                    ].join(' ')}
                    aria-busy={isPageTransition || undefined}
                  >
                    {results.map((r) => (
                      <JobRow key={r.id} result={r} />
                    ))}
                  </ul>
                )}

                {totalPages > 1 && results.length > 0 && (
                  <ListPagination
                    page={page}
                    pageSize={PAGE_SIZE}
                    total={total}
                    onPageChange={(p) => goToPage(p)}
                    className="mt-4"
                  />
                )}
              </>
            )}
          </div>
        </div>

        <aside className="hidden w-64 shrink-0 lg:block">
          <div className="rounded-xl border border-muted bg-surface p-5 shadow-sm">
            <h3 className="text-sm font-semibold text-main">Filters</h3>
            <div className="mt-4">
              <SortPicker value={sort} onChange={onSortChange} />
            </div>
            <div className="mt-4 space-y-4">
              {(['remote_type', 'employment_type', 'seniority'] as const).map((key) => (
                <div key={key}>
                  <label className="block text-xs font-medium uppercase tracking-wide text-secondary">
                    {key === 'remote_type'
                      ? 'Remote'
                      : key === 'employment_type'
                        ? 'Employment'
                        : 'Level'}
                  </label>
                  <div className="mt-2 flex flex-wrap gap-2">
                    {CHIPS.filter((c) => c.key === key).map((c) => {
                      const on = active[c.key] === c.value;
                      return (
                        <button
                          key={c.value}
                          type="button"
                          onClick={() => toggle(c)}
                          aria-pressed={on}
                          className={`rounded-full border px-3 py-1 text-xs font-medium transition-colors ${
                            on
                              ? 'border-accent-500 bg-accent-500/10 text-accent-500'
                              : 'border-muted bg-surface-muted text-secondary hover:border-accent-500/50'
                          }`}
                        >
                          {c.label}
                        </button>
                      );
                    })}
                  </div>
                </div>
              ))}
              {hasFilters && (
                <button
                  type="button"
                  onClick={clearFilters}
                  className="min-h-[44px] w-full rounded-md border border-muted py-1.5 text-xs font-medium text-secondary transition-colors hover:border-muted-strong hover:text-main"
                >
                  Clear all filters
                </button>
              )}
            </div>
          </div>
          <div className="mt-4 rounded-xl border border-muted bg-surface p-5 shadow-sm">
            <h3 className="text-sm font-semibold text-main">Looking for more?</h3>
            <p className="mt-1 text-xs text-secondary">
              Use full search for keyword, location, and salary filters.
            </p>
            <a href="/search/" className="btn-primary mt-3 inline-flex w-full px-4 py-2 text-xs">
              Advanced search →
            </a>
          </div>
        </aside>
      </div>
    </div>
  );
}

function SkeletonList() {
  return (
    <ul
      className="overflow-hidden rounded-lg border border-muted bg-surface"
      aria-hidden="true"
      aria-busy="true"
    >
      {Array.from({ length: 8 }, (_, i) => (
        <li key={i} className="border-b border-muted px-4 py-4 last:border-b-0 sm:px-6">
          <div className="flex items-start gap-4 animate-pulse">
            <div className="h-10 w-10 shrink-0 rounded bg-surface-muted" />
            <div className="min-w-0 flex-1 space-y-2">
              <div className="h-4 w-2/3 max-w-xs rounded bg-surface-muted" />
              <div className="h-3 w-1/2 max-w-[12rem] rounded bg-surface-muted" />
              <div className="h-3 w-1/3 max-w-[8rem] rounded bg-surface-muted" />
            </div>
            <div className="h-3 w-12 shrink-0 rounded bg-surface-muted" />
          </div>
        </li>
      ))}
    </ul>
  );
}
