import type { FeedItem } from '@/api/candidates';
import { useI18n } from '@/i18n/I18nProvider';
import type { StringKey } from '@/i18n/strings';
import { useAuth } from '@/providers/AuthProvider';
import { useSubscription } from '@/hooks/useSubscription';
import { Icon } from '@/components/ui/Icon';
import { DeadlineDate } from '@/components/ui/DeadlineDate';
import { getTypeMeta } from '@/constants/opportunityTypes';
import { urgencyForDeadline } from '@/utils/deadline';
import { scoreToPercent, whyMatched } from '@/utils/matchScore';

const KIND_PATH: Record<string, string> = {
  job: 'jobs',
  scholarship: 'scholarships',
  tender: 'tenders',
  deal: 'deals',
  funding: 'funding',
};

function detailUrl(snapshot: OpportunitySnapshot): string {
  const path = KIND_PATH[snapshot.kind ?? ''] ?? snapshot.kind;
  return snapshot.slug && path ? `/${path}/${snapshot.slug}/` : '';
}

export interface OpportunitySnapshot {
  title: string;
  company?: string;
  location?: string;
  posted_at?: string;
  deadline?: string;
  salary_min?: number;
  salary_max?: number;
  currency?: string;
  kind?: string;
  id?: string;
  slug?: string;
  has_how_to_apply?: boolean;
  apply_url?: string;
}

interface Props {
  item: FeedItem;
  snapshot: OpportunitySnapshot | null;
  onStar: (opportunityId: string) => void;
  onUnstar: (opportunityId: string) => void;
  onApply: (opportunityId: string) => void;
  /** Hide match from feed/digests when match_id is present. */
  onDismiss?: (matchId: string, opportunityId: string) => void;
  isPending?: boolean;
}

const STATUS_KEYS: Record<string, StringKey> = {
  applied: 'status.applied',
  responded: 'status.responded',
  interview: 'status.interview',
  offer: 'status.offer',
  rejected: 'status.rejected',
  hired: 'status.hired',
};

