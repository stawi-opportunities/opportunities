import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  fetchOpportunities,
  starOpportunity,
  unstarOpportunity,
  type FeedItem,
  type OpportunityFilter,
} from '@/api/candidates';
import { fetchSnapshot } from '@/api/snapshot';
import type { OpportunitySnapshot as ApiSnapshot } from '@/types/snapshot';
import { OpportunityCard, type OpportunitySnapshot } from './OpportunityCard';
import { EmptyFeedState } from '@/components/dashboard/EmptyFeedState';
import {
  FilterChips,
  readFiltersFromURL,
  type FeedFilters,
} from '@/components/dashboard/FilterChips';
import { useI18n } from '@/i18n/I18nProvider';
import type { StringKey } from '@/i18n/strings';
import { useToast } from '@/hooks/useToast';
import { SortPicker } from '@/components/ui/SortPicker';
import type { SearchParams } from '@/types/search';
import { openApplyAndTrack } from '@/utils/apply';

const FILTER_KEYS: { id: OpportunityFilter; labelKey: StringKey }[] = [
  { id: 'all', labelKey: 'feed.all' },
  { id: 'matches', labelKey: 'feed.matches' },
  { id: 'starred', labelKey: 'feed.starred' },
  { id: 'applied', labelKey: 'feed.applied' },
];

function readFilterFromURL(): OpportunityFilter {
  if (typeof window === 'undefined') return 'all';
  const v = new URL(window.location.href).searchParams.get('filter');
  if (v === 'matches' || v === 'starred' || v === 'applied') return v;
  return 'all';
}

function writeFilterToURL(filter: OpportunityFilter) {
  if (typeof window === 'undefined') return;
  const url = new URL(window.location.href);
  if (filter === 'all') url.searchParams.delete('filter');
  else url.searchParams.set('filter', filter);
  window.history.pushState({}, '', url.toString());
}

function locationFromParts(
  city?: string,
  region?: string,
  country?: string,
  remote?: boolean
): string | undefined {
  const parts = [city, region, country].filter(Boolean);
  if (remote && !parts.some((p) => /remote/i.test(String(p)))) {
    parts.push('Remote');
  }
  return parts.length ? parts.join(', ') : remote ? 'Remote' : undefined;
}

function toCardSnapshot(snap: ApiSnapshot | null): OpportunitySnapshot | null {
  if (!snap) return null;
  return {
    title: snap.title,
    company: snap.issuing_entity,
    location: snap.anchor_location
      ? [snap.anchor_location.city, snap.anchor_location.region, snap.anchor_location.country]
          .filter(Boolean)
          .join(', ')
      : snap.remote
        ? 'Remote'
        : undefined,
    posted_at: snap.posted_at,
    salary_min: snap.amount_min,
    salary_max: snap.amount_max,
    currency: snap.currency,
    kind: snap.kind,
    id: snap.id,
    slug: snap.slug,
    has_how_to_apply: snap.has_how_to_apply,
    apply_url: snap.apply_url,
  };
}

/** Prefer feed-join enrichment so cards never depend on public slug-only lookup. */
function feedItemToSnapshot(it: FeedItem): OpportunitySnapshot | null {
  if (!it.title && !it.slug) return null;
  return {
    title: it.title || it.opportunity_id,
    company: it.company,
    location: locationFromParts(it.city, it.region, it.country, it.remote),
    posted_at: it.posted_at,
    salary_min: it.salary_min,
    salary_max: it.salary_max,
    currency: it.currency,
    kind: it.kind,
    id: it.opportunity_id,
    slug: it.slug,
    has_how_to_apply: it.has_how_to_apply,
    apply_url: it.apply_url,
  };
}

