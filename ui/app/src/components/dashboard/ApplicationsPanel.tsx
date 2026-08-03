import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  fetchOpportunities,
  starOpportunity,
  unstarOpportunity,
  type FeedItem,
} from '@/api/candidates';
import { fetchSnapshot } from '@/api/snapshot';
import type { OpportunitySnapshot as ApiSnapshot } from '@/types/snapshot';
import { OpportunityCard, type OpportunitySnapshot } from '@/components/OpportunityCard';
import { useI18n } from '@/i18n/I18nProvider';
import { useToast } from '@/hooks/useToast';
import { openApplyAndTrack } from '@/utils/apply';
import {
  STAGES,
  type ApplicationStage,
  loadStageOverrides,
  resolveStage,
  setStageOverride,
} from '@/utils/applicationStages';

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
    deadline: snap.deadline,
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

function feedItemToSnapshot(it: FeedItem): OpportunitySnapshot | null {
  if (!it.title && !it.slug) return null;
  return {
    title: it.title || it.opportunity_id,
    company: it.company,
    location: [it.city, it.region, it.country].filter(Boolean).join(', ') || undefined,
    posted_at: it.posted_at,
    deadline: it.deadline,
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

/**
 * Applications pipeline as stage columns.
 * Stage can be advanced client-side (persisted locally) when the feed only
 * returns a coarse application status without patchable application IDs.
 */
export function ApplicationsPanel() {
  const { t } = useI18n();
  const { push: toast } = useToast();
  const [items, setItems] = useState<FeedItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [hasError, setHasError] = useState(false);
  const [snapshots, setSnapshots] = useState<Record<string, OpportunitySnapshot | null>>({});
  const [pendingItems, setPendingItems] = useState<Set<string>>(new Set());
  const [overrides, setOverrides] = useState(() => loadStageOverrides());

  useEffect(() => {
    let mounted = true;
    (async () => {
      setLoading(true);
      setHasError(false);
      try {
        const page = await fetchOpportunities({ filter: 'applied' });
        if (!mounted) return;
        setItems(page.items);
        const map: Record<string, OpportunitySnapshot | null> = {};
        for (const it of page.items) {
          map[it.opportunity_id] = feedItemToSnapshot(it);
        }
        setSnapshots(map);
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
      .filter(
        (it) => !it.title && (!(it.opportunity_id in snapshots) || !snapshots[it.opportunity_id])
      )
      .map((it) => it.opportunity_id);
    if (ids.length === 0) return;
    let cancelled = false;
    (async () => {
      const results = await Promise.allSettled(
        ids.map((id) => {
          const row = items.find((i) => i.opportunity_id === id);
          return fetchSnapshot(row?.slug || id);
        })
      );
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
  }, [items, snapshots]);

  const stageOf = useCallback(
    (it: FeedItem) => {
      void overrides; // re-read when overrides state changes
      return resolveStage(it.opportunity_id, it.application?.status);
    },
    [overrides]
  );

  const byStage = useMemo(() => {
    const map = new Map<string, FeedItem[]>();
    for (const s of STAGES) map.set(s.id, []);
    map.set('other', []);
    for (const it of items) {
      const st = stageOf(it);
      const key = STAGES.some((s) => s.id === st) ? st : 'other';
      map.get(key)!.push(it);
    }
    return map;
  }, [items, stageOf]);

  const onStageChange = (opportunityId: string, stage: ApplicationStage) => {
    setStageOverride(opportunityId, stage);
    setOverrides(loadStageOverrides());
    setItems((prev) =>
      prev.map((it) =>
        it.opportunity_id === opportunityId && it.application
          ? {
              ...it,
              application: {
                ...it.application,
                status: stage,
                last_event_at: new Date().toISOString(),
              },
            }
          : it
      )
    );
    toast(`Moved to ${STAGES.find((s) => s.id === stage)?.label ?? stage}.`, 'success');
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
          <div key={i} className="animate-pulse rounded-lg border border-muted bg-surface p-4">
            <div className="h-4 w-3/4 rounded bg-surface-hover" />
          </div>
        ))}
      </div>
    );
  }

  if (items.length === 0) {
    return (
      <div className="rounded-lg border border-muted bg-surface p-8 text-center">
        <h2 className="text-base font-semibold text-main">Application pipeline</h2>
        <p className="mt-2 text-sm text-secondary">
          Roles you apply to appear here by stage (Applied → Interview → Offer). Start from Matches.
        </p>
        <a
          href="/dashboard/#matches"
          className="mt-4 inline-block text-sm font-medium text-accent-600 hover:text-accent-700"
        >
          Go to matches →
        </a>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <div>
        <h2 className="text-lg font-semibold text-main">Application pipeline</h2>
        <p className="mt-1 text-sm text-secondary">
          {items.length} application{items.length === 1 ? '' : 's'}. Advance stages as you hear back
          from employers (saved on this device until server sync is available).
        </p>
      </div>

      <div className="flex gap-3 overflow-x-auto pb-2">
        {STAGES.map((col) => {
          const colItems = byStage.get(col.id) ?? [];
          return (
            <div
              key={col.id}
              className="flex w-[min(100%,280px)] shrink-0 flex-col rounded-lg border border-muted bg-surface-muted/40"
            >
              <div className="flex items-center justify-between border-b border-muted px-3 py-2">
                <h3 className="text-sm font-semibold text-main">{col.label}</h3>
                <span className="rounded-full bg-surface px-2 py-0.5 text-xs tabular-nums text-secondary">
                  {colItems.length}
                </span>
              </div>
              <ul className="flex max-h-[70vh] flex-col gap-2 overflow-y-auto p-2">
                {colItems.length === 0 && (
                  <li className="px-2 py-6 text-center text-xs text-secondary">None</li>
                )}
                {colItems.map((it) => (
                  <li key={it.opportunity_id} className="rounded-lg bg-surface shadow-sm">
                    <OpportunityCard
                      item={{
                        ...it,
                        application: it.application
                          ? { ...it.application, status: stageOf(it) }
                          : it.application,
                      }}
                      snapshot={snapshots[it.opportunity_id] ?? null}
                      onStar={onStar}
                      onUnstar={onUnstar}
                      onApply={onApply}
                      isPending={pendingItems.has(it.opportunity_id)}
                    />
                    <div className="border-t border-muted px-3 py-2">
                      <label className="flex items-center gap-2 text-xs text-secondary">
                        <span className="shrink-0">Stage</span>
                        <select
                          className="min-h-[36px] w-full rounded-md border border-muted bg-surface px-2 py-1 text-xs text-main"
                          value={String(stageOf(it))}
                          onChange={(e) =>
                            onStageChange(it.opportunity_id, e.target.value as ApplicationStage)
                          }
                        >
                          {STAGES.map((s) => (
                            <option key={s.id} value={s.id}>
                              {s.label}
                            </option>
                          ))}
                        </select>
                      </label>
                    </div>
                  </li>
                ))}
              </ul>
            </div>
          );
        })}
      </div>
    </div>
  );
}
