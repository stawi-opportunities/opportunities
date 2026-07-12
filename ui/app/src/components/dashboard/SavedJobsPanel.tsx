import { useCallback, useEffect, useState } from 'react';
import {
  fetchOpportunities,
  unstarOpportunity,
  applyToOpportunity,
  type FeedItem,
} from '@/api/candidates';
import { fetchSnapshot } from '@/api/snapshot';
import type { OpportunitySnapshot as ApiSnapshot } from '@/types/snapshot';
import { OpportunityCard, type OpportunitySnapshot } from '@/components/OpportunityCard';
import { useI18n } from '@/i18n/I18nProvider';
import { useToast } from '@/hooks/useToast';

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

export function SavedJobsPanel() {
  const { t } = useI18n();
  const { push: toast } = useToast();
  const [items, setItems] = useState<FeedItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [hasError, setHasError] = useState(false);
  const [snapshots, setSnapshots] = useState<Record<string, OpportunitySnapshot | null>>({});
  const [pendingItems, setPendingItems] = useState<Set<string>>(new Set());

  const load = useCallback(async () => {
    setLoading(true);
    setHasError(false);
    try {
      const page = await fetchOpportunities({ filter: 'starred' });
      setItems(page.items);
    } catch {
      setHasError(true);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

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

  const onUnstar = useCallback(
    async (id: string) => {
      setPendingItems((prev) => new Set(prev).add(id));
      const snapshot = items;
      setItems((prev) => prev.filter((it) => it.opportunity_id !== id));
      try {
        await unstarOpportunity(id);
        toast('Removed from saved.', 'success');
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
    [items, toast, t]
  );

  const onApply = useCallback(
    async (id: string) => {
      setPendingItems((prev) => new Set(prev).add(id));
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
        toast('Applied successfully.', 'success');
      } catch {
        setItems(snapshot);
        toast('Failed to apply.', 'error');
      } finally {
        setPendingItems((prev) => {
          const next = new Set(prev);
          next.delete(id);
          return next;
        });
      }
    },
    [items, toast, t]
  );

  if (hasError) {
    return (
      <div
        role="alert"
        className="rounded-md border border-amber-300 bg-amber-50 p-4 text-sm text-amber-800 dark:border-amber-700 dark:bg-amber-900/20 dark:text-amber-300"
      >
        {t('feed.loadError')}
      </div>
    );
  }

  if (loading) {
    return (
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
    );
  }

  if (items.length === 0) {
    return (
      <div className="rounded-lg border border-gray-200 bg-white p-8 text-center dark:border-navy-700 dark:bg-navy-900">
        <p className="text-sm text-gray-600 dark:text-gray-400">{t('feed.empty')}</p>
        <a
          href="/jobs/"
          className="mt-4 inline-block text-sm font-medium text-accent-600 hover:text-accent-700 dark:text-accent-400 dark:hover:text-accent-300"
        >
          {t('dash.browseJobs')} →
        </a>
      </div>
    );
  }

  return (
    <div className="space-y-3">
      <p className="text-sm text-gray-500 dark:text-gray-400">
        {items.length} {t('feed.opportunities')}
      </p>
      <ul className="space-y-3">
        {items.map((it) => (
          <OpportunityCard
            key={it.opportunity_id}
            item={it}
            snapshot={snapshots[it.opportunity_id] ?? null}
            onStar={() => {}}
            onUnstar={onUnstar}
            onApply={onApply}
            isPending={pendingItems.has(it.opportunity_id)}
          />
        ))}
      </ul>
    </div>
  );
}
