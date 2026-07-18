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
      return 'No recent roles matched your filters yet. Widen locations/roles in Preferences, or browse /jobs/.';
    case 'below_threshold':
      return 'Roles were found but none cleared your quality bar. Update your CV or preferences, then try again.';
    default:
      return 'Match search complete — no new roles above your quality threshold yet. Try updating preferences or CV.';
  }
}

/**
 * Matches section for paid subscribers:
 *  - weekly delivery counters (plan caps; Managed is unlimited)
 *  - live list of matched opportunities
 *  - on-demand refresh so users don't wait only for digests
 *
 * Managed users get the same match feed as Starter (unlimited). We do not
 * hide matches behind agent copy — that destroyed value for the highest-
 * paying tier.
 */
export function MatchesPanel({
  plan,
  freeProof = false,
  queued,
  delivered,
  subQueryError,
  onUpgrade,
}: {
  plan: PlanId;
  /** Unpaid users get proof-tier matches (capped) before subscribe. */
  freeProof?: boolean;
  queued: number | null;
  delivered: number | null;
  subQueryError: boolean;
  onUpgrade?: () => void;
}) {
  const { push: toast } = useToast();
  const [refreshing, setRefreshing] = useState(false);
  const [refreshKey, setRefreshKey] = useState(0);

  const planInfo = planById(plan);
  const unlimited = !freeProof && planInfo.matchesPerWeek === null;
  const cap = freeProof ? 3 : (planInfo.matchesPerWeek ?? 0);
  const progressPct =
    !unlimited && cap > 0 ? Math.min(100, Math.round(((delivered ?? 0) / cap) * 100)) : 0;

  const [lastReason, setLastReason] = useState<string | null>(null);

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
        if (/no_embedding|embedding/i.test(msg)) {
          if (!silent) {
            toast('Upload a CV in Preferences so we can match roles to your profile.', 'error');
          }
        } else if (!silent) {
          // Free proof is allowed — never claim a paywall on refresh errors.
          toast('Could not refresh matches. Try again in a moment.', 'error');
        }
      } finally {
        setRefreshing(false);
      }
    },
    [toast]
  );

  // Auto-kick matching when the queue is empty so paid users get value quickly
  // after checkout / CV upload (rate-limited server-side via gap-fill).
  useEffect(() => {
    if (subQueryError) return;
    if (queued === null) return;
    if (queued > 0) return;
    void runRefresh(true);
  }, [queued, subQueryError, runRefresh]);

  if (subQueryError || queued === null || delivered === null) {
    return (
      <Panel title="Matches">
        <p className="text-sm text-amber-700 dark:text-amber-300">
          Couldn&apos;t load matches. Try again.
        </p>
      </Panel>
    );
  }

  return (
    <div className="space-y-6">
      <Panel title="Matches">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <p className="text-sm text-gray-600 dark:text-gray-300 tabular-nums">
            {delivered}
            {!unlimited && ` / ${cap}`} this week
            <span className="mx-1.5 text-gray-300">·</span>
            {queued} queued
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
          <div className="mt-3 h-1.5 w-full overflow-hidden rounded-full bg-gray-100 dark:bg-navy-700">
            <div
              className="h-full rounded-full bg-accent-500 transition-all"
              style={{ width: `${progressPct}%` }}
            />
          </div>
        )}

        {freeProof && (
          <p className="mt-3 text-sm text-gray-600 dark:text-gray-300">
            Free shortlist (capped).{' '}
            {onUpgrade ? (
              <button type="button" onClick={onUpgrade} className="font-medium underline">
                Upgrade
              </button>
            ) : (
              <a href="/pricing/" className="font-medium underline">
                Upgrade
              </a>
            )}
          </p>
        )}
        {delivered === 0 && queued === 0 && (
          <div className="mt-4 rounded-md border border-amber-200 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-800 dark:bg-amber-950/40 dark:text-amber-200">
            <p className="font-medium">No matches yet</p>
            {lastReason && (
              <p className="mt-1">
                {emptyReasonMessage({
                  ok: true,
                  matches_written: 0,
                  opps_scanned: 0,
                  reason: lastReason,
                  proof: freeProof,
                })}
              </p>
            )}
            <p className="mt-2">
              Upload a CV in{' '}
              <a href="/dashboard/#preferences" className="underline">
                Preferences
              </a>
              , then hit Find matches.
            </p>
          </div>
        )}
      </Panel>

      <OpportunitiesFeed key={refreshKey} initialFilter="matches" />
    </div>
  );
}