export function OpportunitiesFeed({
  initialFilter,
}: {
  /** When set (e.g. matches section), prefer this over the URL on first paint. */
  initialFilter?: OpportunityFilter;
} = {}) {
  const { t } = useI18n();
  const { push: toast } = useToast();
  const [filter, setFilter] = useState<OpportunityFilter>(
    () => initialFilter ?? readFilterFromURL()
  );
  const [feedFilters, setFeedFilters] = useState<FeedFilters>(readFiltersFromURL);
  const [sort, setSort] = useState<SearchParams['sort']>('recent');
  const [items, setItems] = useState<FeedItem[]>([]);
  const [nextCursor, setNextCursor] = useState<string | undefined>(undefined);
  const [loading, setLoading] = useState(true);
  const [hasError, setHasError] = useState(false);
  const [pendingItems, setPendingItems] = useState<Set<string>>(new Set());
  const [snapshots, setSnapshots] = useState<Record<string, OpportunitySnapshot | null>>({});

  const counts = useMemo(
    () => ({
      all: items.length,
      matches: items.filter((i) => (i.score ?? 0) > 0).length,
      starred: items.filter((i) => i.starred).length,
      applied: items.filter((i) => i.application).length,
    }),
    [items]
  );

  const filteredItems = useMemo(() => {
    let result = items;
    if (feedFilters.remote === true) {
      result = result.filter((it) => {
        if (it.remote) return true;
        const snap = snapshots[it.opportunity_id];
        return snap?.location?.toLowerCase().includes('remote') ?? false;
      });
    } else if (feedFilters.remote === false) {
      result = result.filter((it) => {
        if (it.remote) return false;
        const snap = snapshots[it.opportunity_id];
        return snap ? !snap.location?.toLowerCase().includes('remote') : true;
      });
    }
    if (feedFilters.kind) {
      result = result.filter((it) => {
        if (it.kind) return it.kind === feedFilters.kind;
        const snap = snapshots[it.opportunity_id];
        return snap?.kind === feedFilters.kind;
      });
    }
    return result;
  }, [items, snapshots, feedFilters]);

  const load = useCallback(
    async (f: OpportunityFilter, cursor?: string) => {
      setLoading(true);
      setHasError(false);
      try {
        const page = await fetchOpportunities({ filter: f, cursor, sort });
        setItems((prev) => (cursor ? [...prev, ...page.items] : page.items));
        setNextCursor(page.next_cursor);
        // Seed cards from feed join immediately — no public API hop required.
        const map: Record<string, OpportunitySnapshot | null> = {};
        for (const it of page.items) {
          map[it.opportunity_id] = feedItemToSnapshot(it);
        }
        setSnapshots((prev) => (cursor ? { ...prev, ...map } : map));
      } catch {
        setHasError(true);
      } finally {
        setLoading(false);
      }
    },
    [sort]
  );

  useEffect(() => {
    void load(filter);
  }, [filter, sort, load]);

  // Optional enrichment: when feed lacked title/slug, resolve by id (API accepts slug or canonical_id).
  useEffect(() => {
    const need = items.filter(
      (it) =>
        !it.title && (!(it.opportunity_id in snapshots) || snapshots[it.opportunity_id] === null)
    );
    if (need.length === 0) return;
    let cancelled = false;
    (async () => {
      const results = await Promise.allSettled(
        need.map((it) => fetchSnapshot(it.slug || it.opportunity_id))
      );
      if (cancelled) return;
      const map: Record<string, OpportunitySnapshot | null> = {};
      need.forEach((it, i) => {
        const r = results[i] as PromiseFulfilledResult<ApiSnapshot | null> | PromiseRejectedResult;
        const snap = r.status === 'fulfilled' ? r.value : null;
        map[it.opportunity_id] = toCardSnapshot(snap);
      });
      setSnapshots((prev) => ({ ...prev, ...map }));
    })();
    return () => {
      cancelled = true;
    };
  }, [items, snapshots]);

  const onSelectFilter = (id: OpportunityFilter) => {
    if (id === filter) return;
    writeFilterToURL(id);
    setFilter(id);
  };

  const onStar = useCallback(
    async (id: string) => {
      setPendingItems((prev) => new Set(prev).add(id));
      const snapshot = items;
      setItems((prev) =>
        prev.map((it) => (it.opportunity_id === id ? { ...it, starred: true } : it))
      );
      try {
        await starOpportunity(id);
      } catch {
        setItems(snapshot);
        toast('Failed to save.', 'error');
      } finally {
        setPendingItems((prev) => {
          const next = new Set(prev);
          next.delete(id);
          return next;
        });
      }
    },
    [items, toast]
  );

  const onUnstar = useCallback(
    async (id: string) => {
      setPendingItems((prev) => new Set(prev).add(id));
      const snapshot = items;
      setItems((prev) =>
        prev.map((it) => (it.opportunity_id === id ? { ...it, starred: false } : it))
      );
      try {
        await unstarOpportunity(id);
      } catch {
        setItems(snapshot);
        toast('Failed to remove.', 'error');
      } finally {
        setPendingItems((prev) => {
          const next = new Set(prev);
          next.delete(id);
          return next;
        });
      }
    },
    [items, toast]
  );

  const onApply = useCallback(
    async (id: string) => {
      setPendingItems((prev) => new Set(prev).add(id));
      const row = items.find((it) => it.opportunity_id === id);
      const snapshot = items;
      const now = new Date().toISOString();
      await openApplyAndTrack(id, row?.apply_url, {
        toast: (msg, kind) => toast(msg, kind),
        onTracked: () => {
          setItems((prev) =>
            prev.map((it) =>
              it.opportunity_id === id
                ? {
                    ...it,
                    application: {
                      status: 'applied',
                      applied_at: now,
                      last_event_at: now,
                      method: 'manual',
                    },
                  }
                : it
            )
          );
        },
        onTrackFailed: () => setItems(snapshot),
      });
      setPendingItems((prev) => {
        const next = new Set(prev);
        next.delete(id);
        return next;
      });
    },
    [items, toast]
  );

  return (
    <section aria-label="Your opportunities">
      <div className="sticky top-0 z-10 -mx-4 bg-white px-4 pb-3 shadow-sm dark:bg-navy-900 sm:static sm:mx-0 sm:px-0 sm:pb-0 sm:shadow-none">
        <div className="flex flex-wrap items-center gap-2 pt-2" role="tablist">
          {FILTER_KEYS.map((f) => {
            const active = f.id === filter;
            const count = counts[f.id];
            return (
              <button
                key={f.id}
                role="tab"
                aria-selected={active}
                type="button"
                onClick={() => onSelectFilter(f.id)}
                className={`min-h-[44px] rounded-full px-3.5 py-1.5 text-sm font-medium transition-colors ${
                  active
                    ? 'bg-navy-900 text-white'
                    : 'border border-gray-300 bg-white text-gray-700 hover:bg-gray-50 dark:border-navy-600 dark:bg-navy-800 dark:text-gray-300 dark:hover:bg-navy-700'
                }`}
              >
                {t(f.labelKey)}
                {count > 0 && <span className="ml-1.5 text-xs opacity-70">({count})</span>}
              </button>
            );
          })}
        </div>

        {items.length > 0 && (
          <div className="mt-3 flex flex-wrap items-end gap-4">
            <FilterChips filters={feedFilters} onChange={setFeedFilters} t={t} />
            <SortPicker value={sort} onChange={(v) => setSort(v)} />
          </div>
        )}
      </div>

      <div className="space-y-4">
        {hasError ? (
          <div
            role="alert"
            className="rounded-md border border-amber-300 bg-amber-50 p-4 text-sm text-amber-800 dark:border-amber-700 dark:bg-amber-900/20 dark:text-amber-300"
          >
            {t('feed.loadError')}
          </div>
        ) : loading && items.length === 0 ? (
          <div className="space-y-3">
            {[1, 2, 3].map((i) => (
              <div
                key={i}
                className="animate-pulse rounded-lg border border-gray-200 bg-white p-4 dark:border-navy-700 dark:bg-navy-900"
              >
                <div className="h-4 w-3/4 rounded bg-gray-100 dark:bg-navy-800" />
                <div className="mt-2 h-3 w-1/2 rounded bg-gray-100 dark:bg-navy-800" />
                <div className="mt-3 flex gap-2">
                  <div className="h-8 w-20 rounded bg-gray-100 dark:bg-navy-800" />
                  <div className="h-8 w-20 rounded bg-gray-100 dark:bg-navy-800" />
                </div>
              </div>
            ))}
          </div>
        ) : filteredItems.length === 0 && items.length > 0 ? (
          <p className="rounded-md border border-gray-200 bg-white p-4 text-sm text-gray-500 dark:border-navy-700 dark:bg-navy-900 dark:text-gray-400">
            No opportunities match your current filters.
          </p>
        ) : items.length === 0 ? (
          <EmptyFeedState filter={filter} t={t} />
        ) : (
          <>
            <ul className="space-y-3">
              {filteredItems.map((it) => (
                <OpportunityCard
                  key={it.opportunity_id}
                  item={it}
                  snapshot={snapshots[it.opportunity_id] ?? null}
                  onStar={onStar}
                  onUnstar={onUnstar}
                  onApply={onApply}
                  isPending={pendingItems.has(it.opportunity_id)}
                />
              ))}
            </ul>
            <div className="flex items-center justify-between text-xs text-gray-400 dark:text-gray-500">
              <span>
                {filteredItems.length} {t('feed.opportunities')}
              </span>
              {nextCursor && (
                <button
                  type="button"
                  onClick={() => void load(filter, nextCursor)}
                  className="min-h-[44px] rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50 dark:border-navy-600 dark:bg-navy-800 dark:text-gray-300 dark:hover:bg-navy-700"
                  disabled={loading}
                >
                  {loading ? t('common.loading') : t('cta.loadMore')}
                </button>
              )}
            </div>
          </>
        )}
      </div>
    </section>
  );
}
