import { useEffect, useState } from 'react';
import { fetchOpportunities, type FeedItem } from '@/api/candidates';
import { fetchSnapshot } from '@/api/snapshot';
import type { OpportunitySnapshot as ApiSnapshot } from '@/types/snapshot';
import { OpportunityCard, type OpportunitySnapshot } from '@/components/OpportunityCard';
import { useI18n } from '@/i18n/I18nProvider';

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

const STATUS_ORDER: Record<string, number> = {
  applied: 0,
  responded: 1,
  interview: 2,
  offer: 3,
  rejected: 4,
  hired: 5,
};

function statusRank(s: string): number {
  return STATUS_ORDER[s] ?? 99;
}

export function ApplicationsPanel() {
  const { t } = useI18n();
  const [items, setItems] = useState<FeedItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [hasError, setHasError] = useState(false);
  const [snapshots, setSnapshots] = useState<Record<string, OpportunitySnapshot | null>>({});

  useEffect(() => {
    let mounted = true;
    (async () => {
      setLoading(true);
      setHasError(false);
      try {
        const page = await fetchOpportunities({ filter: 'applied' });
        if (!mounted) return;
        setItems(page.items);
      } catch {
        if (!mounted) return;
        setHasError(true);
      } finally {
        if (mounted) setLoading(false);
      }
    })();
    return () => {
      mounted = false;
    };
  }, []);

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

  const sorted = [...items].sort(
    (a, b) => statusRank(a.application?.status ?? '') - statusRank(b.application?.status ?? '')
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
          </div>
        ))}
      </div>
    );
  }

  if (items.length === 0) {
    return (
      <div className="rounded-lg border border-gray-200 bg-white p-8 text-center dark:border-navy-700 dark:bg-navy-900">
        <p className="text-sm text-gray-600 dark:text-gray-400">
          You haven't applied to any opportunities yet.
        </p>
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
        {items.length} {items.length === 1 ? 'application' : 'applications'}
      </p>
      <ul className="space-y-3">
        {sorted.map((it) => (
          <OpportunityCard
            key={it.opportunity_id}
            item={it}
            snapshot={snapshots[it.opportunity_id] ?? null}
            onStar={() => {}}
            onUnstar={() => {}}
            onApply={() => {}}
          />
        ))}
      </ul>
    </div>
  );
}
