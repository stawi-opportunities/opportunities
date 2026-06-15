import { useCallback, useEffect, useState } from 'react';
import {
  applyToOpportunity,
  fetchOpportunities,
  starOpportunity,
  unstarOpportunity,
  type FeedItem,
  type OpportunityFilter,
} from '@/api/candidates';
import { fetchSnapshot } from '@/api/snapshot';
import type { OpportunitySnapshot as ApiSnapshot } from '@/types/snapshot';
import { OpportunityCard, type OpportunitySnapshot } from './OpportunityCard';
import { useI18n } from '@/i18n/I18nProvider';
import type { StringKey } from '@/i18n/strings';

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

function toCardSnapshot(snap: ApiSnapshot | null): OpportunitySnapshot | null {
  if (!snap) return null;
  return {
    title: snap.title,
    company: snap.issuing_entity,
    location: snap.anchor_location
      ? [snap.anchor_location.city, snap.anchor_location.region, snap.anchor_location.country]
          .filter(Boolean)
          .join(', ')
      : undefined,
    posted_at: snap.posted_at,
    salary_min: snap.amount_min,
    salary_max: snap.amount_max,
    currency: snap.currency,
    kind: snap.kind,
  };
}

export function OpportunitiesFeed() {
  const { t } = useI18n();
  const [filter, setFilter] = useState<OpportunityFilter>(readFilterFromURL);
  const [items, setItems] = useState<FeedItem[]>([]);
  const [nextCursor, setNextCursor] = useState<string | undefined>(undefined);
  const [loading, setLoading] = useState(true);
  const [hasError, setHasError] = useState(false);
  const [snapshots, setSnapshots] = useState<Record<string, OpportunitySnapshot | null>>({});

  const load = useCallback(async (f: OpportunityFilter, cursor?: string) => {
    setLoading(true);
    setHasError(false);
    try {
      const page = await fetchOpportunities({ filter: f, cursor });
      setItems((prev) => (cursor ? [...prev, ...page.items] : page.items));
      setNextCursor(page.next_cursor);
    } catch {
      setHasError(true);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load(filter);
  }, [filter, load]);

  useEffect(() => {
    const ids = items
      .filter((it) => !(it.opportunity_id in snapshots))
      .map((it) => it.opportunity_id);
    if (ids.length === 0) return;
    let cancelled = false;
    (async () => {
      const results = await Promise.allSettled(ids.map((id) => fetchSnapshot(id)));
      if (cancelled) return;
      const map: Record<string, OpportunitySnapshot | null> = {};
      ids.forEach((id, i) => {
        const r = results[i] as PromiseFulfilledResult<ApiSnapshot | null> | PromiseRejectedResult;
        const snap = r.status === 'fulfilled' ? r.value : null;
        map[id] = toCardSnapshot(snap);
      });
      setSnapshots((prev) => ({ ...prev, ...map }));
    })();
    return () => {
      cancelled = true;
    };
  }, [items]);

  const onSelectFilter = (id: OpportunityFilter) => {
    if (id === filter) return;
    writeFilterToURL(id);
    setFilter(id);
  };

  const onStar = useCallback(
    async (id: string) => {
      const snapshot = items;
      setItems((prev) =>
        prev.map((it) => (it.opportunity_id === id ? { ...it, starred: true } : it))
      );
      try {
        await starOpportunity(id);
      } catch {
        setItems(snapshot);
      }
    },
    [items]
  );

  const onUnstar = useCallback(
    async (id: string) => {
      const snapshot = items;
      setItems((prev) =>
        prev.map((it) => (it.opportunity_id === id ? { ...it, starred: false } : it))
      );
      try {
        await unstarOpportunity(id);
      } catch {
        setItems(snapshot);
      }
    },
    [items]
  );

  const onApply = useCallback(
    async (id: string) => {
      const snapshot = items;
      const now = new Date().toISOString();
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
      try {
        await applyToOpportunity(id, 'manual');
      } catch {
        setItems(snapshot);
      }
    },
    [items]
  );

  return (
    <section aria-label="Your opportunities" className="space-y-4">
      <div className="flex flex-wrap items-center gap-2" role="tablist">
        {FILTER_KEYS.map((f) => {
          const active = f.id === filter;
          return (
            <button
              key={f.id}
              role="tab"
              aria-selected={active}
              type="button"
              onClick={() => onSelectFilter(f.id)}
              className={`rounded-full px-3.5 py-1.5 text-sm font-medium transition-colors ${
                active
                  ? 'bg-navy-900 text-white'
                  : 'border border-gray-300 bg-white text-gray-700 hover:bg-gray-50'
              }`}
            >
              {t(f.labelKey)}
            </button>
          );
        })}
      </div>

      {hasError ? (
        <div
          role="alert"
          className="rounded-md border border-amber-300 bg-amber-50 p-4 text-sm text-amber-800"
        >
          {t('feed.loadError')}
        </div>
      ) : loading && items.length === 0 ? (
        <p className="rounded-md border border-gray-200 bg-white p-4 text-sm text-gray-600">
          {t('common.loading')}
        </p>
      ) : items.length === 0 ? (
        <p className="rounded-md border border-gray-200 bg-white p-4 text-sm text-gray-600">
          {t('feed.empty')} {filter !== 'all' && t('feed.tryAllFilter')}
        </p>
      ) : (
        <>
          <ul className="space-y-3">
            {items.map((it) => (
              <OpportunityCard
                key={it.opportunity_id}
                item={it}
                snapshot={snapshots[it.opportunity_id] ?? null}
                onStar={onStar}
                onUnstar={onUnstar}
                onApply={onApply}
              />
            ))}
          </ul>
          <div className="flex items-center justify-between text-xs text-gray-400">
            <span>{items.length} {t('feed.opportunities')}</span>
            {nextCursor && (
              <button
                type="button"
                onClick={() => void load(filter, nextCursor)}
                className="rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
                disabled={loading}
              >
                {loading ? t('common.loading') : t('cta.loadMore')}
              </button>
            )}
          </div>
        </>
      )}
    </section>
  );
}
