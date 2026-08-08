import { useCallback, useEffect, useState } from 'react';
import { planById, type PlanId } from '@/utils/plans';
import { Panel } from './Panel';
import { OpportunitiesFeed } from '@/components/OpportunitiesFeed';
import { refreshMyMatches, type MatchRefreshResult } from '@/api/candidates';
import { useToast } from '@/hooks/useToast';
import { Button } from '@/components/ui/Button';

function emptyReasonMessage(res: MatchRefreshResult): string {
  switch (res.reason) {
    case 'weekly_cap':
      return res.proof
        ? `Free proof limit reached (${res.weekly_used ?? 0}/${res.weekly_cap ?? 3} this week). Subscribe for more weekly matches.`
        : `Weekly match limit reached (${res.weekly_used ?? 0}/${res.weekly_cap ?? 5}). Resets on a rolling 7-day window.`;
    case 'daily_cap':
      return res.proof
        ? 'Free proof allows 1 new match per day — try again tomorrow, or subscribe for a higher daily budget.'
        : 'Daily match generation limit reached. Try again tomorrow or upgrade for a higher budget.';
    case 'no_inventory':
      return 'No recent roles matched your filters yet. Widen locations/roles under CV → Match preferences.';
    case 'below_threshold':
      return 'Roles were found but none cleared your quality bar. Improve your CV score, then try again.';
    default:
      return 'Match search complete — no new roles above your quality threshold yet. Update your CV or preferences, then re-run.';
  }
}

/**
 * Scored shortlist only — no top dual-mode menus.
 * CTAs on this screen (Find matches, Go to CV, Upgrade) drive next steps.
 */
