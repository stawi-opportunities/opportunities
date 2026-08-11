import type { FeedItem } from '@/api/candidates';
import { useI18n } from '@/i18n/I18nProvider';
import type { StringKey } from '@/i18n/strings';
import { useAuth } from '@/providers/AuthProvider';
import { useSubscription } from '@/hooks/useSubscription';
import { Icon } from '@/components/ui/Icon';
import { Badge } from '@/components/ui/Badge';
import { Button } from '@/components/ui/Button';
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

/**
 * `triage` — match shortlist: Save + Dismiss only; body opens the listing.
 * `full` — apply/save (and optional dismiss) for saved, applications, mixed feeds.
 */
export type OpportunityCardActionsMode = 'full' | 'triage';

interface Props {
  item: FeedItem;
  snapshot: OpportunitySnapshot | null;
  onStar: (opportunityId: string) => void;
  onUnstar: (opportunityId: string) => void;
  onApply?: (opportunityId: string) => void;
  onDismiss?: (matchId: string, opportunityId: string) => void;
  isPending?: boolean;
  actionsMode?: OpportunityCardActionsMode;
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
  actionsMode = 'full',
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
  const typeMeta = snapshot?.kind ? getTypeMeta(snapshot.kind) : null;
  const listingHref = snapshot ? detailUrl(snapshot) : '';
  const isTriage = actionsMode === 'triage';

  const meta = (
    <>
      <div className="flex flex-wrap items-center gap-1.5">
        {isNew && <Badge variant="success">{t('card.new')}</Badge>}
        {isClosingSoon && <Badge variant="warning">{t('deadline.closingSoon')}</Badge>}
        {typeMeta && (
          <Badge variant="neutral" className="gap-1 font-normal">
            <Icon name={typeMeta.iconName} size={10} />
            {t(typeMeta.labelKey)}
          </Badge>
        )}
        {/* Triage (match list) has no Apply row — surface application status here. */}
        {isTriage && item.application && (
          <Badge variant="info">
            {STATUS_KEYS[item.application.status]
              ? t(STATUS_KEYS[item.application.status]!)
              : item.application.status}
          </Badge>
        )}
      </div>
      <h3 className="mt-1.5 text-base font-semibold leading-snug tracking-tight text-main">
        <span className="break-words">{title}</span>
      </h3>
      {(company || location) && (
        <p className="mt-0.5 text-sm text-secondary">
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
        className="mt-1 block text-sm text-secondary"
      />
      {why && (
        <p
          className="mt-1.5 text-xs leading-relaxed text-accent-700 dark:text-accent-300"
          data-testid="why-matched"
        >
          {why}
        </p>
      )}
    </>
  );

  const scoreBadge =
    isMatched && matchPct != null ? (
      <span
        className="shrink-0 rounded-lg bg-accent-500/10 px-2.5 py-1 text-sm font-semibold tabular-nums text-accent-700 dark:text-accent-300"
        title="Match score (CV + preferences fit)"
      >
        {matchPct}
        {t('card.match')}
      </span>
    ) : null;

  const saveButton = item.starred ? (
    <Button
      type="button"
      variant="secondary"
      size="md"
      onClick={() => onUnstar(item.opportunity_id)}
      aria-label="Remove from saved"
      disabled={isPending}
    >
      ★ {t('cta.saved')}
    </Button>
  ) : (
    <Button
      type="button"
      variant="secondary"
      size="md"
      onClick={() => onStar(item.opportunity_id)}
      aria-label="Save opportunity"
      disabled={isPending}
    >
      ☆ {t('cta.save')}
    </Button>
  );

  const dismissButton = canDismiss ? (
    <Button
      type="button"
      variant="ghost"
      size="md"
      onClick={() => onDismiss!(item.match_id!, item.opportunity_id)}
      aria-label="Dismiss match"
      disabled={isPending}
      title="Hide this match — improves future digests"
    >
      {t('cta.dismiss')}
    </Button>
  ) : null;

  return (
    <li
      className={`ds-list-item flex flex-col gap-3 sm:flex-row sm:items-start sm:gap-4 ${
        isMatched ? 'border-l-[3px] border-l-accent-500' : ''
      } ${isTriage && listingHref ? 'transition-colors hover:bg-surface-hover/60' : ''}`}
    >
      <div className="min-w-0 flex-1">
        {isTriage && listingHref ? (
          <a
            href={listingHref}
            className="group flex items-start justify-between gap-3 rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-500/40 focus-visible:ring-offset-2 focus-visible:ring-offset-page"
            aria-label={`View listing: ${title}`}
          >
            <div className="min-w-0">
              {meta}
              <p className="mt-2 text-xs font-medium text-accent-700 opacity-0 transition-opacity group-hover:opacity-100 group-focus-visible:opacity-100 dark:text-accent-400">
                View listing →
              </p>
            </div>
            {scoreBadge}
          </a>
        ) : (
          <div className="flex items-start justify-between gap-3">
            <div className="min-w-0">{meta}</div>
            {scoreBadge}
          </div>
        )}

        <div className="mt-3 flex flex-wrap items-stretch gap-2 sm:items-center">
          {isTriage ? (
            <>
              {saveButton}
              {dismissButton}
            </>
          ) : (
            <>
              {item.application ? (
                <Badge variant="info">
                  {STATUS_KEYS[item.application.status]
                    ? t(STATUS_KEYS[item.application.status]!)
                    : item.application.status}
                </Badge>
              ) : (
                <>
                  {active && snapshot?.has_how_to_apply && listingHref ? (
                    <Button as="a" href={listingHref} size="md" className="flex-1 sm:flex-none">
                      {t('card.howToApply')}
                    </Button>
                  ) : (
                    onApply && (
                      <Button
                        type="button"
                        size="md"
                        className="flex-1 sm:flex-none"
                        onClick={() => onApply(item.opportunity_id)}
                        disabled={isPending}
                      >
                        {t('cta.apply')}
                      </Button>
                    )
                  )}
                </>
              )}
              {saveButton}
              {dismissButton}
            </>
          )}
        </div>
      </div>
    </li>
  );
}
