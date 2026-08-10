import { useCallback, useEffect, useState } from 'react';
import type { PlanId } from '@/utils/plans';
import { preferenceMissingLabels } from '@/utils/profileReadiness';
import { Panel } from './Panel';
import { OpportunitiesFeed } from '@/components/OpportunitiesFeed';
import { refreshMyMatches, type MatchRefreshResult } from '@/api/candidates';
import { useToast } from '@/hooks/useToast';
import { Button } from '@/components/ui/Button';

function emptyReasonMessage(res: MatchRefreshResult): string {
  // rate_limited is current; weekly_cap / daily_cap are legacy server reasons.
  if (res.reason === 'rate_limited' || res.reason === 'weekly_cap' || res.reason === 'daily_cap') {
    return res.proof
      ? 'Free search used for today. Subscribe for more Find matches, or try again tomorrow.'
      : 'Search limit reached for today. Try again tomorrow.';
  }
  switch (res.reason) {
    case 'no_inventory':
      return 'No recent roles matched your filters yet. Widen locations/roles under CV → Match preferences.';
    case 'below_threshold':
      return 'Roles were found but none scored at 70%+ match quality. Improve your CV or widen preferences, then try again.';
    case 'need_cv':
      return 'Upload a CV under Dashboard → CV so we can score roles against your profile, then run Find matches.';
    default:
      return 'Match search complete — no new roles above your 70% quality floor yet. Update preferences under CV, then re-run.';
  }
}

/** Prefer server problem detail when refresh fails for a real system error. */
function refreshErrorMessage(err: unknown): string {
  const raw = err instanceof Error ? err.message : String(err);
  if (/embedding_unavailable|could not build match embedding/i.test(raw)) {
    return 'Match embedding is temporarily unavailable. Your CV is saved — try Find matches again in a moment.';
  }
  if (/no_embedding|upload a CV/i.test(raw)) {
    return 'Upload a CV under Dashboard → CV so we can match roles to your profile.';
  }
  const jsonStart = raw.indexOf('{');
  if (jsonStart >= 0) {
    try {
      const parsed = JSON.parse(raw.slice(jsonStart)) as { detail?: string; title?: string };
      if (parsed.detail?.trim()) return parsed.detail.trim();
    } catch {
      /* ignore */
    }
  }
  return 'Could not refresh matches. Try again in a moment.';
}

/**
 * Matches page — scored shortlist + Find matches only.
 * CV upload / preference editing live on the CV hub; this panel only probes
 * for a missing CV when match is not yet possible (or the server says so).
 */
