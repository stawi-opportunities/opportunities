import { useCallback, useEffect, useState } from 'react';
import { planById, type PlanId } from '@/utils/plans';
import { Panel } from './Panel';
import { OpportunitiesFeed } from '@/components/OpportunitiesFeed';
import {
  refreshMyMatches,
  type MatchRefreshResult,
  type OpportunityFilter,
} from '@/api/candidates';
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
      return 'No recent roles matched your filters yet. Widen locations/roles under CV → Match preferences, or switch to Browse.';
    case 'below_threshold':
      return 'Roles were found but none cleared your quality bar. Improve your CV score, then try again.';
    default:
      return 'Match search complete — no new roles above your quality threshold yet. Update your CV or preferences, then re-run.';
  }
}

type Mode = 'matches' | 'browse';

/**
 * Matches section: scored shortlist (default) + optional full browse mode.
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
  freeProof?: boolean;
  queued: number | null;
  delivered: number | null;
  subQueryError: boolean;
  onUpgrade?: () => void;
}) {
  const { push: toast } = useToast();
  const [refreshing, setRefreshing] = useState(false);
  const [refreshKey, setRefreshKey] = useState(0);
  const [mode, setMode] = useState<Mode>(() => {
    if (typeof window === 'undefined') return 'matches';
    const f = new URL(window.location.href).searchParams.get('filter');
    return f === 'all' ? 'browse' : 'matches';
  });

  const planInfo = planById(plan);
  const unlimited = !freeProof && planInfo.matchesPerWeek === null;
  const cap = freeProof ? 3 : (planInfo.matchesPerWeek ?? 0);
  const progressPct =
    !unlimited && cap > 0 ? Math.min(100, Math.round(((delivered ?? 0) / cap) * 100)) : 0;

  const [lastReason, setLastReason] = useState<string | null>(null);

  const feedFilter: OpportunityFilter = mode === 'matches' ? 'matches' : 'all';

  const setModeAndUrl = (m: Mode) => {
    setMode(m);
    if (typeof window === 'undefined') return;
    const url = new URL(window.location.href);
    if (m === 'browse') url.searchParams.set('filter', 'all');
    else url.searchParams.set('filter', 'matches');
    window.history.replaceState({}, '', url.toString());
  };

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
            toast(
              'Upload a CV under Dashboard → CV so we can match roles to your profile.',
              'error'
            );
          }
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
    if (subQueryError) return;
    if (queued === null) return;
    if (queued > 0) return;
    if (mode !== 'matches') return;
    void runRefresh(true);
  }, [queued, subQueryError, runRefresh, mode]);

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
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="text-lg font-semibold text-main">
            {mode === 'matches' ? 'Your matches' : 'Browse opportunities'}
          </h2>
          <p className="mt-1 text-sm text-secondary">
            {mode === 'matches'
              ? 'Scored against your CV — highest fit first. Apply from each card or open the role for fitness detail.'
              : 'All opportunities in your feed (not only scored matches). Switch back to Matches for your shortlist.'}
          </p>
        </div>
        <div
          className="inline-flex rounded-lg border border-muted p-0.5"
          role="group"
          aria-label="Matches or browse"
        >
          <button
            type="button"
            onClick={() => setModeAndUrl('matches')}
            className={`rounded-md px-3 py-1.5 text-sm font-medium ${
              mode === 'matches'
                ? 'bg-accent-500/15 text-accent-700 dark:text-accent-300'
                : 'text-secondary hover:text-main'
            }`}
          >
            Matches
          </button>
          <button
            type="button"
            onClick={() => setModeAndUrl('browse')}
            className={`rounded-md px-3 py-1.5 text-sm font-medium ${
              mode === 'browse'
                ? 'bg-accent-500/15 text-accent-700 dark:text-accent-300'
                : 'text-secondary hover:text-main'
            }`}
          >
            Browse
          </button>
        </div>
      </div>

      {mode === 'matches' && (
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
                Upload a CV under{' '}
                <a href="/dashboard/#cv" className="underline">
                  CV
                </a>
                , set match preferences, then hit Find matches.
              </p>
            </div>
          )}
        </Panel>
      )}

      <OpportunitiesFeed
        key={`${refreshKey}-${mode}`}
        initialFilter={feedFilter}
        preferScoreSort={mode === 'matches'}
        hideFilterChips={mode === 'matches'}
      />
    </div>
  );
}