export function MatchesPanel({
  plan,
  freeProof = false,
  queued: queuedProp,
  delivered: deliveredProp,
  subQueryError = false,
  subLoading = false,
  onUpgrade,
  /** When true (stage dashboard_setup), emphasize CV completion over empty inventory. */
  setupMode = false,
  setupMissing = [] as string[],
}: {
  plan: PlanId;
  freeProof?: boolean;
  queued: number | null;
  delivered: number | null;
  subQueryError?: boolean;
  subLoading?: boolean;
  onUpgrade?: () => void;
  setupMode?: boolean;
  setupMissing?: string[];
}) {
  const { push: toast } = useToast();
  const [refreshing, setRefreshing] = useState(false);
  const [refreshKey, setRefreshKey] = useState(0);

  const queued = queuedProp ?? 0;
  const delivered = deliveredProp ?? 0;

  const planInfo = planById(plan);
  const unlimited = !freeProof && planInfo.matchesPerWeek === null;
  const cap = freeProof ? 3 : (planInfo.matchesPerWeek ?? 0);
  const progressPct =
    !unlimited && cap > 0 ? Math.min(100, Math.round((delivered / cap) * 100)) : 0;

  const [lastReason, setLastReason] = useState<string | null>(null);
  const [autoKickDone, setAutoKickDone] = useState(false);

  const runRefresh = useCallback(
    async (silent: boolean) => {
      setRefreshing(true);
      try {
        const res = await refreshMyMatches();
        setLastReason(res.reason ?? null);
        if (!silent) {
          if (res.matches_written > 0) {
            toast(
              `Found ${res.matches_written} new match${res.matches_written === 1 ? '' : 'es'}.`,
              'success'
            );
          } else {
            toast(emptyReasonMessage(res), 'info');
          }
        }
        setRefreshKey((k) => k + 1);
      } catch (err) {
        const msg = err instanceof Error ? err.message : String(err);
        if (/no_embedding|embedding|cv/i.test(msg)) {
          if (!silent) {
            toast(
              'Upload a CV under Dashboard → CV so we can match roles to your profile.',
              'error'
            );
          }
          setLastReason('need_cv');
        } else if (!silent) {
          toast('Could not refresh matches. Try again in a moment.', 'error');
        }
      } finally {
        setRefreshing(false);
      }
    },
    [toast]
  );

  useEffect(() => {
    if (subLoading) return;
    if (autoKickDone) return;
    if (queued > 0) {
      setAutoKickDone(true);
      return;
    }
    setAutoKickDone(true);
    void runRefresh(true);
  }, [subLoading, queued, runRefresh, autoKickDone]);

  if (subLoading && queuedProp === null && deliveredProp === null) {
    return (
      <div className="space-y-6">
        <div>
          <h2 className="text-lg font-semibold text-main">Your matches</h2>
          <p className="mt-1 text-sm text-secondary">Loading your shortlist…</p>
        </div>
        <div className="animate-pulse space-y-3">
          <div className="h-24 rounded-lg border border-muted bg-surface" />
          <div className="h-32 rounded-lg border border-muted bg-surface" />
          <div className="h-32 rounded-lg border border-muted bg-surface" />
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-lg font-semibold text-main">Your matches</h2>
        <p className="mt-1 text-sm text-secondary">
          {setupMode
            ? 'You are subscribed. Finish your CV and match preferences so we can score roles against you.'
            : 'Scored against your CV — highest fit first. Use the actions on this page to refresh, open a role, or improve your CV.'}
        </p>
      </div>

      {setupMode && (
        <div
          role="status"
          className="rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-950 dark:border-amber-800 dark:bg-amber-950/40 dark:text-amber-100"
        >
          <p className="font-semibold">Finish setup for better matches</p>
          <p className="mt-1 text-amber-900/90 dark:text-amber-100/90">
            {setupMissing.length > 0
              ? `Still needed: ${setupMissing.slice(0, 6).join(', ')}${setupMissing.length > 6 ? '…' : ''}.`
              : 'Upload a CV and set target role / location preferences under the CV tab.'}
          </p>
          <a
            href="/dashboard/#cv"
            className="mt-2 inline-flex min-h-[40px] items-center rounded-md bg-navy-900 px-3 py-1.5 text-sm font-medium text-white hover:bg-navy-800"
          >
            Open CV hub
          </a>
        </div>
      )}

      {subQueryError && (
        <div
          role="status"
          className="rounded-md border border-muted bg-surface-muted px-3 py-2 text-sm text-secondary"
        >
          Couldn&apos;t refresh plan counters. You can still use this page — try reloading if this
          persists.
        </div>
      )}

      <Panel title="This week">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <p className="text-sm text-secondary tabular-nums">
            {delivered}
            {!unlimited && ` / ${cap}`} delivered
            <span className="mx-1.5 text-muted-strong">·</span>
            {queued} ready to review
            {unlimited && <span className="ml-1 text-accent-700">· unlimited</span>}
          </p>
          <Button
            type="button"
            variant="primary"
            disabled={refreshing}
            onClick={() => void runRefresh(false)}
          >
            {refreshing ? 'Searching…' : 'Find matches'}
          </Button>
        </div>
        {!unlimited && (
          <div className="mt-3 h-1.5 w-full overflow-hidden rounded-full bg-surface-hover">
            <div
              className="h-full rounded-full bg-accent-500 transition-all"
              style={{ width: `${progressPct}%` }}
            />
          </div>
        )}

        {freeProof && (
          <p className="mt-3 text-sm text-secondary">
            Free shortlist (capped).{' '}
            {onUpgrade ? (
              <button type="button" onClick={onUpgrade} className="font-medium underline">
                Upgrade
              </button>
            ) : (
              <a href="/pricing/" className="font-medium underline">
                Upgrade
              </a>
            )}{' '}
            for more weekly matches.
          </p>
        )}

        {queued === 0 && (
          <div className="mt-4 rounded-md border border-muted bg-surface-muted p-4 text-sm text-main">
            <p className="font-medium">
              {setupMode || lastReason === 'need_cv'
                ? 'Add a CV to get scored matches'
                : 'No matches in your queue yet'}
            </p>
            <p className="mt-1 text-secondary">
              {setupMode || lastReason === 'need_cv' ? (
                <>
                  You are not missing a subscription — finish your CV under{' '}
                  <a href="/dashboard/#cv" className="font-medium text-accent-600 underline">
                    CV
                  </a>
                  , then hit Find matches.
                </>
              ) : lastReason ? (
                emptyReasonMessage({
                  ok: true,
                  matches_written: 0,
                  opps_scanned: 0,
                  reason: lastReason,
                  proof: freeProof,
                })
              ) : (
                <>
                  Start by uploading a CV under{' '}
                  <a href="/dashboard/#cv" className="font-medium text-accent-600 underline">
                    CV
                  </a>
                  , set match preferences, then run Find matches.
                </>
              )}
            </p>
            <div className="mt-3 flex flex-wrap gap-2">
              <a
                href="/dashboard/#cv"
                className="inline-flex min-h-[40px] items-center rounded-md bg-navy-900 px-3 py-1.5 text-sm font-medium text-white hover:bg-navy-800"
              >
                Go to CV
              </a>
              <Button
                type="button"
                variant="secondary"
                size="sm"
                disabled={refreshing}
                onClick={() => void runRefresh(false)}
              >
                {refreshing ? 'Searching…' : 'Find matches'}
              </Button>
              {freeProof && onUpgrade && (
                <Button type="button" variant="secondary" size="sm" onClick={onUpgrade}>
                  View plans
                </Button>
              )}
            </div>
          </div>
        )}
      </Panel>

      <OpportunitiesFeed key={refreshKey} initialFilter="matches" preferScoreSort hideFilterChips />
    </div>
  );
}
