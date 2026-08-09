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
          Scored against your CV — highest fit first. Use Find matches to refresh, or open a role
          from the list below.
        </p>
      </div>

      {/* CV missing only — never show when onboarding already stored a CV. */}
      {needsCvUpload && (
        <div
          role="status"
          className="rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-950 dark:border-amber-800 dark:bg-amber-950/40 dark:text-amber-100"
        >
          <p className="font-semibold">CV needed for scored matches</p>
          <p className="mt-1 text-amber-900/90 dark:text-amber-100/90">
            Upload a resume under the CV tab so we can embed your profile and rank roles. Match
            preferences (location, salary) live there too.
          </p>
          <a
            href="/dashboard/#cv"
            className="mt-2 inline-flex min-h-[40px] items-center rounded-md bg-navy-900 px-3 py-1.5 text-sm font-medium text-white hover:bg-navy-800"
          >
            Open CV tab
          </a>
        </div>
      )}

      {/* Preferences only — soft, non-blocking; never “finish your CV”. */}
      {!needsCvUpload && prefLabels.length > 0 && queued === 0 && (
        <div
          role="status"
          className="rounded-md border border-muted bg-surface-muted px-3 py-2 text-sm text-secondary"
        >
          Optional: add {prefLabels.slice(0, 4).join(', ')}
          {prefLabels.length > 4 ? '…' : ''} under{' '}
          <a href="/dashboard/#cv" className="font-medium text-accent-600 underline">
            CV → Preferences
          </a>{' '}
          for tighter filters. You can still run Find matches now.
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

      <Panel title="Your shortlist">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <p className="text-sm text-secondary tabular-nums">
            {delivered} delivered
            <span className="mx-1.5 text-muted-strong">·</span>
            {queued} ready to review
            {unlimited && <span className="ml-1 text-accent-700">· 70%+ fit</span>}
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

        {freeProof && (
          <p className="mt-3 text-sm text-secondary">
            Free shortlist (limited Find matches).{' '}
            {onUpgrade ? (
              <button type="button" onClick={onUpgrade} className="font-medium underline">
                Upgrade
              </button>
            ) : (
              <a href="/pricing/" className="font-medium underline">
                Upgrade
              </a>
            )}{' '}
            for unlimited feed matches above 70% fit and more Find-matches allowance.
          </p>
        )}

        {queued === 0 && (
          <div className="mt-4 rounded-md border border-muted bg-surface-muted p-4 text-sm text-main">
            <p className="font-medium">
              {needsCvUpload ? 'Add a CV to get scored matches' : 'No matches in your queue yet'}
            </p>
            <p className="mt-1 text-secondary">
              {needsCvUpload ? (
                <>
                  Upload a resume under{' '}
                  <a href="/dashboard/#cv" className="font-medium text-accent-600 underline">
                    CV
                  </a>
                  , then hit Find matches.
                </>
              ) : lastReason === 'error' ? (
                <>
                  Match search hit a temporary server problem. Hit Find matches again. If this keeps
                  happening, check your CV under{' '}
                  <a href="/dashboard/#cv" className="font-medium text-accent-600 underline">
                    CV
                  </a>
                  .
                </>
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
                  Press Find matches to score recent roles against your profile. Refine location and
                  salary anytime under{' '}
                  <a href="/dashboard/#cv" className="font-medium text-accent-600 underline">
                    CV → Preferences
                  </a>
                  .
                </>
              )}
            </p>
            <div className="mt-3 flex flex-wrap gap-2">
              {needsCvUpload ? (
                <a
                  href="/dashboard/#cv"
                  className="inline-flex min-h-[40px] items-center rounded-md bg-navy-900 px-3 py-1.5 text-sm font-medium text-white hover:bg-navy-800"
                >
                  Go to CV
                </a>
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
          </div>
        )}
      </Panel>

      <OpportunitiesFeed key={refreshKey} initialFilter="matches" preferScoreSort hideFilterChips />
    </div>
  );
}