export function OpportunityCard({
  item,
  snapshot,
  onStar,
  onUnstar,
  onApply,
  onDismiss,
  isPending,
}: Props) {
  const { t } = useI18n();
  const { hasSession } = useAuth();
  const sub = useSubscription();
  const st = sub.data?.status;
  const active = hasSession && (st === 'active' || st === 'past_due' || st === 'trial');
  const title = snapshot?.title ?? item.opportunity_id;
  const company = snapshot?.company ?? '';
  const location = snapshot?.location ?? '';
  const isNew = snapshot?.posted_at
    ? Date.now() - new Date(snapshot.posted_at).getTime() < 24 * 60 * 60 * 1000
    : false;
  const closingUrgency = urgencyForDeadline(snapshot?.deadline);
  const isClosingSoon =
    closingUrgency === 'today' || closingUrgency === 'urgent' || closingUrgency === 'soon';
  const matchPct = scoreToPercent(item.score);
  const isMatched = matchPct != null && matchPct > 0;
  const why = whyMatched(item.score);
  const canDismiss = Boolean(item.match_id && onDismiss);

  return (
    <li
      className={`flex flex-col gap-3 rounded-lg border bg-white p-3.5 sm:flex-row sm:items-start sm:gap-4 sm:p-4 dark:bg-navy-900 ${
        isMatched
          ? 'border-l-4 border-l-emerald-500 border-gray-200 dark:border-navy-700'
          : 'border-gray-200 dark:border-navy-700'
      }`}
    >
      <div className="min-w-0 flex-1">
        <div className="flex items-start justify-between gap-2">
          <div className="min-w-0">
            <h3 className="text-base font-semibold leading-snug text-gray-900 dark:text-white">
              <span className="break-words">{title}</span>
              {isNew && (
                <span className="ml-2 inline-flex items-center rounded-full bg-emerald-100 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300">
                  {t('card.new')}
                </span>
              )}
              {isClosingSoon && (
                <span className="ml-2 inline-flex items-center rounded-full bg-orange-100 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-orange-800 dark:bg-orange-900/30 dark:text-orange-300">
                  {t('deadline.closingSoon')}
                </span>
              )}
              {snapshot?.kind && getTypeMeta(snapshot.kind) && (
                <span className="ml-2 inline-flex items-center gap-1 rounded-full bg-gray-100 px-2 py-0.5 text-[10px] font-medium text-gray-600 dark:bg-navy-800 dark:text-gray-300">
                  <Icon name={getTypeMeta(snapshot.kind)!.iconName} size={10} />
                  {t(getTypeMeta(snapshot.kind)!.labelKey)}
                </span>
              )}
            </h3>
            {(company || location) && (
              <p className="mt-0.5 text-sm text-gray-600 dark:text-gray-300">
                {company}
                {company && location && ' · '}
                {location}
              </p>
            )}
            <DeadlineDate
              deadline={snapshot?.deadline}
              posted_at={snapshot?.posted_at}
              kind={snapshot?.kind}
              variant="full"
              className="mt-1 block text-sm"
            />
            {why && (
              <p
                className="mt-1.5 text-xs text-emerald-800 dark:text-emerald-300/90"
                data-testid="why-matched"
              >
                {why}
              </p>
            )}
          </div>
          {isMatched && matchPct != null && (
            <span
              className="shrink-0 rounded-full bg-emerald-50 px-2.5 py-0.5 text-xs font-semibold tabular-nums text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300"
              title="Match score (CV + preferences fit)"
            >
              {matchPct}
              {t('card.match')}
            </span>
          )}
        </div>

        <div className="mt-3 flex flex-wrap items-stretch gap-2 sm:items-center">
          {item.application ? (
            <span className="inline-flex items-center rounded-full bg-blue-50 px-2.5 py-0.5 text-xs font-medium text-blue-700 dark:bg-blue-900/30 dark:text-blue-300">
              {STATUS_KEYS[item.application.status]
                ? t(STATUS_KEYS[item.application.status]!)
                : item.application.status}
            </span>
          ) : (
            <>
              {active && snapshot?.has_how_to_apply && detailUrl(snapshot) ? (
                <a
                  href={detailUrl(snapshot)}
                  className="inline-flex min-h-[44px] flex-1 items-center justify-center rounded-md bg-navy-900 px-3 py-2 text-sm font-medium text-white transition-colors hover:bg-navy-800 sm:flex-none"
                >
                  {t('card.howToApply')}
                </a>
              ) : (
                <button
                  type="button"
                  onClick={() => onApply(item.opportunity_id)}
                  disabled={isPending}
                  className="inline-flex min-h-[44px] flex-1 items-center justify-center rounded-md bg-navy-900 px-3 py-2 text-sm font-medium text-white transition-colors hover:bg-navy-800 disabled:opacity-50 sm:flex-none"
                >
                  {t('cta.apply')}
                </button>
              )}
            </>
          )}
          {item.starred ? (
            <button
              type="button"
              onClick={() => onUnstar(item.opportunity_id)}
              aria-label="Remove from saved"
              disabled={isPending}
              className="inline-flex min-h-[44px] items-center justify-center rounded-md border border-gray-300 bg-white px-3 py-2 text-sm font-medium text-amber-700 transition-colors hover:bg-amber-50 disabled:opacity-50 dark:border-navy-700 dark:bg-navy-900 dark:text-amber-300 dark:hover:bg-amber-900/20"
            >
              ★ {t('cta.saved')}
            </button>
          ) : (
            <button
              type="button"
              onClick={() => onStar(item.opportunity_id)}
              aria-label="Save opportunity"
              disabled={isPending}
              className="inline-flex min-h-[44px] items-center justify-center rounded-md border border-gray-300 bg-white px-3 py-2 text-sm font-medium text-gray-700 transition-colors hover:bg-gray-50 disabled:opacity-50 dark:border-navy-700 dark:bg-navy-900 dark:text-gray-300 dark:hover:bg-navy-800"
            >
              ☆ {t('cta.save')}
            </button>
          )}
          {canDismiss && (
            <button
              type="button"
              onClick={() => onDismiss!(item.match_id!, item.opportunity_id)}
              aria-label="Dismiss match"
              disabled={isPending}
              title="Hide this match — improves future digests"
              className="inline-flex min-h-[44px] items-center justify-center rounded-md border border-gray-300 bg-white px-3 py-2 text-sm font-medium text-gray-600 transition-colors hover:bg-gray-50 disabled:opacity-50 dark:border-navy-700 dark:bg-navy-900 dark:text-gray-400 dark:hover:bg-navy-800"
            >
              Dismiss
            </button>
          )}
        </div>
      </div>
    </li>
  );
}
