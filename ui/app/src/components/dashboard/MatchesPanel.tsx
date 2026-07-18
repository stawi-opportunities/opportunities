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
          We couldn&apos;t load your latest match numbers. Refresh in a few seconds — if this keeps
          happening, drop us a line at{' '}
          <a href="mailto:jobs@stawi.org" className="underline">
            jobs@stawi.org
          </a>
          .
        </p>
      </Panel>
    );
  }

  return (
    <div className="space-y-6">
      <Panel title="Your match pipeline">
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <div>
            <p className="text-xs font-medium uppercase tracking-wide text-gray-500 dark:text-gray-400">
              {unlimited ? 'Matched this week' : 'Delivered this week'}
            </p>
            <div className="mt-2 flex items-baseline gap-1">
              <span className="text-2xl font-bold text-gray-900 dark:text-white">{delivered}</span>
              {!unlimited && (
                <span className="text-sm text-gray-500 dark:text-gray-400">/ {cap}</span>
              )}
              {unlimited && (
                <span className="text-sm font-medium text-accent-700 dark:text-accent-400">
                  unlimited
                </span>
              )}
            </div>
            {!unlimited && (
              <div className="mt-2 h-2 w-full overflow-hidden rounded-full bg-gray-100 dark:bg-navy-700">
                <div
                  className="h-full rounded-full bg-accent-500 transition-all duration-700 ease-out"
                  style={{ width: `${progressPct}%` }}
                />
              </div>
            )}
          </div>
          <div>
            <p className="text-xs font-medium uppercase tracking-wide text-gray-500 dark:text-gray-400">
              In your queue
            </p>
            <p className="mt-2 text-2xl font-bold text-gray-900 dark:text-white">{queued}</p>
          </div>
        </div>

        <div className="mt-4 flex flex-wrap items-center gap-3">
          <Button
            type="button"
            variant="primary"
            disabled={refreshing}
            onClick={() => void runRefresh(false)}
          >
            {refreshing ? 'Searching…' : 'Find matches now'}
          </Button>
          <p className="text-xs text-gray-500 dark:text-gray-400">
            Roles above your quality threshold only. Digests follow your schedule in{' '}
            <a href="/dashboard/#settings" className="underline hover:text-accent-600">
              Settings → Notifications
            </a>
            .
          </p>
        </div>

        {freeProof && (
          <div className="mt-4 rounded-md border border-accent-200 bg-accent-50 p-3 text-sm text-gray-800 dark:border-accent-800 dark:bg-accent-950/40 dark:text-gray-200">
            Free proof: up to ~3 quality matches while you evaluate us. Like what you see?{' '}
            {onUpgrade ? (
              <button
                type="button"
                onClick={onUpgrade}
                className="font-medium text-accent-700 underline dark:text-accent-400"
              >
                Subscribe for more →
              </button>
            ) : (
              <a
                href="/pricing/"
                className="font-medium text-accent-700 underline dark:text-accent-400"
              >
                Subscribe for more →
              </a>
            )}
          </div>
        )}
        {!freeProof && plan === 'starter' && (
          <div className="mt-4 rounded-md border border-gray-200 bg-gray-50 p-3 text-sm text-gray-700 dark:border-navy-600 dark:bg-navy-800 dark:text-gray-300">
            Want unlimited discovery and priority match alerts?{' '}
            {onUpgrade ? (
              <button
                type="button"
                onClick={onUpgrade}
                className="font-medium text-accent-600 hover:text-accent-700 dark:text-accent-400 dark:hover:text-accent-300"
              >
                Upgrade to Managed →
              </button>
            ) : (
              <a
                href="/pricing/"
                className="font-medium text-accent-600 hover:text-accent-700 dark:text-accent-400 dark:hover:text-accent-300"
              >
                Upgrade to Managed →
              </a>
            )}
          </div>
        )}
        {delivered === 0 && queued === 0 && (
          <div className="mt-4 rounded-md border border-amber-200 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-800 dark:bg-amber-950/40 dark:text-amber-200">
            <p className="font-medium">No matches yet</p>
            {lastReason && (
              <p className="mt-1 text-amber-800 dark:text-amber-300">
                {emptyReasonMessage({
                  ok: true,
                  matches_written: 0,
                  opps_scanned: 0,
                  reason: lastReason,
                  proof: freeProof,
                })}
              </p>
            )}
            <ol className="mt-2 list-decimal space-y-1 pl-5 text-amber-800 dark:text-amber-300">
              <li>
                Upload a recent CV under{' '}
                <a href="/dashboard/#preferences" className="underline">
                  Preferences
                </a>
              </li>
              <li>Set target roles and locations</li>
              <li>
                Tap <strong>Find matches now</strong>
              </li>
              <li>
                Or check fit on any posting with{' '}
                <a href="/dashboard/#tools" className="underline">
                  Tools → Job fitness
                </a>
              </li>
            </ol>
          </div>
        )}
      </Panel>

      <div>
        <h3 className="mb-3 text-base font-semibold text-gray-900 dark:text-white">
          Roles matched to you
        </h3>
        <p className="mb-4 text-sm text-gray-600 dark:text-gray-400">
          Score reflects CV + preferences fit. Open Apply to go to the employer site; we track that
          you applied. Dismiss weak fits so future digests stay sharp.
        </p>
        <OpportunitiesFeed key={refreshKey} initialFilter="matches" />
      </div>
    </div>
  );
}