export function MatchesPanel({
  freeProof = false,
  queued: queuedProp,
  delivered: deliveredProp,
  subQueryError = false,
  subLoading = false,
  onUpgrade,
  /** CV file or capabilities on file — false only blocks with upload guidance. */
  cvPresent = true,
  /** Preference gaps (salary, countries, …) — soft tip only, never “upload CV”. */
  preferenceMissing = [] as string[],
}: {
  /** Kept for parent API compatibility; feed is uncapped for paid plans. */
  plan: PlanId;
  freeProof?: boolean;
  queued: number | null;
  delivered: number | null;
  subQueryError?: boolean;
  subLoading?: boolean;
  onUpgrade?: () => void;
  cvPresent?: boolean;
  preferenceMissing?: string[];
}) {
  const { push: toast } = useToast();
  const [refreshing, setRefreshing] = useState(false);
  const [refreshKey, setRefreshKey] = useState(0);

  const queued = queuedProp ?? 0;
  const delivered = deliveredProp ?? 0;

  // Feed is uncapped above the quality floor for paid plans; free proof is a soft shortlist.
  // We no longer show weekly used/cap progress — find-matches uses daily fair-use limits.
  const unlimited = !freeProof;

  const [lastReason, setLastReason] = useState<string | null>(null);
  const [autoKickDone, setAutoKickDone] = useState(false);

  // Only treat as “need CV” when we truly lack a CV — not incomplete preferences.
  const needsCvUpload = !cvPresent || lastReason === 'need_cv';
  const prefLabels = preferenceMissingLabels(preferenceMissing);

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
        if (/no_embedding|need_cv|upload a CV/i.test(msg)) {
          setLastReason('need_cv');
        } else {
          setLastReason('error');
        }
        if (!silent) {
          toast(refreshErrorMessage(err), 'error');
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
    // Only auto-kick when we have a CV signal; otherwise Matches would only
    // get need_cv noise. User can still press Find matches if they know better.
    setAutoKickDone(true);
    if (!cvPresent) return;
    void runRefresh(true);
  }, [subLoading, queued, runRefresh, autoKickDone, cvPresent]);

  if (subLoading && queuedProp === null && deliveredProp === null) {
    return (
      <div className="ds-stack">
        <div>
          <h2 className="ds-section-title">Matches</h2>
          <p className="ds-section-desc">Loading your shortlist…</p>
        </div>
        <div className="animate-pulse space-y-3">
          <div className="h-20 rounded-xl border border-muted bg-surface" />
          <div className="h-28 rounded-xl border border-muted bg-surface" />
          <div className="h-28 rounded-xl border border-muted bg-surface" />
        </div>
      </div>
    );
  }

  return (
    <div className="ds-stack">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div className="min-w-0">
          <h2 className="ds-section-title">Matches</h2>
          <p className="ds-section-desc">
            Highest fit first. Refresh when you want a new shortlist — only roles at 70%+ appear.
          </p>
        </div>
        <Button
          type="button"
          variant="primary"
          disabled={refreshing}
          onClick={() => void runRefresh(false)}
        >
          {refreshing ? 'Searching…' : 'Find matches'}
        </Button>
      </div>

      <p className="ds-meta -mt-2">
        {queued} ready
        <span className="mx-1.5 text-secondary/50">·</span>
        {delivered} delivered
        {unlimited && (
          <>
            <span className="mx-1.5 text-secondary/50">·</span>
            <span className="text-accent-700 dark:text-accent-400">70%+ fit</span>
          </>
        )}
      </p>

      {needsCvUpload && (
        <div role="status" className="ds-callout-warn">
          <p className="font-semibold text-main">CV needed for scored matches</p>
          <p className="mt-1 leading-relaxed">
            Upload a resume under CV so we can rank roles. Preferences (location, salary) live there
            too.
          </p>
          <Button as="a" href="/dashboard/#cv" size="sm" className="mt-3">
            Open CV
          </Button>
        </div>
      )}

      {!needsCvUpload && prefLabels.length > 0 && queued === 0 && (
        <p role="status" className="ds-callout">
          Optional: add {prefLabels.slice(0, 3).join(', ')}
          {prefLabels.length > 3 ? '…' : ''} under{' '}
          <a
            href="/dashboard/#cv"
            className="font-medium text-accent-700 underline-offset-2 hover:underline dark:text-accent-400"
          >
            CV → Preferences
          </a>
          .
        </p>
      )}

      {subQueryError && (
        <p role="status" className="ds-callout">
          Couldn&apos;t refresh plan status. You can still use this page.
        </p>
      )}

      {freeProof && (
        <p className="ds-callout">
          Free shortlist (limited Find matches).{' '}
          {onUpgrade ? (
            <button
              type="button"
              onClick={onUpgrade}
              className="font-medium text-accent-700 underline-offset-2 hover:underline dark:text-accent-400"
            >
              Upgrade
            </button>
          ) : (
            <a
              href="/pricing/"
              className="font-medium text-accent-700 underline-offset-2 hover:underline dark:text-accent-400"
            >
              Upgrade
            </a>
          )}{' '}
          for full feed and more searches.
        </p>
      )}

      {queued === 0 && (
        <Panel title={needsCvUpload ? 'Add a CV to get scored matches' : 'No matches yet'}>
          <p className="text-sm leading-relaxed text-secondary">
            {needsCvUpload ? (
              <>
                Upload a resume under{' '}
                <a
                  href="/dashboard/#cv"
                  className="font-medium text-accent-700 underline-offset-2 hover:underline dark:text-accent-400"
                >
                  CV
                </a>
                , then hit Find matches.
              </>
            ) : lastReason === 'error' ? (
              <>Match search hit a temporary problem. Try Find matches again.</>
            ) : lastReason && lastReason !== 'need_cv' ? (
              emptyReasonMessage({
                ok: true,
                matches_written: 0,
                opps_scanned: 0,
                reason: lastReason,
                proof: freeProof,
              })
            ) : (
              <>
                Press Find matches to score recent roles. Refine filters anytime under{' '}
                <a
                  href="/dashboard/#cv"
                  className="font-medium text-accent-700 underline-offset-2 hover:underline dark:text-accent-400"
                >
                  CV → Preferences
                </a>
                .
              </>
            )}
          </p>
          <div className="mt-4 flex flex-wrap gap-2">
            {needsCvUpload ? (
              <Button as="a" href="/dashboard/#cv" size="sm">
                Go to CV
              </Button>
            ) : null}
            <Button
              type="button"
              variant={needsCvUpload ? 'secondary' : 'primary'}
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
        </Panel>
      )}

      <OpportunitiesFeed key={refreshKey} initialFilter="matches" preferScoreSort hideFilterChips />
    </div>
  );
}
