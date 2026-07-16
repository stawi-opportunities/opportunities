import { useCallback, useEffect, useState } from 'react';
import { planById, type PlanId } from '@/utils/plans';
import { Panel } from './Panel';
import { OpportunitiesFeed } from '@/components/OpportunitiesFeed';
import { refreshMyMatches } from '@/api/candidates';
import { useToast } from '@/hooks/useToast';
import { Button } from '@/components/ui/Button';

/**
 * Matches section for paid subscribers:
 *  - weekly delivery counters (plan caps)
 *  - live list of matched opportunities (filter=matches feed)
 *  - on-demand refresh so users don't wait only for the weekly Trustage digest
 */
export function MatchesPanel({
  plan,
  queued,
  delivered,
  subQueryError,
  onUpgrade,
}: {
  plan: PlanId;
  queued: number | null;
  delivered: number | null;
  subQueryError: boolean;
  onUpgrade?: () => void;
}) {
  const { push: toast } = useToast();
  const [refreshing, setRefreshing] = useState(false);
  const [refreshKey, setRefreshKey] = useState(0);

  const planInfo = planById(plan);
  const cap = planInfo.matchesPerWeek ?? 0;
  const progressPct = cap > 0 ? Math.min(100, Math.round(((delivered ?? 0) / cap) * 100)) : 0;

  const runRefresh = useCallback(
    async (silent: boolean) => {
      setRefreshing(true);
      try {
        const res = await refreshMyMatches();
        if (!silent) {
          if (res.matches_written > 0) {
            toast(
              `Found ${res.matches_written} new match${res.matches_written === 1 ? '' : 'es'}.`,
              'success'
            );
          } else {
            toast('Match search complete — no new roles above your quality threshold yet.', 'info');
          }
        }
        setRefreshKey((k) => k + 1);
      } catch (err) {
        const msg = err instanceof Error ? err.message : String(err);
        if (/no_embedding|embedding/i.test(msg)) {
          if (!silent) {
            toast('Upload a CV in Preferences so we can match roles to your profile.', 'error');
          }
        } else if (/subscription|payment/i.test(msg)) {
          if (!silent) toast('An active subscription is required to refresh matches.', 'error');
        } else if (!silent) {
          toast('Could not refresh matches. Try again in a moment.', 'error');
        }
      } finally {
        setRefreshing(false);
      }
    },
    [toast]
  );

  // Auto-kick matching when the queue is empty so paid users get value quickly
  // after checkout / CV upload (still rate-limited server-side via gap-fill).
  useEffect(() => {
    if (plan === 'managed') return;
    if (subQueryError) return;
    if (queued === null) return;
    if (queued > 0) return;
    void runRefresh(true);
  }, [plan, queued, subQueryError, runRefresh]);

  if (plan === 'managed') {
    return (
      <Panel title="Matches">
        <p className="text-sm text-gray-600 dark:text-gray-400">
          Your agent hand-picks roles that pass their screen before they reach you. Expect curated
          matches in your inbox and a weekly summary on your 1:1 call.
        </p>
      </Panel>
    );
  }

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
        <div className="grid grid-cols-2 gap-4">
          <div>
            <p className="text-xs font-medium uppercase tracking-wide text-gray-500 dark:text-gray-400">
              Delivered this week
            </p>
            <div className="mt-2 flex items-baseline gap-1">
              <span className="text-2xl font-bold text-gray-900 dark:text-white">{delivered}</span>
              <span className="text-sm text-gray-500 dark:text-gray-400">/ {cap}</span>
            </div>
            <div className="mt-2 h-2 w-full overflow-hidden rounded-full bg-gray-100 dark:bg-navy-700">
              <div
                className="h-full rounded-full bg-accent-500 transition-all duration-700 ease-out"
                style={{ width: `${progressPct}%` }}
              />
            </div>
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
            Roles above your quality threshold only. Email summaries follow your schedule in{' '}
            <a href="/dashboard/#settings" className="underline hover:text-accent-600">
              Settings → Notifications
            </a>{' '}
            (daily or weekly).
          </p>
        </div>

        {plan === 'starter' && (
          <div className="mt-4 rounded-md border border-gray-200 bg-gray-50 p-3 text-sm text-gray-700 dark:border-navy-600 dark:bg-navy-800 dark:text-gray-300">
            Want 5× the matches and priority placement in the queue?{' '}
            {onUpgrade ? (
              <button
                type="button"
                onClick={onUpgrade}
                className="font-medium text-accent-600 hover:text-accent-700 dark:text-accent-400 dark:hover:text-accent-300"
              >
                Upgrade to Pro →
              </button>
            ) : (
              <a
                href="/pricing/"
                className="font-medium text-accent-600 hover:text-accent-700 dark:text-accent-400 dark:hover:text-accent-300"
              >
                Upgrade to Pro →
              </a>
            )}
          </div>
        )}
        {delivered === 0 && queued === 0 && (
          <p className="mt-4 text-sm text-gray-500 dark:text-gray-400">
            First matches usually appear within minutes of CV upload and payment — use{' '}
            <strong>Find matches now</strong> anytime.
          </p>
        )}
      </Panel>

      <div>
        <h3 className="mb-3 text-base font-semibold text-gray-900 dark:text-white">
          Roles matched to you
        </h3>
        <p className="mb-4 text-sm text-gray-600 dark:text-gray-400">
          Apply from here — score reflects CV + preferences fit. Dismiss weak fits from the card
          actions so future digests stay sharp.
        </p>
        {/* remount when refresh completes so the feed reloads */}
        <OpportunitiesFeed key={refreshKey} initialFilter="matches" />
      </div>
    </div>
  );
}
